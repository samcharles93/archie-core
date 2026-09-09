package staterpc

import (
	"context"
	"net"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"

	"github.com/samcharles93/archie-core/internal/domain/workflow"
	"github.com/samcharles93/archie-core/internal/events"
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
	RegisterServer(server, Deps{Tasks: local, Grants: grants, ConfigSnapshots: local})
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { server.Stop(); _ = listener.Close() })

	dial = func(t *testing.T, token string) *Client {
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
	return grants, dial
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
