package staterpc

import (
	"context"
	"net"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	"github.com/samcharles93/archie-core/internal/domain/binding"
	"github.com/samcharles93/archie-core/internal/domain/mapping"
	"github.com/samcharles93/archie-core/internal/domain/workflow"
	"github.com/samcharles93/archie-core/internal/events"
	"github.com/samcharles93/archie-core/internal/infrastructure/edastore"
	"github.com/samcharles93/archie-core/internal/store"
)

// grantsServer wires TaskGrants' Unary/Stream interceptors -- the same
// composition state_store.go's stateStoreServerOpts uses for a non-loopback
// listener -- so tests can dial with an admin token, a task grant, or
// nothing.
func grantsServer(t *testing.T, adminToken string) (grants *TaskGrants, dial func(t *testing.T, token string) *Client) {
	t.Helper()
	grants = &TaskGrants{}
	listener := bufconn.Listen(1 << 20)
	local := store.OpenTest(t)
	server := grpc.NewServer(
		grpc.ChainUnaryInterceptor(grants.UnaryInterceptor(adminToken)),
		grpc.ChainStreamInterceptor(grants.StreamInterceptor(adminToken)),
	)
	// One event-capture store serves every EDA surface, as the real
	// composition does: separate instances would not share a database.
	eda := edastore.OpenTest(t)
	RegisterServer(server, Deps{
		Tasks: local, ConfigSnapshots: local, ApplyStatus: local,
		Captures: eda, Mappings: eda, Bindings: eda,
		BindingDispatcher: eda, PlaybookDispatcher: eda,
		Grants: grants,
	})
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { server.Stop(); _ = listener.Close() })

	dial = func(t *testing.T, token string) *Client {
		t.Helper()
		// Dial is the production client-side wiring (and the only dial path
		// the daemon and the dashboard use), so a credential attached to one
		// call shape but not another cannot hide here. The target is the
		// non-loopback bridge address every container-mode deployment
		// profile uses; the dialer redirects it to the bufconn listener.
		client, cleanup, err := Dial("172.17.0.1:9090", token, grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return listener.DialContext(ctx) }))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(cleanup)
		return client
	}
	return grants, dial
}

// TestCaptureStreamsCarryTheAdminToken is the capture-batching regression:
// the batch reads moved onto server-streaming RPCs (StreamCaptures /
// StreamUndispatchedCaptures) to get past gRPC's 4 MiB unary cap, but the
// client interceptor suite attached the bearer token to unary calls only --
// so on the token-protected topology every deployment profile uses, the
// daemon's dispatch loop and the dashboard's inspector got PermissionDenied
// from the stream interceptor, with no unary fallback left to take. 20 x
// 256 KiB is 5 MiB, past that cap, so the batch also proves the streaming
// path still carries it.
func TestCaptureStreamsCarryTheAdminToken(t *testing.T) {
	const adminToken = "daemon-admin-token"
	_, dial := grantsServer(t, adminToken)
	admin := dial(t, adminToken)
	ctx := t.Context()

	body := strings.Repeat("x", 256<<10)
	for range 20 {
		if _, err := admin.InsertCapture(ctx, store.CapturedEvent{Source: "large", Body: body, Authenticated: true}, 0, 0); err != nil {
			t.Fatalf("InsertCapture: %v", err)
		}
	}
	// ListUndispatchedCaptures only returns sources with an armed binding
	// A binding's mapping is a real relation now, so it must point at a
	// mapping that exists; the store refuses a dangling id.
	mappingID, mapErr := admin.InsertMapping(t.Context(), mapping.Mapping{
		Name:   "m",
		Fields: []mapping.Field{{Name: "title", Path: "title", Type: mapping.TypeString}},
	})
	if mapErr != nil {
		t.Fatal(mapErr)
	}
	// (edastore.ArmedBindingsForSource), so "large" needs one taken through the
	// public draft -> pending_approval -> armed lifecycle.
	id, err := admin.InsertBinding(ctx, binding.Binding{
		Name: "large binding", Matcher: binding.Matcher{Source: "large"},
		MappingID: mappingID, Workflow: "implement", Secret: "0123456789abcdef0123456789abcdef",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := admin.UpdateBinding(ctx, binding.Binding{
		ID: id, Name: "large binding", Matcher: binding.Matcher{Source: "large"},
		MappingID: mappingID, Workflow: "implement", Secret: "0123456789abcdef0123456789abcdef",
	}); err != nil {
		t.Fatal(err)
	}
	if err := admin.ApproveBinding(ctx, id); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name string
		list func(context.Context) ([]store.CapturedEvent, error)
	}{
		{"ListCaptures", func(ctx context.Context) ([]store.CapturedEvent, error) { return admin.ListCaptures(ctx, 20) }},
		{"ListUndispatchedCaptures", func(ctx context.Context) ([]store.CapturedEvent, error) {
			return admin.ListUndispatchedCaptures(ctx, []string{"large"}, 20)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			captures, err := tc.list(t.Context())
			if err != nil {
				t.Fatalf("%s over a token-protected listener: %v", tc.name, err)
			}
			if len(captures) != 20 {
				t.Fatalf("got %d captures, want 20", len(captures))
			}
			for _, capture := range captures {
				if capture.Body != body {
					t.Fatal("capture body truncated")
				}
			}
		})
	}
}

// TestUpdateRejectsNilTask is the Update-validation regression: a request
// carrying no Task must be refused at the RPC boundary as InvalidArgument,
// not dereferenced inside the store layer.
func TestUpdateRejectsNilTask(t *testing.T) {
	const adminToken = "daemon-admin-token"
	_, dial := grantsServer(t, adminToken)
	err := dial(t, adminToken).Update(t.Context(), nil)
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("Update(nil) = %v, want InvalidArgument", err)
	}
}

