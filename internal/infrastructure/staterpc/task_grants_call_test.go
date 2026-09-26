package staterpc

import (
	"errors"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pb "github.com/samcharles93/archie-core/internal/contracts/state/v1"
	"github.com/samcharles93/archie-core/internal/domain/storecontract"
	"github.com/samcharles93/archie-core/internal/domain/workflow"
)

// TestTaskGrantAuthorizesTheWorkflowCallRPCs: a workflow.call step starts its
// callee and reads it back while waiting, so the caller's task grant must
// reach the two call RPCs for its own task -- and nothing else's
// (docs/prds/workflow-calls.md). CallStatus's parent check stays server-side,
// so the grant itself only ever verifies the request names the granted task.
func TestTaskGrantAuthorizesTheWorkflowCallRPCs(t *testing.T) {
	const adminToken = "daemon-admin-token"
	_, dial := grantsServer(t, adminToken)
	admin := dial(t, adminToken)
	ctx := t.Context()

	caller, err := admin.EnqueueChatTask(ctx, "acme", "widget", "caller", "body", "implement", "")
	if err != nil {
		t.Fatal(err)
	}
	other, err := admin.EnqueueChatTask(ctx, "acme", "widget", "other", "body", "implement", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := admin.Transition(ctx, caller.ID, caller.Status, workflow.StatusRunning, "started"); err != nil {
		t.Fatal(err)
	}

	workerToken, err := admin.RegisterTaskGrant(ctx, caller.ID, callGrantLifetime)
	if err != nil {
		t.Fatalf("RegisterTaskGrant: %v", err)
	}
	worker := dial(t, workerToken)

	callee, err := worker.StartCall(ctx, caller.ID, "callee", map[string]any{"src_ip": "10.0.0.9"})
	if err != nil {
		t.Fatalf("task grant should authorize StartCall on its own task: %v", err)
	}
	if callee.CallParentTaskID != caller.ID || callee.CallDepth != 1 {
		t.Fatalf("callee = %+v, want the parent link and depth 1", callee)
	}
	if _, _, err := worker.CallStatus(ctx, caller.ID, callee.ID); err != nil {
		t.Fatalf("task grant should authorize CallStatus on its own callees: %v", err)
	}
	if _, _, err := worker.CallStatus(ctx, caller.ID, other.ID); !errors.Is(err, storecontract.ErrCallNotYours) {
		t.Fatalf("CallStatus for a task that is not this caller's callee = %v, want ErrCallNotYours", err)
	}

	// The grant names one caller: another task's grant cannot start calls
	// under it, and the granted caller cannot be named by someone else's
	// token -- the request-shape check refuses both before the store runs.
	if _, err := worker.StartCall(ctx, other.ID, "callee", nil); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("StartCall for another task = %v, want PermissionDenied", err)
	}
	if _, _, err := worker.CallStatus(ctx, other.ID, callee.ID); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("CallStatus for another task = %v, want PermissionDenied", err)
	}

	// The sentinels survive the wire: a stale retry surfaces the documented
	// refusal, not an opaque internal. The admin dial carries it because the
	// worker's grant names a caller that is still running; the refusal is
	// what a later grant for a finished caller's task would see.
	if _, err := admin.StartCall(ctx, callee.ID, "deeper", nil); !errors.Is(err, storecontract.ErrCallCallerNotRunning) {
		t.Fatalf("StartCall from a completed caller = %v, want ErrCallCallerNotRunning", err)
	}
}

// callGrantLifetime mirrors the daemon's defaultGrantLifetime; the value is
// not under test, only the scoping.
const callGrantLifetime = defaultGrantLifetime

// The wire requests this grant widens are the two call RPCs only; this
// compile-time check pins the full-method names the interceptor cases match
// on, so a rename on the proto side is caught here rather than at runtime.
var (
	_ = pb.StateStoreService_EnqueueCallTask_FullMethodName
	_ = pb.StateStoreService_WorkflowCallStatus_FullMethodName
)
