// Orgs, workspaces, memberships and agent assignments: the Postgres
// implementation of internal/domain/org's contracts, plus the resumable
// default org/workspace upgrade the State Store runs at boot
// (docs/prds/orgs-and-access.md, "Upgrading existing installs").
//
// The upgrade refuses nothing itself; the refusal to serve scoped calls
// until it has finished is enforced by the boot order -- the State Store
// runs this upgrade before its gRPC listener starts, and a failed upgrade
// fails the boot (fail closed) rather than serving records with no org.
package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/samcharles93/archie-core/internal/domain/access"
	"github.com/samcharles93/archie-core/internal/domain/identity"
	"github.com/samcharles93/archie-core/internal/domain/org"
	"github.com/samcharles93/archie-core/internal/infrastructure/postgres/postgresdb"
)

var (
	_ org.Repository = (*Store)(nil)
	_ org.Upgrader   = (*Store)(nil)
)

// orgFromRow maps the generated org row to the domain org.
func orgFromRow(o postgresdb.Org) org.Org {
	return org.Org{ID: org.OrgID(o.ID), Name: o.Name}
}

// CreateOrg inserts a new org; re-creating an existing ID is ErrOrgExists.
func (s *Store) CreateOrg(ctx context.Context, value org.Org) (org.Org, error) {
	if err := value.Validate(); err != nil {
		return org.Org{}, err
	}
	if err := s.queries().InsertOrg(ctx, postgresdb.InsertOrgParams{
		ID: string(value.ID), Name: value.Name,
	}); err != nil {
		if isUniqueViolation(err) {
			return org.Org{}, fmt.Errorf("%w: %s", org.ErrOrgExists, value.ID)
		}
		return org.Org{}, err
	}
	return value, nil
}

// GetOrg returns one org by ID, or ErrOrgNotFound.
func (s *Store) GetOrg(ctx context.Context, id org.OrgID) (org.Org, error) {
	o, err := s.queries().GetOrg(ctx, string(id))
	if errors.Is(err, pgx.ErrNoRows) {
		return org.Org{}, org.ErrOrgNotFound
	}
	if err != nil {
		return org.Org{}, err
	}
	return orgFromRow(o), nil
}

// ListOrgs returns every org, oldest first.
func (s *Store) ListOrgs(ctx context.Context) ([]org.Org, error) {
	rows, err := s.queries().ListOrgs(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]org.Org, 0, len(rows))
	for _, r := range rows {
		out = append(out, orgFromRow(r))
	}
	return out, nil
}

// CreateWorkspace inserts a workspace into an existing org; an unknown org is
// ErrOrgNotFound.
func (s *Store) CreateWorkspace(ctx context.Context, value org.Workspace) (org.Workspace, error) {
	if err := value.Validate(); err != nil {
		return org.Workspace{}, err
	}
	if _, err := s.GetOrg(ctx, value.OrgID); err != nil {
		return org.Workspace{}, err
	}
	if err := s.queries().InsertWorkspace(ctx, postgresdb.InsertWorkspaceParams{
		ID: string(value.ID), OrgID: string(value.OrgID),
		Name: value.Name, Environment: value.Environment,
	}); err != nil {
		if isUniqueViolation(err) {
			return org.Workspace{}, fmt.Errorf("%w: %s/%s", org.ErrWorkspaceExists, value.OrgID, value.ID)
		}
		return org.Workspace{}, err
	}
	return value, nil
}

// workspaceFromRow maps the generated workspace row to the domain workspace.
func workspaceFromRow(w postgresdb.Workspace) org.Workspace {
	return org.Workspace{
		ID: org.WorkspaceID(w.ID), OrgID: org.OrgID(w.OrgID),
		Name: w.Name, Environment: w.Environment,
	}
}

// ListWorkspaces returns an org's workspaces, oldest first.
func (s *Store) ListWorkspaces(ctx context.Context, id org.OrgID) ([]org.Workspace, error) {
	rows, err := s.queries().ListWorkspaces(ctx, string(id))
	if err != nil {
		return nil, err
	}
	out := make([]org.Workspace, 0, len(rows))
	for _, r := range rows {
		out = append(out, workspaceFromRow(r))
	}
	return out, nil
}

// EnsureMembership grants an identity a role in an org, or in one workspace
// of it; an identical existing membership is not an error.
func (s *Store) EnsureMembership(ctx context.Context, value org.Membership) error {
	if err := value.Validate(); err != nil {
		return err
	}
	var workspace pgtype.Text
	if value.WorkspaceID != "" {
		workspace = pgtype.Text{String: string(value.WorkspaceID), Valid: true}
	}
	if _, err := s.GetOrg(ctx, value.OrgID); err != nil {
		return err
	}
	return s.queries().EnsureMembership(ctx, postgresdb.EnsureMembershipParams{
		IdentityID:  string(value.IdentityID),
		OrgID:       string(value.OrgID),
		WorkspaceID: workspace,
		Role:        string(value.Role),
	})
}

// AssignAgent records the one org an agent or service identity serves,
// replacing any prior assignment for that identity.
func (s *Store) AssignAgent(ctx context.Context, value org.AgentAssignment) error {
	if err := value.Validate(); err != nil {
		return err
	}
	if _, err := s.GetOrg(ctx, value.OrgID); err != nil {
		return err
	}
	return s.queries().UpsertAgentAssignment(ctx, postgresdb.UpsertAgentAssignmentParams{
		IdentityID: string(value.IdentityID), OrgID: string(value.OrgID),
	})
}

