// Package access implements internal/domain/access's Authorizer with
// github.com/cedar-policy/cedar-go. The engine owns policy parsing and
// validation against the shipped Cedar schema, and the chain evaluation:
// instance ▶ org ▶ workspace ▶ object, where a level with policies must
// permit, a forbid at any level wins, and with no permit the request is
// denied (docs/prds/orgs-and-access.md).
//
// The cross-org forbid is enforced here, structurally, before any Cedar
// runs: a principal never reaches a resource outside its org. It is not a
// stored policy, so it cannot be edited away or removed by
// `archied access reset`.
package access

import (
	"fmt"

	cedar "github.com/cedar-policy/cedar-go"
	"github.com/cedar-policy/cedar-go/types"
	expast "github.com/cedar-policy/cedar-go/x/exp/ast"
	schema "github.com/cedar-policy/cedar-go/x/exp/schema"
	validate "github.com/cedar-policy/cedar-go/x/exp/schema/validate"

	"github.com/samcharles93/archie-core/internal/domain/access"
	"github.com/samcharles93/archie-core/internal/domain/org"
)

// schemaText is the Cedar schema the engine validates every policy against.
// It names the entity vocabulary internal/domain/access defines: an
// Identity principal carrying its org and effective role, the org and
// workspace parents, one Object entity per record, and one Action per
// action. Context carries the event's signature result, the address it came
// from, and the run and step of an agent's request.
const schemaText = `namespace Archie {
  entity Identity in [Org] = {"org": String, "role": String};
  entity Org;
  entity Workspace in [Org] = {"org": String};
  entity Object in [Org, Workspace] = {"org": String, "workspace": String, "kind": String, "owner": String, "state": String};
  action read appliesTo { principal: Identity, resource: Object, context: {signature: String, addr: ipaddr, time: datetime, run: String, step: String} };
  action create appliesTo { principal: Identity, resource: Object, context: {signature: String, addr: ipaddr, time: datetime, run: String, step: String} };
  action update appliesTo { principal: Identity, resource: Object, context: {signature: String, addr: ipaddr, time: datetime, run: String, step: String} };
  action delete appliesTo { principal: Identity, resource: Object, context: {signature: String, addr: ipaddr, time: datetime, run: String, step: String} };
  action approve appliesTo { principal: Identity, resource: Object, context: {signature: String, addr: ipaddr, time: datetime, run: String, step: String} };
  action run appliesTo { principal: Identity, resource: Object, context: {signature: String, addr: ipaddr, time: datetime, run: String, step: String} };
  action read_logs appliesTo { principal: Identity, resource: Object, context: {signature: String, addr: ipaddr, time: datetime, run: String, step: String} };
  action read_secret appliesTo { principal: Identity, resource: Object, context: {signature: String, addr: ipaddr, time: datetime, run: String, step: String} };
  action manage_members appliesTo { principal: Identity, resource: Object, context: {signature: String, addr: ipaddr, time: datetime, run: String, step: String} };
  action manage_identities appliesTo { principal: Identity, resource: Object, context: {signature: String, addr: ipaddr, time: datetime, run: String, step: String} };
  action manage_policies appliesTo { principal: Identity, resource: Object, context: {signature: String, addr: ipaddr, time: datetime, run: String, step: String} };
}`

// Problem is one stored policy the engine could not validate. It is what a
// health issue names: the policy and the error
// (docs/prds/orgs-and-access.md, "Storing and changing policies").
type Problem struct {
	Policy access.Policy
	Err    error
}

// levelSet is one chain level's parsed policies. invalid is non-nil when
// any of the level's policies failed to compile: the level then refuses
// every request and none of its policies is evaluated.
type levelSet struct {
	at      access.Level
	ids     []string
	set     *cedar.PolicySet
	invalid error
}

// active reports whether the level carries policies at all: a level whose
// policies failed to compile is active too, because it refuses everything.
func (l *levelSet) active() bool { return l != nil && (len(l.ids) > 0 || l.invalid != nil) }

// Engine evaluates the chain. It is immutable after construction: a policy
// change rebuilds it from a fresh snapshot of the store.
type Engine struct {
	instance *levelSet
	orgs     map[org.OrgID]*levelSet
	spaces   map[spaceKey]*levelSet
	objects  map[objectKey]*levelSet
	problems []Problem
}

type (
	spaceKey struct {
		org org.OrgID
		ws  org.WorkspaceID
	}
	objectKey struct {
		org  org.OrgID
		ws   org.WorkspaceID
		kind access.ResourceKind
		id   string
	}
)

// The engine satisfies the domain contracts.
var (
	_ access.Authorizer = (*Engine)(nil)
	_ access.Validator  = (*Engine)(nil)
)

// New builds an engine over one snapshot of the stored policies. An invalid
// instance policy is a boot failure: it returns (nil, err) and archie stops
// serving until the policy is fixed. An invalid org, workspace or object
// policy becomes a Problem and makes its level deny everything -- the other
// levels still serve, and the health surface reports the problem by name.
func New(policies []access.Policy) (*Engine, error) {
	e := &Engine{
		orgs:    map[org.OrgID]*levelSet{},
		spaces:  map[spaceKey]*levelSet{},
		objects: map[objectKey]*levelSet{},
	}
	for _, p := range policies {
		lvl := e.level(p)
		policy, err := compile(p)
		if err != nil {
			if p.Level == access.LevelInstance {
				return nil, fmt.Errorf("access: instance policy %q: %w", p.ID, err)
			}
			// The refusal must name the policy that poisoned the level.
			lvl.ids = append(lvl.ids, p.ID)
			lvl.invalid = err
			e.problems = append(e.problems, Problem{Policy: p, Err: err})
			continue
		}
		lvl.ids = append(lvl.ids, p.ID)
		lvl.set.Add(types.PolicyID(p.ID), policy)
	}
	return e, nil
}

