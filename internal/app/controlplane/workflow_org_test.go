package controlplane_test

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"

	"github.com/samcharles93/archie-core/internal/app/controlplane"
	"github.com/samcharles93/archie-core/internal/config"
	pb "github.com/samcharles93/archie-core/internal/contracts/controlplane/v1"
	"github.com/samcharles93/archie-core/internal/domain/access"
	"github.com/samcharles93/archie-core/internal/domain/identity"
	"github.com/samcharles93/archie-core/internal/domain/org"
	"github.com/samcharles93/archie-core/internal/domain/storecontract"
	"github.com/samcharles93/archie-core/internal/domain/workflow"
	infraaccess "github.com/samcharles93/archie-core/internal/infrastructure/access"
	"github.com/samcharles93/archie-core/internal/infrastructure/postgres/pgstore"
	"github.com/samcharles93/archie-core/internal/infrastructure/postgres/pgtest"
	"github.com/samcharles93/archie-core/internal/infrastructure/rpcidentity"
	"github.com/samcharles93/archie-core/internal/infrastructure/staterpc"
	"github.com/samcharles93/archie-core/internal/infrastructure/workflowsteps"
	"github.com/samcharles93/archie-core/internal/webui"
)

func TestMain(m *testing.M) { os.Exit(pgtest.Main(m)) }

func TestOrgWorkflowResources(t *testing.T) {
	for _, kind := range []string{controlplane.WorkflowDefinitionsKind, controlplane.WorkflowEnablementKind} {
		t.Run(kind, func(t *testing.T) {
			ctx := t.Context()
			db := pgstore.Open(t)
			if err := db.BootstrapIdentities(ctx, nil); err != nil {
				t.Fatal(err)
			}
			if err := db.UpgradeDefaultOrg(ctx); err != nil {
				t.Fatal(err)
			}
			steps, err := workflowsteps.NewManager()
			if err != nil {
				t.Fatal(err)
			}
			server, err := controlplane.NewServer(db, steps)
			if err != nil {
				t.Fatal(err)
			}
			if _, _, err := server.ImportConfig(ctx, config.Config{}); err != nil {
				t.Fatal(err)
			}
			owner, err := db.Create(ctx, identity.Identity{ID: "acme-owner", Kind: identity.KindUser, DisplayName: "Owner", Lifecycle: identity.LifecycleActive}, identity.Audit{ActorID: identity.SystemID, Source: "test", RequestID: "owner", At: time.Now()})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := db.CreateOrg(ctx, org.Org{ID: "acme", Name: "Acme"}, owner.ID); err != nil {
				t.Fatal(err)
			}
			client := workflowRPC(t, server, db)
			principal, err := db.PrincipalFor(ctx, owner.ID)
			if err != nil {
				t.Fatal(err)
			}
			other := access.WithPrincipal(ctx, principal)
			initial, err := client.ControlPlane().Query(other, &pb.QueryRequest{Kind: kind})
			if err != nil {
				t.Fatalf("new org cannot read its shipped %s: %v", kind, err)
			}
			if initial.Resource.OrgId != "acme" {
				t.Fatalf("read org %q", initial.Resource.OrgId)
			}
			value := []byte(`{"orgs":{"acme":{"disabled":["tdd"]}}}`)
			if kind == controlplane.WorkflowDefinitionsKind {
				collection := workflow.ShippedDefinitions()
				collection.Definitions = append(collection.Definitions, workflow.WorkflowDefinitionEntry{ID: "acme-only", YAML: "id: acme-only\nrepository: none\nsteps:\n  - type: workflow.finish\n"})
				value, err = json.Marshal(collection)
				if err != nil {
					t.Fatal(err)
				}
			}
			changed, err := client.ControlPlane().Command(other, &pb.CommandRequest{Kind: kind, Command: "replace", ValueJson: value, ExpectedVersion: initial.Resource.Version, Actor: "owner", Source: "test", RequestId: "edit"})
			if err != nil {
				t.Fatal(err)
			}
			if _, _, err := server.ImportConfig(ctx, config.Config{}); err != nil {
				t.Fatal(err)
			}
			for _, tc := range []struct {
				ctxOrg org.OrgID
				want   bool
			}{{"acme", true}, {org.DefaultOrgID, false}} {
				queryCtx := ctx
				if tc.ctxOrg == "acme" {
					queryCtx = other
				}
				got, err := client.ControlPlane().Query(queryCtx, &pb.QueryRequest{Kind: kind})
				if err != nil {
					t.Fatal(err)
				}
				if strings.Contains(string(got.Resource.ValueJson), "acme") != tc.want {
					t.Fatalf("org %q got %s", tc.ctxOrg, got.Resource.ValueJson)
				}
				if tc.want && got.Resource.Version != changed.Resource.Version {
					t.Fatalf("boot replaced operator's document")
				}
			}
			if kind == controlplane.WorkflowDefinitionsKind {
				checkWorkflowDashboard(t, db, client)
			}
		})
	}
}

