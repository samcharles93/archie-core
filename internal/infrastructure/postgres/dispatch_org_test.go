package postgres_test

import (
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"

	"github.com/samcharles93/archie-core/internal/domain/binding"
	"github.com/samcharles93/archie-core/internal/domain/eventtype"
	"github.com/samcharles93/archie-core/internal/domain/identity"
	"github.com/samcharles93/archie-core/internal/domain/mapping"
	"github.com/samcharles93/archie-core/internal/domain/org"
	"github.com/samcharles93/archie-core/internal/domain/source"
	"github.com/samcharles93/archie-core/internal/domain/storecontract"
	"github.com/samcharles93/archie-core/internal/infrastructure/captureintake"
	"github.com/samcharles93/archie-core/internal/infrastructure/postgres"
	"github.com/samcharles93/archie-core/internal/infrastructure/postgres/pgstore"
	"github.com/samcharles93/archie-core/internal/infrastructure/staterpc"
)

func TestPendingDispatchInCallerOrg(t *testing.T) {
	ctx := t.Context()
	db := pgstore.Open(t)
	if err := db.BootstrapIdentities(ctx, nil); err != nil {
		t.Fatal(err)
	}
	if err := db.UpgradeDefaultOrg(ctx); err != nil {
		t.Fatal(err)
	}
	for _, who := range []identity.Identity{{ID: "acme-owner", Kind: identity.KindUser, DisplayName: "Owner", Lifecycle: identity.LifecycleActive}, {ID: "acme-bot", Kind: identity.KindBot, DisplayName: "bot", Lifecycle: identity.LifecycleActive}} {
		if _, err := db.Create(ctx, who, identity.Audit{ActorID: identity.SystemID, Source: "test", RequestID: string(who.ID)}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.CreateOrg(ctx, org.Org{ID: "acme", Name: "Acme"}, "acme-owner"); err != nil {
		t.Fatal(err)
	}
	if err := db.AssignAgent(ctx, org.AgentAssignment{IdentityID: "acme-bot", OrgID: "acme"}); err != nil {
		t.Fatal(err)
	}
	eda := postgres.NewEDA(db.Pool, nil)
	client := captureRPC(t, eda, db)
	receiver := &captureintake.Receiver{Captures: client, Sources: client, Retention: time.Hour, MaxEvents: 100}
	mux := http.NewServeMux()
	receiver.Register(mux)
	ids := map[org.OrgID]string{}
	bindings := map[org.OrgID]string{}
	paths := []string{}
	for _, owner := range []org.OrgID{org.DefaultOrgID, "acme"} {
		path := "webhook-" + string(owner)
		paths = append(paths, path)
		if err := eda.InsertSource(org.WithOrg(ctx, owner), source.Source{Path: path, Signing: source.SigningUnsigned}); err != nil {
			t.Fatal(err)
		}
		// Workspace selection is not yet a write path, so only the workspace
		// needs a fixture fixup; the source org comes from the insert itself.
		if _, err := db.Pool.Exec(ctx, "UPDATE sources SET workspace_id='incoming' WHERE path=$1", path); err != nil {
			t.Fatal(err)
		}
		typeID, err := eda.InsertEventType(ctx, eventtype.EventType{Source: path, Name: string(owner), Rule: eventtype.Rule{Headers: []eventtype.HeaderCondition{{Name: "X-Org", Value: string(owner)}}}})
		if err != nil {
			t.Fatal(err)
		}
		mappingID, err := eda.InsertMapping(ctx, mapping.Mapping{Name: string(owner), EventTypeID: typeID})
		if err != nil {
			t.Fatal(err)
		}
		bindingID, err := eda.InsertBinding(ctx, binding.Binding{Name: string(owner), Matcher: binding.Matcher{Source: path}, MappingID: mappingID, Workflow: "deploy"})
		if err != nil {
			t.Fatal(err)
		}
		if err := eda.ApproveBinding(ctx, bindingID); err != nil {
			t.Fatal(err)
		}
		// Catalogue writes still default to the system org; capture ownership
		// must come from the persisted source, never a caller-supplied org.
		for _, record := range []struct{ table, id string }{{"event_types", typeID}, {"mappings", mappingID}, {"bindings", bindingID}} {
			if _, err := db.Pool.Exec(ctx, "UPDATE "+record.table+" SET org_id=$1 WHERE id=$2", owner, record.id); err != nil {
				t.Fatal(err)
			}
		}
		request := httptest.NewRequestWithContext(ctx, http.MethodPost, "/webhooks/capture/"+path, strings.NewReader("{}"))
		request.Header.Set("X-Org", string(owner))
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, request)
		if response.Code != http.StatusAccepted {
			t.Fatalf("capture: %d %s", response.Code, response.Body.String())
		}
		var captureID, captureOrg, workspace string
		if err := db.Pool.QueryRow(ctx, "SELECT id, org_id, workspace_id FROM captures WHERE source=$1", path).Scan(&captureID, &captureOrg, &workspace); err != nil {
			t.Fatal(err)
		}
		if captureOrg != string(owner) || workspace != "incoming" {
			t.Fatalf("capture org %s workspace %s, want %s/incoming", captureOrg, workspace, owner)
		}
		ids[owner] = captureID
		bindings[owner] = bindingID
	}
	for _, owner := range []org.OrgID{org.DefaultOrgID, "acme"} {
		t.Run(string(owner), func(t *testing.T) {
			captures, err := eda.ListUndispatchedCaptures(org.WithOrg(t.Context(), owner), paths, 1)
			if err != nil || len(captures) != 1 || captures[0].ID != ids[owner] {
				t.Fatalf("org %s got captures %#v err %v", owner, captures, err)
			}
			bindingID := bindings[owner]
			if err := client.RecordDispatch(t.Context(), bindingID, 1, ids[owner], 0, ""); err != nil {
				t.Fatal(err)
			}
			taskIdentity := ""
			if owner == "acme" {
				taskIdentity = "acme-bot"
			}
			run, err := client.EnqueueBindingTask(t.Context(), "", "", "Org dispatch", "", "deploy", taskIdentity, bindingID, 1, nil)
			if err != nil || run.Org != owner {
				t.Fatalf("binding task %#v err %v", run, err)
			}
			if err := client.SetDispatchTask(t.Context(), bindingID, ids[owner], run.ID); err != nil {
				t.Fatal(err)
			}
			pending, err := eda.ListUndispatchedCaptures(org.WithOrg(t.Context(), owner), paths, 1)
			if err != nil || len(pending) != 0 {
				t.Fatalf("dispatched capture still pending: %#v err %v", pending, err)
			}
		})
	}
	t.Run("unknown source", func(t *testing.T) {
		id, err := client.InsertCapture(org.WithOrg(t.Context(), "acme"), storecontract.CapturedEvent{Source: "unknown", Body: "{}"}, time.Hour, 100)
		if err != nil {
			t.Fatal(err)
		}
		var owner, workspace string
		if err := db.Pool.QueryRow(t.Context(), "SELECT org_id,workspace_id FROM captures WHERE id=$1", id).Scan(&owner, &workspace); err != nil {
			t.Fatal(err)
		}
		if owner != string(org.DefaultOrgID) || workspace != string(org.DefaultWorkspaceID) {
			t.Fatalf("unknown source owner %s/%s", owner, workspace)
		}
	})
}

func captureRPC(t *testing.T, eda *postgres.EDA, db *pgstore.TaskDB) *staterpc.Client {
	t.Helper()
	listener, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	grants := &staterpc.TaskGrants{}
	srv := grpc.NewServer(grpc.ChainUnaryInterceptor(grants.UnaryInterceptor("test-token")), grpc.ChainStreamInterceptor(grants.StreamInterceptor("test-token")))
	staterpc.RegisterServer(srv, staterpc.Deps{Tasks: db, Captures: eda, Sources: eda, BindingDispatcher: eda, BindingTaskCreator: db})
	t.Cleanup(srv.Stop)
	go func() { _ = srv.Serve(listener) }()
	client, closeClient, err := staterpc.Dial(listener.Addr().String(), "archied", "test-token")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(closeClient)
	return client
}
