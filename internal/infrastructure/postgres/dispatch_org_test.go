package postgres_test

import (
	"testing"
	"time"

	"github.com/samcharles93/archie-core/internal/domain/binding"
	"github.com/samcharles93/archie-core/internal/domain/eventtype"
	"github.com/samcharles93/archie-core/internal/domain/identity"
	"github.com/samcharles93/archie-core/internal/domain/mapping"
	"github.com/samcharles93/archie-core/internal/domain/org"
	"github.com/samcharles93/archie-core/internal/domain/storecontract"
	"github.com/samcharles93/archie-core/internal/infrastructure/postgres"
	"github.com/samcharles93/archie-core/internal/infrastructure/postgres/pgstore"
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
	ids := map[org.OrgID]string{}
	for n, owner := range []org.OrgID{org.DefaultOrgID, "acme"} {
		typeID, err := eda.InsertEventType(ctx, eventtype.EventType{Source: "webhook", Name: string(owner), Rule: eventtype.Rule{Headers: []eventtype.HeaderCondition{{Name: "X-Org", Value: string(owner)}}}})
		if err != nil {
			t.Fatal(err)
		}
		mappingID, err := eda.InsertMapping(ctx, mapping.Mapping{Name: string(owner), EventTypeID: typeID})
		if err != nil {
			t.Fatal(err)
		}
		bindingID, err := eda.InsertBinding(ctx, binding.Binding{Name: string(owner), Matcher: binding.Matcher{Source: "webhook"}, MappingID: mappingID, Workflow: "deploy"})
		if err != nil {
			t.Fatal(err)
		}
		if err := eda.ApproveBinding(ctx, bindingID); err != nil {
			t.Fatal(err)
		}
		captureID, err := eda.InsertCapture(ctx, storecontract.CapturedEvent{Source: "webhook", Body: "{}", Headers: `{"X-Org":["` + string(owner) + `"]}`, Authenticated: true, EventType: typeID, ReceivedAt: time.Now().Add(time.Duration(n) * time.Minute)}, time.Hour, 100)
		if err != nil {
			t.Fatal(err)
		}
		// EDA writes do not yet take org ownership; model their persisted org
		// here to exercise the dispatch read independently of that work.
		for _, record := range []struct{ table, id string }{{"event_types", typeID}, {"mappings", mappingID}, {"bindings", bindingID}, {"captures", captureID}} {
			if _, err := db.Pool.Exec(ctx, "UPDATE "+record.table+" SET org_id=$1 WHERE id=$2", owner, record.id); err != nil {
				t.Fatal(err)
			}
		}
		ids[owner] = captureID
		if owner == "acme" {
			run, err := db.EnqueueBindingTask(org.WithOrg(ctx, owner), "", "", "Org dispatch", "", "deploy", "acme-bot", bindingID, 1, nil)
			if err != nil || run.Org != owner || run.Identity != "acme-bot" {
				t.Fatalf("binding task %#v err %v", run, err)
			}
		}
	}
	for _, owner := range []org.OrgID{org.DefaultOrgID, "acme"} {
		t.Run(string(owner), func(t *testing.T) {
			captures, err := eda.ListUndispatchedCaptures(org.WithOrg(t.Context(), owner), []string{"webhook"}, 1)
			if err != nil || len(captures) != 1 || captures[0].ID != ids[owner] {
				t.Fatalf("org %s got captures %#v err %v", owner, captures, err)
			}
		})
	}
}
