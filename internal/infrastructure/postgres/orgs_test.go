package postgres_test

import (
	"errors"
	"testing"

	"github.com/samcharles93/archie-core/internal/domain/identity"
	"github.com/samcharles93/archie-core/internal/domain/org"
	"github.com/samcharles93/archie-core/internal/infrastructure/postgres/pgstore"
)

// TestOrgStoreMembers pins the org membership queries: a role change updates
// the existing membership, the list joins the identity, removal deletes, agent
// assignment lists, and an unknown role is refused naming it.
func TestOrgStoreMembers(t *testing.T) {
	ctx := t.Context()
	db := pgstore.Open(t)
	for _, id := range []string{"sam", "bot"} {
		kind := identity.KindUser
		name := "Sam"
		if id == "bot" {
			kind, name = identity.KindBot, "Bot"
		}
		if _, err := db.Create(ctx, identity.Identity{ID: identity.IdentityID(id), Kind: kind, DisplayName: name, Lifecycle: identity.LifecycleActive}, identity.Audit{ActorID: identity.SystemID, Source: "test", RequestID: "id-" + id}); err != nil {
			t.Fatalf("create identity %s: %v", id, err)
		}
	}
	if _, err := db.CreateOrg(ctx, org.Org{ID: "acme", Name: "Acme"}, "sam"); err != nil {
		t.Fatalf("create org: %v", err)
	}

	if err := db.EnsureMembership(ctx, org.Membership{IdentityID: "sam", OrgID: "acme", Role: org.RoleDeveloper}); err != nil {
		t.Fatalf("ensure membership: %v", err)
	}
	if err := db.EnsureMembership(ctx, org.Membership{IdentityID: "sam", OrgID: "acme", Role: org.RoleAdmin}); err != nil {
		t.Fatalf("change membership: %v", err)
	}
	members, err := db.ListMembers(ctx, "acme")
	if err != nil {
		t.Fatalf("list members: %v", err)
	}
	if len(members) != 1 {
		t.Fatalf("members = %d, want 1", len(members))
	}
	got := members[0]
	if got.IdentityID != "sam" || got.Kind != identity.KindUser || got.DisplayName != "Sam" || got.Role != org.RoleAdmin {
		t.Fatalf("member = %#v", got)
	}

	if err := db.RemoveMembership(ctx, org.Membership{IdentityID: "sam", OrgID: "acme"}); err != nil {
		t.Fatalf("remove membership: %v", err)
	}
	if members, err = db.ListMembers(ctx, "acme"); err != nil || len(members) != 0 {
		t.Fatalf("after remove: members = %d, err %v", len(members), err)
	}
	// Removing again is not an error: the target is simply gone.
	if err := db.RemoveMembership(ctx, org.Membership{IdentityID: "sam", OrgID: "acme"}); err != nil {
		t.Fatalf("second remove: %v", err)
	}

	if err := db.AssignAgent(ctx, org.AgentAssignment{IdentityID: "bot", OrgID: "acme"}); err != nil {
		t.Fatalf("assign agent: %v", err)
	}
	agents, err := db.ListOrgAgents(ctx, "acme")
	if err != nil {
		t.Fatalf("list agents: %v", err)
	}
	if len(agents) != 1 || agents[0].IdentityID != "bot" {
		t.Fatalf("agents = %#v", agents)
	}

	if err := db.EnsureMembership(ctx, org.Membership{IdentityID: "sam", OrgID: "acme", Role: "superuser"}); !errors.Is(err, org.ErrInvalidRole) {
		t.Fatalf("unknown role err = %v, want ErrInvalidRole", err)
	}
}