// EnsureAgentOrgMembership grants every agent identity the shipped developer
// role in the org it serves. It runs at the State Store's boot, after the
// default-org upgrade, so dispatch's chain has a principal to evaluate.
// Idempotent: an existing membership (any role) is left alone.
func (s *Store) EnsureAgentOrgMembership(ctx context.Context) error {
	return s.queries().EnsureAgentOrgMembership(ctx)
}

// PrincipalFor assembles the access principal for one identity: the org it
// serves and its memberships (internal/domain/access). An identity in no org
// is the default org with no role, which the shipped role policies never
// permit.
func (s *Store) PrincipalFor(ctx context.Context, id identity.IdentityID) (access.Principal, error) {
	resolved, err := s.OrgForIdentity(ctx, id)
	if err != nil {
		return access.Principal{}, err
	}
	rows, err := s.queries().ListMembershipsByIdentity(ctx, string(id))
	if err != nil {
		return access.Principal{}, err
	}
	memberships := make([]org.Membership, 0, len(rows))
	for _, r := range rows {
		var ws org.WorkspaceID
		if r.WorkspaceID.Valid {
			ws = org.WorkspaceID(r.WorkspaceID.String)
		}
		memberships = append(memberships, org.Membership{
			IdentityID:  id,
			OrgID:       org.OrgID(r.OrgID),
			WorkspaceID: ws,
			Role:        org.Role(r.Role),
		})
	}
	return access.Principal{IdentityID: id, Org: resolved, Memberships: memberships}, nil
}

// OrgForIdentity returns the org an identity serves: the assigned org for an
// agent, the org of one of its memberships for a person, and the default org
// for an identity in no org.
func (s *Store) OrgForIdentity(ctx context.Context, id identity.IdentityID) (org.OrgID, error) {
	resolved, err := s.queries().ResolveIdentityOrg(ctx, string(id))
	if err != nil {
		return "", err
	}
	return org.OrgID(resolved), nil
}

// UpgradeDefaultOrg runs the resumable default org/workspace upgrade: the
// State Store phase first, the event-capture phase second, each in one
// transaction with its ledger row so a phase is either recorded or entirely
// absent, and each skipped on a later start once recorded.
func (s *Store) UpgradeDefaultOrg(ctx context.Context) error {
	type phase struct {
		name   string
		stamps []func(context.Context, *postgresdb.Queries) (int64, error)
	}
	phases := []phase{
		{name: "state_store", stamps: []func(context.Context, *postgresdb.Queries) (int64, error){
			func(ctx context.Context, q *postgresdb.Queries) (int64, error) { return q.StampStateStoreTasks(ctx) },
			func(ctx context.Context, q *postgresdb.Queries) (int64, error) { return q.StampStateStoreEvents(ctx) },
			func(ctx context.Context, q *postgresdb.Queries) (int64, error) {
				return q.StampStateStoreTransitions(ctx)
			},
			func(ctx context.Context, q *postgresdb.Queries) (int64, error) {
				return q.StampStateStoreResources(ctx)
			},
			func(ctx context.Context, q *postgresdb.Queries) (int64, error) {
				return q.StampStateStoreResourceHistory(ctx)
			},
		}},
		{name: "edastore", stamps: []func(context.Context, *postgresdb.Queries) (int64, error){
			func(ctx context.Context, q *postgresdb.Queries) (int64, error) { return q.StampEdastoreBindings(ctx) },
			func(ctx context.Context, q *postgresdb.Queries) (int64, error) { return q.StampEdastoreMappings(ctx) },
			func(ctx context.Context, q *postgresdb.Queries) (int64, error) { return q.StampEdastoreCaptures(ctx) },
			func(ctx context.Context, q *postgresdb.Queries) (int64, error) { return q.StampEdastoreSources(ctx) },
			func(ctx context.Context, q *postgresdb.Queries) (int64, error) { return q.StampEdastoreEventTypes(ctx) },
			func(ctx context.Context, q *postgresdb.Queries) (int64, error) { return q.StampEdastoreToolCalls(ctx) },
		}},
	}
	for _, p := range phases {
		if _, err := s.queries().GetOrgUpgradePhase(ctx, p.name); err == nil {
			continue // recorded: the phase committed on an earlier start
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if err := s.runUpgradePhase(ctx, p.name, p.stamps); err != nil {
			return fmt.Errorf("org upgrade phase %s: %w", p.name, err)
		}
	}
	return nil
}

// runUpgradePhase stamps one phase's tables and records its ledger row in one
// transaction, after making sure the default org and workspace exist and
// every agent identity belongs to the default org. An interrupted phase
// leaves no ledger row, so the next start re-runs it from the beginning --
// every stamp is idempotent on rows the upgrade has not covered yet.
func (s *Store) runUpgradePhase(ctx context.Context, name string, stamps []func(context.Context, *postgresdb.Queries) (int64, error)) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := postgresdb.New(tx)

	if err := q.EnsureDefaultOrg(ctx); err != nil {
		return err
	}
	if err := q.EnsureDefaultWorkspace(ctx); err != nil {
		return err
	}
	if err := q.AssignDefaultOrgIdentities(ctx); err != nil {
		return err
	}
	for _, stamp := range stamps {
		if _, err := stamp(ctx, q); err != nil {
			return err
		}
	}
	if err := q.InsertOrgUpgradePhase(ctx, name); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