// TestTaskGrantScopesWorkerToItsOwnThreeRPCs is the finding #1 regression:
// a container's task-scoped credential must authorize only Update/
// Transition/InsertEvent on its own task ID, never the admin-only surface
// (StatusCounts here stands in for any of the other ~37 RPCs) and never
// another task's ID.
func TestTaskGrantScopesWorkerToItsOwnThreeRPCs(t *testing.T) {
	const adminToken = "daemon-admin-token"
	grants, dial := grantsServer(t, adminToken)
	admin := dial(t, adminToken)
	ctx := t.Context()

	taskA, err := admin.EnqueueChatTask(ctx, "acme", "widget", "a", "body", "implement", "")
	if err != nil {
		t.Fatal(err)
	}
	taskB, err := admin.EnqueueChatTask(ctx, "acme", "widget", "b", "body", "implement", "")
	if err != nil {
		t.Fatal(err)
	}

	// The daemon (never a worker) mints task grants with its own
	// administrative credential, mirroring staterpc.GrantIssuer.Issue.
	workerToken, err := admin.RegisterTaskGrant(ctx, taskA.ID, time.Hour)
	if err != nil {
		t.Fatalf("RegisterTaskGrant: %v", err)
	}
	_ = grants // grants map is exercised indirectly through the interceptor

	workerA := dial(t, workerToken)

	if err := workerA.Transition(ctx, taskA.ID, taskA.Status, "running", "started"); err != nil {
		t.Fatalf("task grant should authorize Transition on its own task: %v", err)
	}
	if _, err := workerA.InsertEvent(ctx, events.Event{TaskID: taskA.ID, Kind: "log"}); err != nil {
		t.Fatalf("task grant should authorize InsertEvent on its own task: %v", err)
	}
	// The documented residual of R4, pinned rather than hidden: a task grant
	// may insert events on its OWN task -- that is how tool_call and agent_finish
	// already work -- so a task can write its own config_captured provenance,
	// and only its own. The permission is the existing InsertEvent permission,
	// not a new surface, which is why the provenance is exactly as trustworthy
	// as the rest of that task's event stream.
	configEvent := events.Event{
		TaskID: taskA.ID, Kind: events.KindConfigCaptured, Attempt: 1,
		Data: map[string]any{"schema": events.ConfigCapturedSchema, "document": map[string]any{"bot_user": "archie"}},
	}
	if _, err := workerA.InsertEvent(ctx, configEvent); err != nil {
		t.Fatalf("task grant should authorize its own config_captured event: %v", err)
	}
	configEvent.TaskID = taskB.ID
	if _, err := workerA.InsertEvent(ctx, configEvent); err == nil {
		t.Fatal("task grant must not authorize a config_captured event on another task")
	}
	// The document rides under the producer's own key (internal/daemon/daemon.go
	// writes "document"), so the fixture above is read back and asserted by key
	// rather than trusted: a rename would otherwise leave this test green.
	stored, err := admin.TaskEvents(ctx, taskA.ID)
	if err != nil {
		t.Fatalf("TaskEvents: %v", err)
	}
	for _, e := range stored {
		if e.Kind != events.KindConfigCaptured {
			continue
		}
		if _, renamed := e.Data["config"]; renamed {
			t.Error(`the document crossed under "config"; the producer writes it under "document"`)
		}
		if _, ok := e.Data["document"].(map[string]any); !ok {
			t.Errorf("config_captured data = %#v, want the document under the producer's key", e.Data)
		}
	}
	if err := workerA.Update(ctx, &workflow.Task{ID: taskA.ID, Owner: "acme", Repo: "widget"}); err != nil {
		t.Fatalf("task grant should authorize Update on its own task: %v", err)
	}

	if err := workerA.Transition(ctx, taskB.ID, taskB.Status, "running", "started"); err == nil {
		t.Fatal("task grant must not authorize another task's Transition")
	}
	if _, err := workerA.StatusCounts(ctx); err == nil {
		t.Fatal("task grant must not authorize an admin-only RPC")
	}
	if _, err := workerA.ListCaptures(ctx, 10); err == nil {
		t.Fatal("task grant must not authorize the streaming capture surface")
	}

	if err := admin.RevokeTaskGrant(ctx, workerToken); err != nil {
		t.Fatalf("RevokeTaskGrant: %v", err)
	}
	if err := workerA.Transition(ctx, taskA.ID, "running", "waiting", "revoked check"); err == nil {
		t.Fatal("a revoked task grant must be rejected")
	}
}

