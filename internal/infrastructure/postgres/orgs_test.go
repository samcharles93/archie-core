package postgres

import (
	"errors"
	"testing"

	"github.com/samcharles93/archie-core/internal/domain/identity"
	"github.com/samcharles93/archie-core/internal/domain/org"
)

// newStoreForOrg is a migrated pool with the Store built over it.
func newStoreForOrg(t *testing.T) *Store {
	t.Helper()
	pool, _ := migrated(t)
	return New(pool)
}

// seedAgent inserts one bot identity to assign.
func seedAgent(t *testing.T, s *Store, name string) identity.Identity {
	t.Helper()
	value, err := identity.New(identity.StableID(name), identity.KindBot, name)
	if err != nil {
		t.Fatalf("identity.New(%s): %v", name, err)
	}
	created, err := s.Create(t.Context(), value, identity.Audit{
		ActorID: identity.SystemID, Source: "test", RequestID: "seed:" + name,
	})
	if err != nil {
		t.Fatalf("Create identity %s: %v", name, err)
	}
	return created
}

func TestOrgRepository(t *testing.T) {
	s := newStoreForOrg(t)
	ctx := t.Context()

	if _, err := s.CreateOrg(ctx, org.Org{ID: "soc", Name: "SOC"}); err != nil {
		t.Fatalf("CreateOrg: %v", err)
	}
	if _, err := s.CreateOrg(ctx, org.Org{ID: "soc", Name: "dup"}); !errors.Is(err, org.ErrOrgExists) {
		t.Fatalf("duplicate CreateOrg = %v, want ErrOrgExists", err)
	}
	if _, err := s.CreateOrg(ctx, org.Org{ID: "bad", Name: " "}); !errors.Is(err, org.ErrInvalidOrg) {
		t.Fatalf("blank-name CreateOrg = %v, want ErrInvalidOrg", err)
	}
	got, err := s.GetOrg(ctx, "soc")
	if err != nil {
		t.Fatalf("GetOrg: %v", err)
	}
	if got.Name != "SOC" {
		t.Fatalf("GetOrg = %+v, want name SOC", got)
	}
	if _, err := s.GetOrg(ctx, "missing"); !errors.Is(err, org.ErrOrgNotFound) {
		t.Fatalf("GetOrg(missing) = %v, want ErrOrgNotFound", err)
	}

	ws, err := s.CreateWorkspace(ctx, org.Workspace{ID: "net", OrgID: "soc", Name: "networking-prd", Environment: "production"})
	if err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}
	if _, err := s.CreateWorkspace(ctx, ws); !errors.Is(err, org.ErrWorkspaceExists) {
		t.Fatalf("duplicate CreateWorkspace = %v, want ErrWorkspaceExists", err)
	}
	if _, err := s.CreateWorkspace(ctx, org.Workspace{ID: "x", OrgID: "missing", Name: "x"}); !errors.Is(err, org.ErrOrgNotFound) {
		t.Fatalf("CreateWorkspace(unknown org) = %v, want ErrOrgNotFound", err)
	}
	workspaces, err := s.ListWorkspaces(ctx, "soc")
	if err != nil || len(workspaces) != 1 || workspaces[0].ID != "net" {
		t.Fatalf("ListWorkspaces = %+v, %v; want one net", workspaces, err)
	}

	member := seedAgent(t, s, "responder")
	if err := s.EnsureMembership(ctx, org.Membership{IdentityID: member.ID, OrgID: "soc", Role: org.RoleOwner}); err != nil {
		t.Fatalf("EnsureMembership: %v", err)
	}
	if err := s.EnsureMembership(ctx, org.Membership{IdentityID: member.ID, OrgID: "soc", Role: org.RoleOwner}); err != nil {
		t.Fatalf("repeated EnsureMembership: %v", err)
	}
	if err := s.EnsureMembership(ctx, org.Membership{OrgID: "soc", Role: org.RoleOwner}); !errors.Is(err, org.ErrInvalidMembership) {
		t.Fatalf("EnsureMembership(no identity) = %v, want ErrInvalidMembership", err)
	}
	missing := seedAgent(t, s, "ghost")
	if err := s.EnsureMembership(ctx, org.Membership{IdentityID: missing.ID, OrgID: "missing", Role: org.RoleOwner}); !errors.Is(err, org.ErrOrgNotFound) {
		t.Fatalf("EnsureMembership(unknown org) = %v, want ErrOrgNotFound", err)
	}

	agent := seedAgent(t, s, "archie")
	if err := s.AssignAgent(ctx, org.AgentAssignment{IdentityID: agent.ID, OrgID: "soc"}); err != nil {
		t.Fatalf("AssignAgent: %v", err)
	}
	if err := s.AssignAgent(ctx, org.AgentAssignment{IdentityID: agent.ID, OrgID: "missing"}); !errors.Is(err, org.ErrOrgNotFound) {
		t.Fatalf("AssignAgent(unknown org) = %v, want ErrOrgNotFound", err)
	}
	resolved, err := s.OrgForIdentity(ctx, agent.ID)
	if err != nil || resolved != "soc" {
		t.Fatalf("OrgForIdentity(agent) = %s, %v; want soc", resolved, err)
	}

	// An identity with only an org-wide membership resolves through it.
	// A person with only an org-wide membership resolves through it.
	person := seedAgent(t, s, "operator")
	if err := s.EnsureMembership(ctx, org.Membership{IdentityID: person.ID, OrgID: "soc", Role: org.RoleOwner}); err != nil {
		t.Fatalf("EnsureMembership: %v", err)
	}
	resolved, err = s.OrgForIdentity(ctx, person.ID)
	if err != nil || resolved != "soc" {
		t.Fatalf("OrgForIdentity(member) = %s, %v; want soc", resolved, err)
	}

	// An identity in no org resolves to the default org.
	resolved, err = s.OrgForIdentity(ctx, "nobody")
	if err != nil || resolved != org.DefaultOrgID {
		t.Fatalf("OrgForIdentity(no org) = %s, %v; want default", resolved, err)
	}
}

