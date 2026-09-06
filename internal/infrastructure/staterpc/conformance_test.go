package staterpc

import (
	"context"
	"errors"
	"net"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"

	"github.com/samcharles93/archie-core/internal/domain/binding"
	"github.com/samcharles93/archie-core/internal/domain/mapping"
	"github.com/samcharles93/archie-core/internal/events"
	"github.com/samcharles93/archie-core/internal/store"
)

// contract is the union of every store surface staterpc fronts, so the same
// test battery drives both the local *store.Store and the remote *Client
// through one interface value, per docs/prds/state-store-contract.md §11's
// conformance requirement.
type contract interface {
	store.TaskStore
	store.CaptureStore
	store.MappingStore
	store.BindingStore
	store.BindingDispatcher
	store.BindingTaskCreator
}

func remoteContract(t *testing.T, local *store.Store) contract {
	t.Helper()
	listener := bufconn.Listen(1 << 20)
	server := grpc.NewServer()
	RegisterServer(server, Deps{
		Tasks: local, Captures: local, Mappings: local, Bindings: local,
		BindingDispatcher: local, BindingTaskCreator: local,
	})
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { server.Stop(); _ = listener.Close() })
	conn, err := grpc.NewClient("passthrough:///state",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return listener.DialContext(ctx) }))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return NewClient(conn)
}

