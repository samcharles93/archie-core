// The persistence contracts for policies and denial records. The State Store
// implements them (internal/infrastructure/postgres); composition wires them
// like org.Repository.
package access

import (
	"context"

	"github.com/samcharles93/archie-core/internal/domain/org"
)

// PolicyStore persists stored policies. Versioned per level: PutPolicy
// returns the level's new version, so a policy change is one version bump
// the engine's next load can detect. Every change is an audit event naming
// who made it.
type PolicyStore interface {
	// ListPolicies returns every stored policy across levels.
	ListPolicies(ctx context.Context) ([]Policy, error)
	// PutPolicy stores or replaces one policy by its (level, scope, ID)
	// key, and returns the level's new version.
	PutPolicy(ctx context.Context, p Policy) (int64, error)
	// DeletePolicy removes one stored policy; an unknown key is
	// ErrPolicyNotFound.
	DeletePolicy(ctx context.Context, p Policy) error
	// EnsureShippedOrgPolicies seeds an org's shipped role policies when it
	// has none; an org that already carries them is left untouched, so a
	// restart never overwrites an edited role policy.
	EnsureShippedOrgPolicies(ctx context.Context, orgID org.OrgID) error
}

// Resetter restores access for a locked-out org or a broken instance policy
// (docs/prds/orgs-and-access.md, "Recovering from a locked-out org"). It
// runs only on the State Store host, never over the network, and the reset
// command records it as an audit event.
type Resetter interface {
	// ResetOrgPolicies restores the shipped role policies for one org and
	// removes its other org-level policies.
	ResetOrgPolicies(ctx context.Context, orgID org.OrgID) error
	// ResetInstancePolicies removes every stored instance policy. The
	// cross-org forbid is engine-enforced and survives any reset.
	ResetInstancePolicies(ctx context.Context) error
}

// DenialStore records and lists denial records. The store coalesces
// identical denials within a minute into one row with a count, so a retry
// storm does not flood the surface.
type DenialStore interface {
	// RecordDenial records one refusal with the level that decided it and
	// the deciding policies.
	RecordDenial(ctx context.Context, d Denial) error
	// ListDenials returns an org's most recent denials, newest first.
	ListDenials(ctx context.Context, orgID org.OrgID, limit int) ([]Denial, error)
}