// TestOnlyAdminPublishesTheConfigSnapshot: the published projection is the
// dashboard's whole view of the running configuration, so a container's
// task-scoped credential must not be able to rewrite what an operator reads,
// nor to read a projection describing the deployment it runs inside.
func TestOnlyAdminPublishesTheConfigSnapshot(t *testing.T) {
	const adminToken = "daemon-admin-token"
	_, dial := grantsServer(t, adminToken)
	admin := dial(t, adminToken)
	ctx := t.Context()

	task, err := admin.EnqueueChatTask(ctx, "acme", "widget", "a", "body", "implement", "")
	if err != nil {
		t.Fatal(err)
	}
	workerToken, err := admin.RegisterTaskGrant(ctx, task.ID, time.Hour)
	if err != nil {
		t.Fatalf("RegisterTaskGrant: %v", err)
	}
	worker := dial(t, workerToken)

	snapshot := store.ConfigSnapshot{Schema: "webui.ConfigView/1", Document: []byte(`{}`)}
	if err := admin.PutConfigSnapshot(ctx, snapshot); err != nil {
		t.Fatalf("admin PutConfigSnapshot: %v", err)
	}
	if err := worker.PutConfigSnapshot(ctx, snapshot); err == nil {
		t.Fatal("a task grant must not authorize publishing the configuration snapshot")
	}
	if _, _, err := worker.ConfigSnapshot(ctx); err == nil {
		t.Fatal("a task grant must not authorize reading the configuration snapshot")
	}
	if _, found, err := admin.ConfigSnapshot(ctx); err != nil || !found {
		t.Fatalf("admin ConfigSnapshot = (found %v, %v), want the published snapshot", found, err)
	}
}