func workflowRPC(t *testing.T, control *controlplane.Server, db *pgstore.TaskDB) *staterpc.Client {
	t.Helper()
	listener, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	grants := &staterpc.TaskGrants{}
	callers := rpcidentity.Callers{Principals: db}
	srv := grpc.NewServer(grpc.ChainUnaryInterceptor(grants.UnaryInterceptor("test-token"), callers.Unary()), grpc.ChainStreamInterceptor(grants.StreamInterceptor("test-token"), callers.Stream()))
	staterpc.RegisterServer(srv, staterpc.Deps{ControlPlane: control, Tasks: db, Principals: db, Identities: db})
	t.Cleanup(srv.Stop)
	go func() { _ = srv.Serve(listener) }()
	client, closeClient, err := staterpc.Dial(listener.Addr().String(), "archie-ui", "test-token")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(closeClient)
	return client
}

func TestValidateStoredOrgWorkflows(t *testing.T) {
	ctx := t.Context()
	db := pgstore.Open(t)
	if err := db.BootstrapIdentities(ctx, nil); err != nil {
		t.Fatal(err)
	}
	if err := db.UpgradeDefaultOrg(ctx); err != nil {
		t.Fatal(err)
	}
	steps, err := workflowsteps.NewManager()
	if err != nil {
		t.Fatal(err)
	}
	server, err := controlplane.NewServer(db, steps)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.CreateOrg(ctx, org.Org{ID: "acme", Name: "Acme"}, identity.SystemID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.PutResource(ctx, storecontract.ResourceWrite{OrgID: "acme", Kind: controlplane.WorkflowDefinitionsKind, Value: []byte(`{"definitions":[{"id":"broken","yaml":"id: broken\nsteps:\n  - type: nonexistent\n"}]}`), Actor: "owner", Source: "test", RequestID: "invalid"}); err != nil {
		t.Fatal(err)
	}
	if _, err := server.ValidateStored(ctx); err == nil || !strings.Contains(err.Error(), "acme") {
		t.Fatalf("invalid org workflow was not reported: %v", err)
	}
}

func checkWorkflowDashboard(t *testing.T, db *pgstore.TaskDB, client *staterpc.Client) {
	t.Helper()
	ctx := t.Context()
	policies := append(access.ShippedOrgPolicies("acme"), access.ShippedOrgPolicies(org.DefaultOrgID)...)
	chain, err := infraaccess.New(policies)
	if err != nil {
		t.Fatal(err)
	}
	srv := &webui.Server{Store: client, ControlPlane: client.ControlPlane(), Access: chain, Principals: client, Authenticate: func(ctx context.Context, token string) (identity.Identity, error) {
		return db.Get(ctx, identity.IdentityID(token))
	}}
	handler := srv.Handler()
	for _, tc := range []struct {
		method, path, body string
		status             int
	}{
		{"GET", "/api/workflows", "", http.StatusOK},
		{"POST", "/api/work-requests", `{"identity":"acme-owner","workflow":"acme-only","title":"Org work","instructions":"Finish"}`, http.StatusCreated},
	} {
		r := httptest.NewRequestWithContext(ctx, tc.method, tc.path, strings.NewReader(tc.body))
		r.Header.Set("Authorization", "Bearer acme-owner")
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-Archie-CSRF", "1")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != tc.status {
			t.Fatalf("%s %s: %d %s", tc.method, tc.path, w.Code, w.Body.String())
		}
		if tc.method == "GET" && !strings.Contains(w.Body.String(), "acme-only") {
			t.Fatalf("dashboard omitted the org's workflow: %s", w.Body.String())
		}
		if tc.method == "POST" {
			var reply struct {
				TaskID int64 `json:"task_id"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &reply); err != nil {
				t.Fatal(err)
			}
			run, err := db.TaskByID(ctx, reply.TaskID)
			if err != nil || run.Org != "acme" || run.Workflow != "acme-only" {
				t.Fatalf("admitted task %#v err %v", run, err)
			}
		}
	}
}
