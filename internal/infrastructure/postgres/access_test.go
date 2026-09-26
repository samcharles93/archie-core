package postgres

import (
	"errors"
	"testing"

	"github.com/samcharles93/archie-core/internal/domain/access"
	"github.com/samcharles93/archie-core/internal/domain/identity"
)

func newStoreForAccess(t *testing.T) *Store {
	t.Helper()
	pool, _ := migrated(t)
	return New(pool)
}

func TestAccessPolicyStore(t *testing.T) {
	s := newStoreForAccess(t)
	ctx := t.Context()

	// PutPolicy stores one policy and reports a versioned level.
	p := access.Policy{
		ID: "test-policy", Level: access.LevelOrg, OrgID: "soc",
		Text: `permit(principal, action, resource) when { resource.kind == "binding" };`,
	}
	v1, err := s.PutPolicy(ctx, p)
	if err != nil {
		t.Fatalf("PutPolicy: %v", err)
	}
	if v1 != 2 { // the counter starts at 1; the first write bumps it
		t.Fatalf("PutPolicy version = %d, want 2", v1)
	}
	v2, err := s.PutPolicy(ctx, p)
	if err != nil {
		t.Fatalf("second PutPolicy: %v", err)
	}
	if v2 <= v1 {
		t.Fatalf("second PutPolicy version = %d, want > %d", v2, v1)
	}

	// A malformed policy is refused by the store's own structural check.
	bad := access.Policy{ID: "bad", Level: access.LevelOrg, OrgID: "soc"}
	if _, err := s.PutPolicy(ctx, bad); !errors.Is(err, access.ErrInvalidPolicy) {
		t.Fatalf("PutPolicy(structurally invalid) = %v, want ErrInvalidPolicy", err)
	}

	// ListPolicies round-trips the scope tuple.
	list, err := s.ListPolicies(ctx)
	if err != nil {
		t.Fatalf("ListPolicies: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("ListPolicies = %d policies, want 1", len(list))
	}
	if list[0].ID != "test-policy" || list[0].OrgID != "soc" {
		t.Fatalf("ListPolicies[0] = %+v, want the stored policy", list[0])
	}

	// DeletePolicy removes it and reports an unknown key as a sentinel.
	if err := s.DeletePolicy(ctx, p); err != nil {
		t.Fatalf("DeletePolicy: %v", err)
	}
	if err := s.DeletePolicy(ctx, p); !errors.Is(err, access.ErrPolicyNotFound) {
		t.Fatalf("second DeletePolicy = %v, want ErrPolicyNotFound", err)
	}
}

func TestEnsureShippedOrgPolicies(t *testing.T) {
	s := newStoreForAccess(t)
	ctx := t.Context()

	if err := s.EnsureShippedOrgPolicies(ctx, "soc"); err != nil {
		t.Fatalf("EnsureShippedOrgPolicies: %v", err)
	}
	list, err := s.ListPolicies(ctx)
	if err != nil {
		t.Fatalf("ListPolicies: %v", err)
	}
	if len(list) != 4 {
		t.Fatalf("seeding produced %d policies, want the 4 shipped roles", len(list))
	}

	// An edited role policy survives a re-seed: an org that already carries
	// the shipped IDs is left untouched.
	edited := list[0]
	edited.Text = `permit(principal, action, resource) when { resource.kind == "edited" };`
	if _, err := s.PutPolicy(ctx, edited); err != nil {
		t.Fatalf("PutPolicy(edit): %v", err)
	}
	if err := s.EnsureShippedOrgPolicies(ctx, "soc"); err != nil {
		t.Fatalf("re-seed: %v", err)
	}
	after, err := s.ListPolicies(ctx)
	if err != nil {
		t.Fatalf("ListPolicies after re-seed: %v", err)
	}
	for _, p := range after {
		if p.ID == edited.ID && p.Text != edited.Text {
			t.Fatalf("re-seed overwrote an edited role policy %s", p.ID)
		}
	}
}

func TestResetPolicies(t *testing.T) {
	s := newStoreForAccess(t)
	ctx := t.Context()

	if err := s.ResetOrgPolicies(ctx, "soc"); err != nil {
		t.Fatalf("ResetOrgPolicies: %v", err)
	}
	// An operator's custom org policy on top of the shipped set.
	custom := access.Policy{
		ID: "org-lockdown", Level: access.LevelOrg, OrgID: "soc",
		Text: `forbid(principal, action, resource) when { resource.kind == "source" };`,
	}
	if _, err := s.PutPolicy(ctx, custom); err != nil {
		t.Fatalf("PutPolicy(custom): %v", err)
	}
	// A workspace policy must survive an org reset.
	ws := access.Policy{
		ID: "ws-rule", Level: access.LevelWorkspace, OrgID: "soc", WorkspaceID: "net",
		Text: `permit(principal, action, resource) when { resource.kind == "binding" };`,
	}
	if _, err := s.PutPolicy(ctx, ws); err != nil {
		t.Fatalf("PutPolicy(ws): %v", err)
	}
	// And an instance policy must survive an org reset but not an instance
	// reset.
	instance := access.Policy{
		ID: "instance-rule", Level: access.LevelInstance,
		Text: `forbid(principal, action, resource) when { resource.kind == "secret" };`,
	}
	if _, err := s.PutPolicy(ctx, instance); err != nil {
		t.Fatalf("PutPolicy(instance): %v", err)
	}

	if err := s.ResetOrgPolicies(ctx, "soc"); err != nil {
		t.Fatalf("org reset: %v", err)
	}
	after, err := s.ListPolicies(ctx)
	if err != nil {
		t.Fatalf("ListPolicies: %v", err)
	}
	orgPolicies := 0
	for _, p := range after {
		switch {
		case p.Level == access.LevelOrg && p.OrgID == "soc":
			orgPolicies++
		case p.Level == access.LevelOrg:
			t.Fatalf("org reset touched policy %s of another org", p.ID)
		}
	}
	if orgPolicies != 4 {
		t.Fatalf("org-level policies after reset = %d, want the 4 shipped roles", orgPolicies)
	}

	if err := s.ResetInstancePolicies(ctx); err != nil {
		t.Fatalf("ResetInstancePolicies: %v", err)
	}
	after, err = s.ListPolicies(ctx)
	if err != nil {
		t.Fatalf("ListPolicies after instance reset: %v", err)
	}
	for _, p := range after {
		if p.Level == access.LevelInstance {
			t.Fatalf("instance reset left policy %s", p.ID)
		}
	}
	if len(after) == 0 {
		t.Fatal("instance reset removed non-instance policies")
	}
}

func TestDenialStore(t *testing.T) {
	s := newStoreForAccess(t)
	ctx := t.Context()

	denial := access.Denial{
		Principal: identity.IdentityID("11111111-1111-5111-8111-111111111111"),
		Org:       "soc", Action: access.ActionRead, Kind: access.KindBinding,
		ResourceID: "b1", Level: access.LevelOrg,
		Policies: []string{access.PolicyOrgRead, access.PolicyOrgEdit},
	}
	if err := s.RecordDenial(ctx, denial); err != nil {
		t.Fatalf("RecordDenial: %v", err)
	}
	if err := s.RecordDenial(ctx, denial); err != nil {
		t.Fatalf("repeat RecordDenial: %v", err)
	}
	// A different action is a separate row.
	other := denial
	other.Action = access.ActionDelete
	if err := s.RecordDenial(ctx, other); err != nil {
		t.Fatalf("RecordDenial(other action): %v", err)
	}

	rows, err := s.ListDenials(ctx, "soc", 10)
	if err != nil {
		t.Fatalf("ListDenials: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("ListDenials = %d rows, want 2", len(rows))
	}
	if rows[0].Action != access.ActionDelete {
		t.Fatalf("newest denial = %v, want the delete denial", rows[0].Action)
	}
	for _, r := range rows {
		if r.Action == access.ActionRead {
			if r.Count != 2 {
				t.Fatalf("repeated denial count = %d, want 2 (coalesced within the minute)", r.Count)
			}
			if len(r.Policies) != 2 {
				t.Fatalf("denial policies = %v, want the deciding policies", r.Policies)
			}
		}
	}
	// Another org sees none of these.
	if rows, err := s.ListDenials(ctx, "other", 10); err != nil || len(rows) != 0 {
		t.Fatalf("ListDenials(other org) = %d rows %v, want none", len(rows), err)
	}
	// An org-less denial is refused at the producer.
	if err := s.RecordDenial(ctx, access.Denial{Action: access.ActionRead}); err == nil {
		t.Fatal("RecordDenial accepted a denial with no org or principal")
	}
}