// TestOnlyAdminReportsApplyStatus: apply status says which process is running
// which settings version. A container's task-scoped credential must not be
// able to claim a process applied something, nor to read the deployment's
// process inventory.
func TestOnlyAdminReportsApplyStatus(t *testing.T) {
	const adminToken = "daemon-admin-token"
	_, dial := grantsServer(t, adminToken)
	admin := dial(t, adminToken)
	ctx := t.Context()

	task, err := admin.EnqueueChatTask(ctx, "acme", "widget", "a", "body", "implement", "")
	if err != nil {
		t.Fatal(err)
	}
	workerToken, err := admin.RegisterTaskGrant(ctx, task.ID, time.Hour)
	if err != nil {
		t.Fatalf("RegisterTaskGrant: %v", err)
	}
	worker := dial(t, workerToken)

	status := store.ApplyStatus{Process: "archied", Kind: "tool-settings", AppliedVersion: 2}
	if err := admin.PutApplyStatus(ctx, status); err != nil {
		t.Fatalf("admin PutApplyStatus: %v", err)
	}
	if err := worker.PutApplyStatus(ctx, status); err == nil {
		t.Fatal("a task grant must not authorize reporting apply status")
	}
	if _, err := worker.ListApplyStatus(ctx); err == nil {
		t.Fatal("a task grant must not authorize reading apply status")
	}
	got, err := admin.ListApplyStatus(ctx)
	if err != nil || len(got) != 1 {
		t.Fatalf("admin ListApplyStatus = (%d records, %v), want the reported one", len(got), err)
	}
}

// TestOnlyAdminOwnsThePlaybookLedger: the playbook dispatch ledger records
// which side-effecting playbook action already fired, so a container's
// task-scoped credential must not be able to write a "not dispatched" row
// that would re-run a non-revocable side effect, nor to delete the ledger
// and erase the skip.
func TestOnlyAdminOwnsThePlaybookLedger(t *testing.T) {
	const adminToken = "daemon-admin-token"
	_, dial := grantsServer(t, adminToken)
	admin := dial(t, adminToken)
	ctx := t.Context()

	task, err := admin.EnqueueChatTask(ctx, "acme", "widget", "a", "body", "implement", "")
	if err != nil {
		t.Fatal(err)
	}
	workerToken, err := admin.RegisterTaskGrant(ctx, task.ID, time.Hour)
	if err != nil {
		t.Fatalf("RegisterTaskGrant: %v", err)
	}
	worker := dial(t, workerToken)

	if err := admin.RecordPlaybookDispatch(ctx, "pb.yaml", "v1", "archie:acme/widget/7", "notify"); err != nil {
		t.Fatalf("admin RecordPlaybookDispatch: %v", err)
	}
	// A distinct event_id is deliberate: the ledger's own duplicate answer is
	// ErrAlreadyDispatched, so re-recording the admin's tuple would be non-nil
	// even for an authorized caller. A distinct tuple succeeds only if the
	// caller is authorized, which is exactly what must be refused here.
	if err := worker.RecordPlaybookDispatch(ctx, "pb.yaml", "v1", "archie:acme/widget/8", "notify"); err == nil {
		t.Fatal("a task grant must not authorize recording the playbook dispatch ledger")
	}
	if err := worker.DeletePlaybookDispatches(ctx, "pb.yaml"); err == nil {
		t.Fatal("a task grant must not authorize deleting the playbook dispatch ledger")
	}
	if err := admin.DeletePlaybookDispatches(ctx, "pb.yaml"); err != nil {
		t.Fatalf("admin DeletePlaybookDispatches: %v", err)
	}
}

func TestOnlyAdminCanRegisterOrRevokeTaskGrants(t *testing.T) {
	const adminToken = "daemon-admin-token"
	_, dial := grantsServer(t, adminToken)
	ctx := t.Context()

	admin := dial(t, adminToken)
	taskA, err := admin.EnqueueChatTask(ctx, "acme", "widget", "a", "body", "implement", "")
	if err != nil {
		t.Fatal(err)
	}
	workerToken, err := admin.RegisterTaskGrant(ctx, taskA.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}

	worker := dial(t, workerToken)
	if _, err := worker.RegisterTaskGrant(ctx, taskA.ID, time.Hour); err == nil {
		t.Fatal("a task-scoped grant must not be able to mint further grants")
	}
	if err := worker.RevokeTaskGrant(ctx, workerToken); err == nil {
		t.Fatal("a task-scoped grant must not be able to revoke grants")
	}
}
