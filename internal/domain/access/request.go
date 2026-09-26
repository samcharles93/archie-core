// The entities a decision is made about. The domain owns their shape and the
// Cedar vocabulary they are built from (the schema); the engine in
// internal/infrastructure/access turns them into engine entities. This file
// carries no Cedar dependency: the vocabulary is plain constants so the
// domain never links the engine.
package access

import (
	"time"

	"github.com/samcharles93/archie-core/internal/domain/identity"
	"github.com/samcharles93/archie-core/internal/domain/org"
)

// The Cedar vocabulary every policy and every assembled entity shares. A
// policy naming an unknown type, action or attribute is refused when it is
// saved and denied at every start (the engine's validation).
const (
	// Namespace prefixes every Cedar entity type the engine assembles.
	Namespace = "Archie"

	// EntityTypeIdentity is the principal: an Archie identity.
	EntityTypeIdentity = Namespace + "::Identity"
	// EntityTypeOrg is the org a resource belongs to.
	EntityTypeOrg = Namespace + "::Org"
	// EntityTypeWorkspace is the workspace a resource belongs to.
	EntityTypeWorkspace = Namespace + "::Workspace"
	// EntityTypeObject is the resource acted on. Every record -- source,
	// binding, workflow, secret, task, the org itself -- is one Object
	// entity carrying its kind, so a policy can scope on
	// `resource.kind == Archie::Kind::"binding"` without a type per record.
	EntityTypeObject = Namespace + "::Object"
	// EntityTypeAction is the action entity of a request, one per Action.
	EntityTypeAction = Namespace + "::Action"

	// AttributeOrg is the org ID an identity or object carries.
	AttributeOrg = "org"
	// AttributeRole is the principal's effective role for the resource
	// (workspace membership role when one exists, else its org-wide role).
	AttributeRole = "role"
	// AttributeKind is an object's resource kind.
	AttributeKind = "kind"
	// AttributeOwner is the ID of the identity that owns an object.
	AttributeOwner = "owner"
	// AttributeState is an object's state (binding's draft/armed, task's
	// status, and so on), as the caller that assembles the request sees it.
	AttributeState = "state"
)

// Principal is who acts: an identity, with the memberships that give it
// roles. The org is the one the identity serves (org.Repository's
// OrgForIdentity); Memberships carries its org-wide and workspace roles.
type Principal struct {
	IdentityID  identity.IdentityID
	Kind        identity.Kind
	Org         org.OrgID
	Memberships []org.Membership
}

// Role returns the principal's effective role for a resource: the role of
// its membership in the resource's workspace when it holds one, else the
// role of its org-wide membership, else the empty string (no role, which
// the shipped role policies never permit).
func (p Principal) Role(ws org.WorkspaceID) org.Role {
	var orgRole org.Role
	for _, m := range p.Memberships {
		if m.OrgID != p.Org {
			continue
		}
		if m.WorkspaceID == "" {
			orgRole = m.Role
		}
		if m.WorkspaceID == ws {
			return m.Role
		}
	}
	return orgRole
}

// Resource is the record acted on: its type, its workspace and org as
// parents, its owner and its state.
type Resource struct {
	Kind ResourceKind
	// ID is the record's caller-facing key: a source's path, a binding's
	// ID, an org's ID, a workflow's name.
	ID string
	// Org is the org the record belongs to. Empty means the principal's own
	// org, which is how a create request names a resource that does not
	// exist yet.
	Org org.OrgID
	// Workspace is the workspace the record belongs to, empty for
	// org-owned records.
	Workspace org.WorkspaceID
	Owner     identity.IdentityID
	State     string
}

// Context is what was going on when the decision was asked for.
type Context struct {
	// Signature is the event's signature result ("valid", "unsigned", ...).
	Signature string
	// Addr is the address the event came from.
	Addr string
	// Time is when the request was made; zero means now.
	Time time.Time
	// Run and Step identify the run and step when the principal is an agent.
	Run  string
	Step string
}

// Decision is the outcome of one Authorize call. When the request is denied,
// Level names the level that decided and Policies the IDs of the deciding
// policies at that level, so a denial record can name them.
type Decision struct {
	Allowed  bool
	Level    Level
	Policies []string
	// Err carries the engine failure behind a deny-all level: a stored
	// policy that no longer parses. A denied request with Err set was never
	// evaluated -- the level refuses everything rather than decide.
	Err error
}

// Allowed is the shared permit decision: no level denied.
func Allowed() Decision { return Decision{Allowed: true} }

// DeniedAt records a denial decided at a level by the given policies.
func DeniedAt(level Level, policies []string) Decision {
	return Decision{Level: level, Policies: policies}
}

// Denial is one recorded refusal: who was refused, doing what, to what, by
// which level and which policies (docs/prds/orgs-and-access.md, "Denials").
type Denial struct {
	Principal  identity.IdentityID `json:"principal"`
	Org        org.OrgID           `json:"org"`
	Action     Action              `json:"action"`
	Kind       ResourceKind        `json:"kind"`
	ResourceID string              `json:"resource"`
	Level      Level               `json:"level"`
	Policies   []string            `json:"policies"`
	At         time.Time           `json:"at"`
	// Count is how many identical denials this record stands for. The store
	// coalesces identical denials within a minute into one row with a count.
	Count int64 `json:"count"`
}
