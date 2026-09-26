// Access policies and denial records: the Postgres implementation of
// internal/domain/access's PolicyStore, Resetter and DenialStore
// (docs/prds/orgs-and-access.md). Policy changes are versioned per level
// scope and audited as one sys_audit row per change naming who made it.
package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/samcharles93/archie-core/internal/domain/access"
	"github.com/samcharles93/archie-core/internal/domain/identity"
	"github.com/samcharles93/archie-core/internal/domain/org"
	"github.com/samcharles93/archie-core/internal/infrastructure/postgres/postgresdb"
)

var (
	_ access.PolicyStore = (*Store)(nil)
	_ access.Resetter    = (*Store)(nil)
	_ access.DenialStore = (*Store)(nil)
)

// shippedOrgPolicyIDs lists an org's shipped role policy IDs: the seed's
// conflict keys, and what an org reset keeps.
func shippedOrgPolicyIDs() []string {
	ids := make([]string, 0, 4)
	for _, p := range access.ShippedOrgPolicies("") {
		ids = append(ids, p.ID)
	}
	return ids
}

// accessScopeOf is the scope tuple every access query carries.
func accessScopeOf(p access.Policy) (string, string, string, string, string, string) {
	return string(p.Level), string(p.OrgID), string(p.WorkspaceID),
		string(p.ObjectKind), p.ObjectID, p.ID
}

// auditKey is the sys_audit record_key for one policy: its scope and ID, so
// an operator can trace one policy document's changes.
func auditKey(p access.Policy) string {
	joined := strings.Join([]string{
		string(p.Level), string(p.OrgID), string(p.WorkspaceID),
		string(p.ObjectKind), p.ObjectID, p.ID,
	}, "/")
	return strings.Trim(joined, "/")
}

// textJSON wraps a policy text as the JSON string sys_audit stores.
func textJSON(text string) []byte {
	out, _ := json.Marshal(text)
	return out
}

// ListPolicies returns every stored policy across levels.
func (s *Store) ListPolicies(ctx context.Context) ([]access.Policy, error) {
	rows, err := s.queries().ListAccessPolicies(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]access.Policy, 0, len(rows))
	for _, r := range rows {
		out = append(out, access.Policy{
			ID:          r.PolicyID,
			Level:       access.Level(r.Level),
			OrgID:       org.OrgID(r.OrgID),
			WorkspaceID: org.WorkspaceID(r.WorkspaceID),
			ObjectKind:  access.ResourceKind(r.ObjectKind),
			ObjectID:    r.ObjectID,
			Text:        r.Text,
		})
	}
	return out, nil
}

// PutPolicy stores or replaces one policy by its scope+ID key, bumps the
// level's version, and writes the audit row -- in one transaction, so a
// version that never moved cannot advertise a change that half-landed.
func (s *Store) PutPolicy(ctx context.Context, p access.Policy) (int64, error) {
	if err := p.Validate(); err != nil {
		return 0, err
	}
	q := s.queries()
	level, orgID, wsID, kind, id, policyID := accessScopeOf(p)
	var version int64
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := q.WithTx(tx)
		if err := q.UpsertAccessPolicy(ctx, postgresdb.UpsertAccessPolicyParams{
			Level: level, OrgID: orgID, WorkspaceID: wsID,
			ObjectKind: kind, ObjectID: id,
			PolicyID: policyID, Text: p.Text,
		}); err != nil {
			return err
		}
		v, err := q.UpsertAccessPolicyVersion(ctx, postgresdb.UpsertAccessPolicyVersionParams{
			Level: level, OrgID: orgID, WorkspaceID: wsID,
		})
		if err != nil {
			return err
		}
		version = v
		return q.InsertAccessPolicyAudit(ctx, postgresdb.InsertAccessPolicyAuditParams{
			RecordKey:     auditKey(p),
			NewValue:      textJSON(p.Text),
			RecordVersion: version,
			Actor:         "archied",
			Source:        "archied",
			RequestID:     "",
		})
	})
	if err != nil {
		return 0, err
	}
	return version, nil
}