// TestStateStoreConformance runs the same battery of contract calls through
// both the local adapter and a bufconn-backed gRPC client, asserting
// identical behaviour including error-sentinel fidelity and the
// not-found-as-(nil,nil) convention (§7, §11).
func TestStateStoreConformance(t *testing.T) {
	for _, mode := range []string{"local", "grpc"} {
		t.Run(mode, func(t *testing.T) {
			ctx := t.Context()
			local := store.OpenTest(t)
			var c contract = local
			if mode == "grpc" {
				c = remoteContract(t, local)
			}

			// workflow.Store view: EnqueueChatTask, Transition, Update, InsertEvent.
			task, err := c.EnqueueChatTask(ctx, "acme", "widget", "title", "body", "implement", "")
			if err != nil || task == nil {
				t.Fatalf("EnqueueChatTask: %+v %v", task, err)
			}
			if err := c.Transition(ctx, task.ID, task.Status, "running", "started"); err != nil {
				t.Fatalf("Transition: %v", err)
			}
			task.Plan = "the plan"
			task.Status = "running"
			if err := c.Update(ctx, task); err != nil {
				t.Fatalf("Update: %v", err)
			}
			eventID, err := c.InsertEvent(ctx, events.Event{Kind: "stage_finish", TaskID: task.ID, Data: map[string]any{"duration_ms": 12.0}})
			if err != nil || eventID == 0 {
				t.Fatalf("InsertEvent: %v %v", eventID, err)
			}

			// Stale transition: from no longer matches current status.
			err = c.Transition(ctx, task.ID, "queued", "merged", "")
			if !errors.Is(err, store.ErrStaleTransition) {
				t.Fatalf("Transition stale = %v, want ErrStaleTransition", err)
			}

			// TaskQueries: found=false is not an error.
			missing, err := c.TaskByID(ctx, task.ID+999999)
			if err != nil || missing != nil {
				t.Fatalf("TaskByID missing: %+v %v", missing, err)
			}
			got, err := c.TaskByID(ctx, task.ID)
			if err != nil || got == nil || got.Plan != "the plan" {
				t.Fatalf("TaskByID: %+v %v", got, err)
			}
			byIssue, err := c.TaskByIssue(ctx, task.Owner, task.Repo, task.IssueNumber)
			if err != nil || byIssue == nil || byIssue.ID != task.ID {
				t.Fatalf("TaskByIssue: %+v %v", byIssue, err)
			}
			if _, err := c.Tasks(ctx, 10); err != nil {
				t.Fatalf("Tasks: %v", err)
			}
			if _, err := c.StatusCounts(ctx); err != nil {
				t.Fatalf("StatusCounts: %v", err)
			}
			if _, err := c.OpenPRs(ctx); err != nil {
				t.Fatalf("OpenPRs: %v", err)
			}
			if err := c.IncrementRetryCount(ctx, task.ID); err != nil {
				t.Fatalf("IncrementRetryCount: %v", err)
			}

			// TaskEvents / EventsSince / stats.
			if evs, err := c.TaskEvents(ctx, task.ID); err != nil || len(evs) != 1 || evs[0].Data["duration_ms"] != 12.0 {
				t.Fatalf("TaskEvents: %+v %v", evs, err)
			}
			if _, err := c.EventsSince(ctx, 0, 10); err != nil {
				t.Fatalf("EventsSince: %v", err)
			}
			if _, err := c.WorkflowStats(ctx); err != nil {
				t.Fatalf("WorkflowStats: %v", err)
			}
			if _, err := c.StageStats(ctx); err != nil {
				t.Fatalf("StageStats: %v", err)
			}
			if _, err := c.TokensByDay(ctx, 7); err != nil {
				t.Fatalf("TokensByDay: %v", err)
			}

			// Lifecycle: ClaimNext / ClaimByIssue / Requeue / RetryTask / RecoverStale.
			if _, err := c.EnqueueIssue(ctx, "acme", "widget", 42, "issue title", "body", "bug", ""); err != nil {
				t.Fatalf("EnqueueIssue: %v", err)
			}
			claimed, err := c.ClaimNext(ctx)
			if err != nil || claimed == nil {
				t.Fatalf("ClaimNext: %+v %v", claimed, err)
			}
			if err := c.Requeue(ctx, claimed.ID, "running", ""); err != nil {
				t.Fatalf("Requeue: %v", err)
			}
			if err := c.RetryTask(ctx, claimed.ID, "queued", ""); err != nil {
				t.Fatalf("RetryTask: %v", err)
			}
			if _, err := c.RecoverStale(ctx); err != nil {
				t.Fatalf("RecoverStale: %v", err)
			}
			byClaim, err := c.ClaimByIssue(ctx, "acme", "widget", 999999)
			if err != nil || byClaim != nil {
				t.Fatalf("ClaimByIssue missing: %+v %v", byClaim, err)
			}

			// Archive. RecoverStale above may have requeued task if it was
			// still running, so re-fetch its current status rather than
			// trusting the in-memory snapshot.
			current, err := c.TaskByID(ctx, task.ID)
			if err != nil || current == nil {
				t.Fatalf("TaskByID before archive: %+v %v", current, err)
			}
			if _, err := c.ArchiveTask(ctx, task.ID, current.Status, events.Event{Kind: "archived"}); err != nil {
				t.Fatalf("ArchiveTask: %v", err)
			}
			if _, err := c.ClearTerminalTasks(ctx); err != nil {
				t.Fatalf("ClearTerminalTasks: %v", err)
			}

			// Mapping: found=false, ErrMappingNotFound.
			mappingID, err := c.InsertMapping(ctx, mapping.Mapping{Name: "m1", Fields: []mapping.Field{{Name: "a", Path: "a", Type: mapping.TypeString}}})
			if err != nil || mappingID == 0 {
				t.Fatalf("InsertMapping: %v %v", mappingID, err)
			}
			missingMapping, err := c.GetMapping(ctx, mappingID+999999)
			if err != nil || missingMapping != nil {
				t.Fatalf("GetMapping missing: %+v %v", missingMapping, err)
			}
			gotMapping, err := c.GetMapping(ctx, mappingID)
			if err != nil || gotMapping == nil || gotMapping.Name != "m1" {
				t.Fatalf("GetMapping: %+v %v", gotMapping, err)
			}
			if _, err := c.ListMappings(ctx); err != nil {
				t.Fatalf("ListMappings: %v", err)
			}
			gotMapping.Name = "m1-renamed"
			if err := c.UpdateMapping(ctx, *gotMapping); err != nil {
				t.Fatalf("UpdateMapping: %v", err)
			}
			err = c.UpdateMapping(ctx, mapping.Mapping{ID: mappingID + 999999, Name: "x", Fields: gotMapping.Fields})
			if !errors.Is(err, store.ErrMappingNotFound) {
				t.Fatalf("UpdateMapping missing = %v, want ErrMappingNotFound", err)
			}
			err = c.DeleteMapping(ctx, mappingID+999999)
			if !errors.Is(err, store.ErrMappingNotFound) {
				t.Fatalf("DeleteMapping missing = %v, want ErrMappingNotFound", err)
			}

			// Binding: found=false, ErrBindingNotFound, ErrBindingOverlap,
			// ErrBindingTransition, and the dispatch surface incl.
			// ErrAlreadyDispatched.
			bindingID, err := c.InsertBinding(ctx, binding.Binding{
				Name: "b1", Matcher: binding.Matcher{Source: "sentry"}, MappingID: mappingID,
				Workflow: "implement", Secret: "0123456789abcdef0123456789abcdef",
			})
			if err != nil || bindingID == 0 {
				t.Fatalf("InsertBinding: %v %v", bindingID, err)
			}
			_, err = c.InsertBinding(ctx, binding.Binding{
				Name: "b2", Matcher: binding.Matcher{Source: "sentry"}, MappingID: mappingID,
				Workflow: "implement", Secret: "0123456789abcdef0123456789abcdef",
			})
			if !errors.Is(err, store.ErrBindingOverlap) {
				t.Fatalf("InsertBinding overlap = %v, want ErrBindingOverlap", err)
			}
			missingBinding, err := c.GetBinding(ctx, bindingID+999999)
			if err != nil || missingBinding != nil {
				t.Fatalf("GetBinding missing: %+v %v", missingBinding, err)
			}
			gotBinding, err := c.GetBinding(ctx, bindingID)
			if err != nil || gotBinding == nil || gotBinding.Name != "b1" {
				t.Fatalf("GetBinding: %+v %v", gotBinding, err)
			}
			if _, err := c.ListBindings(ctx); err != nil {
				t.Fatalf("ListBindings: %v", err)
			}
			err = c.ApproveBinding(ctx, bindingID)
			if !errors.Is(err, store.ErrBindingTransition) {
				t.Fatalf("ApproveBinding from draft = %v, want ErrBindingTransition", err)
			}
			err = c.DeleteBinding(ctx, bindingID+999999)
			if !errors.Is(err, store.ErrBindingNotFound) {
				t.Fatalf("DeleteBinding missing = %v, want ErrBindingNotFound", err)
			}

			// Capture + dispatch.
			captureID, err := c.InsertCapture(ctx, store.CapturedEvent{Source: "sentry", Body: `{"id":1}`, Authenticated: true}, 0, 0)
			if err != nil || captureID == 0 {
				t.Fatalf("InsertCapture: %v %v", captureID, err)
			}
			if _, err := c.ListCaptures(ctx, 10); err != nil {
				t.Fatalf("ListCaptures: %v", err)
			}
			if _, err := c.ArmedBindingsForSource(ctx, "sentry"); err != nil {
				t.Fatalf("ArmedBindingsForSource: %v", err)
			}
			bindingTask, err := c.EnqueueBindingTask(ctx, "acme", "widget", "t", "b", "implement", "", bindingID, 1)
			if err != nil || bindingTask == nil {
				t.Fatalf("EnqueueBindingTask: %+v %v", bindingTask, err)
			}
			if err := c.RecordDispatch(ctx, bindingID, 1, captureID, bindingTask.ID); err != nil {
				t.Fatalf("RecordDispatch: %v", err)
			}
			err = c.RecordDispatch(ctx, bindingID, 1, captureID, bindingTask.ID)
			if !errors.Is(err, store.ErrAlreadyDispatched) {
				t.Fatalf("RecordDispatch dup = %v, want ErrAlreadyDispatched", err)
			}
			if _, err := c.ListUndispatchedCaptures(ctx, []string{"sentry"}, 10); err != nil {
				t.Fatalf("ListUndispatchedCaptures: %v", err)
			}
		})
	}
}

