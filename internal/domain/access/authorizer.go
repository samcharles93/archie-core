// The contracts callers depend on: the Authorizer the dashboard/API path and
// dispatch call, the Validator policy changes are checked against, and the
// policy source the engine is built from.
package access

import "context"

// Authorizer evaluates the policy chain for one request. Exactly two places
// call it (docs/prds/orgs-and-access.md, "Where it lives"): the dashboard and
// API request path, and dispatch. The implementation is the Cedar engine in
// internal/infrastructure/access.
//
// Authorize never fails open: a stored policy the engine cannot parse makes
// its level deny everything and the decision carries the error; an invalid
// instance policy is a boot failure, not a denial the request path sees.
type Authorizer interface {
	// Authorize evaluates instance, org, workspace and object policies for
	// the request. A request is allowed only if every level with policies
	// for it permits it; a forbid at any level wins; with no permit the
	// request is denied.
	Authorize(p Principal, a Action, r Resource, c Context) Decision
}

// Validator checks one policy against the current schema: Cedar syntax plus
// the entity vocabulary this package defines. A policy naming an unknown
// entity type, action or attribute is refused. Every stored policy is
// checked again when archie starts (the engine's construction reports the
// invalid ones).
type Validator interface {
	Validate(p Policy) error
}

// PolicySource hands the engine its snapshot. The State Store owns the
// stored policies; a process loads them once at boot (a policy change takes
// effect when the process reloads, which the policy-editing surface ships
// with its own refresh).
type PolicySource interface {
	Policies(ctx context.Context) ([]Policy, error)
}
