// The contracts callers depend on: the Authorizer the dashboard/API path and
// dispatch call, the Validator policy changes are checked against, and the
// policy source the engine is built from.
package access

import "context"

// Authorizer evaluates the policy chain for a request. It never fails open:
// an unparsable stored policy denies its whole level.
type Authorizer interface {
	// Authorize evaluates instance, org, workspace and object policies for
	// the request. A request is allowed only if every level with policies
	// for it permits it; a forbid at any level wins; with no permit the
	// request is denied.
	Authorize(p Principal, a Action, r Resource, c Context) Decision
}

// Validator checks a policy's Cedar syntax and entity vocabulary.
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