// TestTokenInterceptorRejectsMissingOrInvalidToken exercises the gRPC
// interceptor the bridge-address (agent-consumed) listener topology
// requires: missing or unrecognised tokens fail closed with
// codes.Unauthenticated, per §9's token lifecycle.
func TestTokenInterceptorRejectsMissingOrInvalidToken(t *testing.T) {
	const validToken = "task-42-token"
	validate := func(token string) bool { return token == validToken }

	listener := bufconn.Listen(1 << 20)
	local := store.OpenTest(t)
	server := grpc.NewServer(grpc.UnaryInterceptor(UnaryTokenInterceptor(validate)))
	RegisterServer(server, Deps{Tasks: local})
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { server.Stop(); _ = listener.Close() })

	dial := func(t *testing.T, token string) contract {
		t.Helper()
		opts := []grpc.DialOption{
			grpc.WithTransportCredentials(insecure.NewCredentials()),
			grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return listener.DialContext(ctx) }),
		}
		if token != "" {
			opts = append(opts, grpc.WithUnaryInterceptor(UnaryClientTokenInterceptor(token)))
		}
		conn, err := grpc.NewClient("passthrough:///state", opts...)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = conn.Close() })
		return NewClient(conn)
	}

	ctx := t.Context()
	if _, err := dial(t, "").StatusCounts(ctx); err == nil {
		t.Fatal("missing token accepted")
	}
	if _, err := dial(t, "wrong-token").StatusCounts(ctx); err == nil {
		t.Fatal("invalid token accepted")
	}
	if _, err := dial(t, validToken).StatusCounts(ctx); err != nil {
		t.Fatalf("valid token rejected: %v", err)
	}
}