func TestUpgradeDefaultOrg(t *testing.T) {
	s := newStoreForOrg(t)
	ctx := t.Context()

	// Records written before the upgrade: one with an empty org (the shape a
	// pre-column row lands in), one stamped by the default backfill.
	agent := seedAgent(t, s, "archie")
	if err := s.queries().InsertOrgUpgradePhase(ctx, "state_store"); err != nil {
		t.Fatalf("pre-insert ledger row: %v", err)
	}

	// First run: completes both phases and is recorded.
	if err := s.UpgradeDefaultOrg(ctx); err != nil {
		t.Fatalf("UpgradeDefaultOrg: %v", err)
	}
	for _, phase := range []string{"state_store", "edastore"} {
		if _, err := s.queries().GetOrgUpgradePhase(ctx, phase); err != nil {
			t.Fatalf("phase %s not recorded: %v", phase, err)
		}
	}
	if _, err := s.GetOrg(ctx, org.DefaultOrgID); err != nil {
		t.Fatalf("default org missing after upgrade: %v", err)
	}
	workspaces, err := s.ListWorkspaces(ctx, org.DefaultOrgID)
	if err != nil || len(workspaces) != 1 || workspaces[0].ID != org.DefaultWorkspaceID {
		t.Fatalf("default workspace missing after upgrade: %+v, %v", workspaces, err)
	}
	resolved, err := s.OrgForIdentity(ctx, agent.ID)
	if err != nil || resolved != org.DefaultOrgID {
		t.Fatalf("agent org after upgrade = %s, %v; want default", resolved, err)
	}

	// A second run is a no-op: recorded phases are never re-run.
	if err := s.UpgradeDefaultOrg(ctx); err != nil {
		t.Fatalf("second UpgradeDefaultOrg: %v", err)
	}
}

func TestUpgradeStampsEmptyOrg(t *testing.T) {
	pool, _ := migrated(t)
	s := New(pool)
	ctx := t.Context()

	// Rows with an empty org, as a write between migration and upgrade would
	// leave them: one on each phase's side of the ledger.
	for _, stmt := range []string{
		"INSERT INTO resources (kind, value, version, updated_at, org_id) VALUES ('workflow:legacy', 'v', 1, now(), '')",
		"INSERT INTO bindings (id, name, source, secret) VALUES ('b1', 'b', 'src', '')",
		"UPDATE bindings SET org_id = '' WHERE id = 'b1'",
	} {
		if _, err := pool.Exec(ctx, stmt); err != nil {
			t.Fatalf("seed %q: %v", stmt, err)
		}
	}

	if err := s.UpgradeDefaultOrg(ctx); err != nil {
		t.Fatalf("UpgradeDefaultOrg: %v", err)
	}
	var stamped string
	if err := pool.QueryRow(ctx, "SELECT org_id FROM resources WHERE kind = 'workflow:legacy'").Scan(&stamped); err != nil || stamped != "default" {
		t.Fatalf("resource org after upgrade = %q, %v; want default", stamped, err)
	}
	if err := pool.QueryRow(ctx, "SELECT org_id, workspace_id FROM bindings WHERE id = 'b1'").Scan(&stamped, &stamped); err != nil || stamped != "default" {
		t.Fatalf("binding org/workspace after upgrade = %q, %v; want default", stamped, err)
	}
}