// DeletePolicy removes one stored policy; an unknown key is
// ErrPolicyNotFound.
func (s *Store) DeletePolicy(ctx context.Context, p access.Policy) error {
	if err := p.Validate(); err != nil {
		return err
	}
	level, orgID, wsID, kind, id, policyID := accessScopeOf(p)
	deleted, err := s.queries().DeleteAccessPolicy(ctx, postgresdb.DeleteAccessPolicyParams{
		Level: level, OrgID: orgID, WorkspaceID: wsID,
		ObjectKind: kind, ObjectID: id, PolicyID: policyID,
	})
	if err != nil {
		return err
	}
	if deleted == 0 {
		return access.ErrPolicyNotFound
	}
	if _, err := s.queries().UpsertAccessPolicyVersion(ctx, postgresdb.UpsertAccessPolicyVersionParams{
		Level: level, OrgID: orgID, WorkspaceID: wsID,
	}); err != nil {
		return err
	}
	// A level-scope with no policies left keeps no version row.
	return s.queries().DeleteAccessPolicyVersion(ctx, postgresdb.DeleteAccessPolicyVersionParams{
		Level: level, OrgID: orgID, WorkspaceID: wsID,
	})
}

// EnsureShippedOrgPolicies seeds an org's shipped role policies when it
// carries none of them, so a restart never overwrites an edited role policy.
func (s *Store) EnsureShippedOrgPolicies(ctx context.Context, orgID org.OrgID) error {
	shipped := access.ShippedOrgPolicies(orgID)
	count, err := s.queries().CountShippedOrgPolicies(ctx, postgresdb.CountShippedOrgPoliciesParams{
		OrgID: string(orgID), Column2: shippedOrgPolicyIDs(),
	})
	if err != nil {
		return err
	}
	if count > 0 {
		return nil
	}
	for _, p := range shipped {
		if _, err := s.PutPolicy(ctx, p); err != nil {
			return err
		}
	}
	return nil
}

// ResetOrgPolicies restores the shipped role policies for one org and
// removes its other org-level policies. It is the store-side half of
// `archied access reset --org`, which runs on this host and records the
// reset as an audit event.
func (s *Store) ResetOrgPolicies(ctx context.Context, orgID org.OrgID) error {
	if orgID == "" {
		return fmt.Errorf("%w: no org", access.ErrInvalidPolicy)
	}
	if err := s.queries().DeleteOrgPoliciesNotShipped(ctx, postgresdb.DeleteOrgPoliciesNotShippedParams{
		OrgID: string(orgID), Column2: shippedOrgPolicyIDs(),
	}); err != nil {
		return err
	}
	for _, p := range access.ShippedOrgPolicies(orgID) {
		if _, err := s.PutPolicy(ctx, p); err != nil {
			return err
		}
	}
	return nil
}

// ResetInstancePolicies removes every stored instance policy. The
// cross-org forbid is engine-enforced and survives any reset.
func (s *Store) ResetInstancePolicies(ctx context.Context) error {
	return s.queries().ResetInstancePolicies(ctx)
}

// RecordResetAudit records the one audit row a reset is written as.
func (s *Store) RecordResetAudit(ctx context.Context, key string) error {
	return s.queries().InsertAccessResetAudit(ctx, key)
}

// RecordDenial records one refusal The store coalesces identical denials
// within a minute into one row with a count, so a retry storm does not
// flood the surface.
func (s *Store) RecordDenial(ctx context.Context, d access.Denial) error {
	if d.Org == "" || d.Principal == "" {
		return fmt.Errorf("%w: org and principal are required", access.ErrInvalidPolicy)
	}
	return s.queries().RecordDenial(ctx, postgresdb.RecordDenialParams{
		OrgID: string(d.Org), Principal: string(d.Principal), Action: string(d.Action),
		ResourceKind: string(d.Kind), ResourceID: d.ResourceID, Level: string(d.Level),
		Policies: d.Policies,
	})
}

// ListDenials returns an org's most recent denials, newest first.
func (s *Store) ListDenials(ctx context.Context, orgID org.OrgID, limit int) ([]access.Denial, error) {
	rows, err := s.queries().ListDenials(ctx, postgresdb.ListDenialsParams{
		OrgID: string(orgID), Limit: int32(limit),
	})
	if err != nil {
		return nil, err
	}
	out := make([]access.Denial, 0, len(rows))
	for _, r := range rows {
		out = append(out, access.Denial{
			Principal:  identity.IdentityID(r.Principal),
			Org:        org.OrgID(r.OrgID),
			Action:     access.Action(r.Action),
			Kind:       access.ResourceKind(r.ResourceKind),
			ResourceID: r.ResourceID,
			Level:      access.Level(r.Level),
			Policies:   r.Policies,
			At:         r.CreatedAt,
			Count:      r.Count,
		})
	}
	return out, nil
}
