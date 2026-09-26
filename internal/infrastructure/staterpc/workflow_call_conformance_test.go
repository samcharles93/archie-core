package staterpc

import (
	"errors"
	"testing"

	"github.com/samcharles93/archie-core/internal/domain/storecontract"
	"github.com/samcharles93/archie-core/internal/domain/workflow"
	"github.com/samcharles93/archie-core/internal/infrastructure/postgres/pgstore"
)

// workflowCallerContract is the workflow.call surface both adapters must
// satisfy identically (§11), including refusal-sentinel fidelity across the
// wire (docs/prds/workflow-calls.md).
type workflowCallerContract interface {
	storecontract.WorkflowCaller
}

// TestWorkflowCallerConformance drives StartCall/CallStatus through the local
// PostgreSQL store and a bufconn-backed gRPC client.
func TestWorkflowCallerConformance(t *testing.T) {
	for _, mode := range []string{"local", "grpc"} {
		t.Run(mode, func(t *testing.T) {
			ctx := t.Context()
			local := pgstore.Open(t)
			var c workflowCallerContract = local
			if mode == "grpc" {
				c = remoteTaskStore(t, local, nil, nil)
			}

			caller, err := local.EnqueueChatTask(ctx, "acme", "widget", "caller", "body", "implement", "")
			if err != nil {
				t.Fatalf("EnqueueChatTask: %v", err)
			}
			if err := local.Transition(ctx, caller.ID, caller.Status, workflow.StatusRunning, "started"); err != nil {
				t.Fatalf("Transition caller: %v", err)
			}

			// A running caller's callee inherits its row and links back to it.
			callee, err := c.StartCall(ctx, caller.ID, "callee", map[string]any{"src_ip": "10.0.0.9"})
			if err != nil || callee == nil {
				t.Fatalf("StartCall: (%+v, %v)", callee, err)
			}
			if callee.Workflow != "callee" || callee.CallParentTaskID != caller.ID || callee.CallDepth != 1 {
				t.Fatalf("callee = %+v, want the parent link and depth 1", callee)
			}
			if callee.Org != caller.Org || callee.Identity != caller.Identity {
				t.Fatalf("callee org/identity = %q/%q, want the caller's %q/%q", callee.Org, callee.Identity, caller.Org, caller.Identity)
			}
			if callee.Owner != caller.Owner || callee.Repo != caller.Repo {
				t.Fatalf("callee owner/repo = %s/%s, want the caller's %s/%s", callee.Owner, callee.Repo, caller.Owner, caller.Repo)
			}
			if callee.Source != "chat" || callee.IssueNumber == caller.IssueNumber {
				t.Fatalf("callee source/issue = %s/%d, want a chat source with a fresh synthetic number", callee.Source, callee.IssueNumber)
			}
			if callee.Inputs["src_ip"] != "10.0.0.9" {
				t.Fatalf("callee inputs = %+v, want the call's inputs", callee.Inputs)
			}

			// A callee that has not moved yet answers its queued status with
			// no detail; after a transition the detail is the latest one.
			status, detail, err := c.CallStatus(ctx, caller.ID, callee.ID)
			if err != nil || status != workflow.StatusQueued || detail != "" {
				t.Fatalf("CallStatus queued = (%q, %q, %v), want (queued, \"\", nil)", status, detail, err)
			}
			if err := local.Transition(ctx, callee.ID, callee.Status, workflow.StatusRunning, "started"); err != nil {
				t.Fatalf("Transition callee to running: %v", err)
			}
			if err := local.Transition(ctx, callee.ID, workflow.StatusRunning, workflow.StatusCompleted, "contained 10.0.0.9"); err != nil {
				t.Fatalf("Transition callee to completed: %v", err)
			}
			status, detail, err = c.CallStatus(ctx, caller.ID, callee.ID)
			if err != nil || status != workflow.StatusCompleted || detail != "contained 10.0.0.9" {
				t.Fatalf("CallStatus completed = (%q, %q, %v), want the terminal status and detail", status, detail, err)
			}

			// A caller reads only its own callees, over both adapters.
			_, _, err = c.CallStatus(ctx, 99999, callee.ID)
			if !errors.Is(err, storecontract.ErrCallNotYours) {
				t.Fatalf("CallStatus foreign caller = %v, want ErrCallNotYours", err)
			}
			_, _, err = c.CallStatus(ctx, caller.ID, 99999)
			if !errors.Is(err, storecontract.ErrCallNotYours) {
				t.Fatalf("CallStatus missing callee = %v, want ErrCallNotYours", err)
			}

			// Refusals: a caller that is not running, and the depth limit. The
			// sentinels cross the wire, so both adapters report the same one.
			if _, err := c.StartCall(ctx, callee.ID, "deeper", nil); !errors.Is(err, storecontract.ErrCallCallerNotRunning) {
				t.Fatalf("StartCall from a completed caller = %v, want ErrCallCallerNotRunning", err)
			}
			// The depth limit is re-checked by the store, not just the engine:
			// walk a chain of callees, claiming each so it is running, until
			// the insert itself refuses at MaxCallDepth.
			current, err := c.StartCall(ctx, caller.ID, "deeper", nil)
			if err != nil {
				t.Fatalf("StartCall at depth 1: %v", err)
			}
			for current.CallDepth < workflow.MaxCallDepth {
				claimed, err := local.ClaimByIssue(ctx, current.Owner, current.Repo, current.IssueNumber)
				if err != nil || claimed == nil {
					t.Fatalf("claim depth %d callee: (%+v, %v)", current.CallDepth, claimed, err)
				}
				current, err = c.StartCall(ctx, current.ID, "deeper", nil)
				if err != nil {
					t.Fatalf("StartCall at depth %d: %v", claimed.CallDepth+1, err)
				}
			}
			if _, err := local.ClaimByIssue(ctx, current.Owner, current.Repo, current.IssueNumber); err != nil {
				t.Fatalf("claim depth %d callee: %v", current.CallDepth, err)
			}
			if _, err := c.StartCall(ctx, current.ID, "deeper", nil); !errors.Is(err, storecontract.ErrCallDepthExceeded) {
				t.Fatalf("StartCall past the depth limit = %v, want ErrCallDepthExceeded", err)
			}
		})
	}
}