// Problems reports every stored policy the engine could not compile. Empty
// when every stored policy is valid.
func (e *Engine) Problems() []Problem { return e.problems }

// key locates the level set the policy belongs to, creating it on first use.
func (e *Engine) level(p access.Policy) *levelSet {
	switch p.Level {
	case access.LevelInstance:
		if e.instance == nil {
			e.instance = &levelSet{at: p.Level, set: cedar.NewPolicySet()}
		}
		return e.instance
	case access.LevelOrg:
		id := p.OrgID
		if lvl, ok := e.orgs[id]; ok {
			return lvl
		}
		lvl := &levelSet{at: p.Level, set: cedar.NewPolicySet()}
		e.orgs[id] = lvl
		return lvl
	case access.LevelWorkspace:
		id := spaceKey{p.OrgID, p.WorkspaceID}
		if lvl, ok := e.spaces[id]; ok {
			return lvl
		}
		lvl := &levelSet{at: p.Level, set: cedar.NewPolicySet()}
		e.spaces[id] = lvl
		return lvl
	case access.LevelObject:
		id := objectKey{p.OrgID, p.WorkspaceID, p.ObjectKind, p.ObjectID}
		if lvl, ok := e.objects[id]; ok {
			return lvl
		}
		lvl := &levelSet{at: p.Level, set: cedar.NewPolicySet()}
		e.objects[id] = lvl
		return lvl
	}
	return nil
}

// Validate checks one policy against the schema: the save-time check the
// domain's Validator contract requires, and the re-check every start of
// archie performs by rebuilding the engine from the store.
func (e *Engine) Validate(p access.Policy) error {
	_, err := compile(p)
	return err
}

// compile parses and validates one policy: structural shape, Cedar syntax,
// then the schema.
func compile(p access.Policy) (*cedar.Policy, error) {
	if err := p.Validate(); err != nil {
		return nil, err
	}
	pls, err := cedar.NewPolicyListFromBytes(p.ID, []byte(p.Text))
	if err != nil {
		return nil, fmt.Errorf("%w: %w", access.ErrPolicyInvalidText, err)
	}
	if len(pls) != 1 {
		return nil, fmt.Errorf("%w: %s carries %d policies, want exactly one", access.ErrPolicyInvalidText, p.ID, len(pls))
	}
	v, err := schemaValidator()
	if err != nil {
		return nil, err
	}
	if err := v.Policy(p.ID, (*expast.Policy)(pls[0].AST())); err != nil {
		return nil, fmt.Errorf("%w: %w", access.ErrPolicyInvalidText, err)
	}
	return pls[0], nil
}

// Authorize evaluates the chain for one request.
func (e *Engine) Authorize(p access.Principal, a access.Action, r access.Resource, c access.Context) access.Decision {
	// The cross-org forbid, enforced structurally: a principal's requests
	// only ever reach its own org. An unset resource org means the
	// principal's org (a create request names a record that does not exist
	// yet); an org-less principal is denied everything.
	resOrg := r.Org
	if resOrg == "" {
		resOrg = p.Org
	}
	if p.Org == "" || resOrg != p.Org {
		return access.DeniedAt(access.LevelInstance, []string{access.CrossOrgForbidID})
	}

	request := buildRequest(p, a, r, c)
	entities := buildEntities(p, r)

	for _, lvl := range e.chain(r) {
		if !lvl.active() {
			continue
		}
		if lvl.invalid != nil {
			decision := access.DeniedAt(lvl.at, lvl.ids)
			decision.Err = lvl.invalid
			return decision
		}
		decision, diag := cedar.Authorize(lvl.set, entities, request)
		if decision == cedar.Allow {
			continue
		}
		ids := make([]string, 0, len(diag.Reasons))
		for _, reason := range diag.Reasons {
			ids = append(ids, string(reason.PolicyID))
		}
		if len(ids) == 0 {
			// No permit at a level with policies: the level denied by
			// default, not through any one policy. Name the level's
			// policies so the denial record still says what stood between
			// the principal and the record.
			ids = lvl.ids
		}
		return access.DeniedAt(lvl.at, ids)
	}
	return access.Allowed()
}

// chain returns the level sets the request passes through, in order.
func (e *Engine) chain(r access.Resource) []*levelSet {
	out := []*levelSet{e.instance}
	if l, ok := e.orgs[r.Org]; ok {
		out = append(out, l)
	}
	if r.Workspace != "" {
		if l, ok := e.spaces[spaceKey{r.Org, r.Workspace}]; ok {
			out = append(out, l)
		}
	}
	if r.Org != "" && r.ID != "" {
		if l, ok := e.objects[objectKey{r.Org, r.Workspace, r.Kind, r.ID}]; ok {
			out = append(out, l)
		}
	}
	return out
}

// schemaValidator returns the resolved schema, building it once. The schema
// is part of the shipped vocabulary and cannot change at runtime.
func schemaValidator() (*validate.Validator, error) {
	var sch schema.Schema
	if err := sch.UnmarshalCedar([]byte(schemaText)); err != nil {
		return nil, err
	}
	resolved, err := sch.Resolve()
	if err != nil {
		return nil, err
	}
	return validate.New(resolved), nil
}
