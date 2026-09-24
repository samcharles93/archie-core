// Package binding owns the playbook binding vocabulary: the matcher +
// mapping + workflow + state-machine entity that turns a captured webhook
// into an archie task. See docs/prds/webhook-intake-security.md and
// docs/prds/payload-field-mapping.md.
package binding

import (
	"fmt"
	"strings"
	"time"
)

// Status is the binding lifecycle state. Mirrors telegram's
// dangerousAction / pendingApproval shape: a binding is created
// pending_approval, armed is the only state that evaluates against incoming
// events, any edit drops armed back to pending_approval, and only an
// explicit Approve call moves pending_approval -> armed.
type Status string

const (
	StatusPendingApproval Status = "pending_approval"
	StatusArmed           Status = "armed"
)

// Matcher decides which captured events a binding applies to. Source is
// the only dimension today -- the path segment the sender POSTs to
// (e.g. "sentry"). Extend with additional predicates if a use case
// demands it; the storage column is plain TEXT so widening is
// non-breaking.
type Matcher struct {
	Source string `json:"source"`
}

// Binding is the runtime-editable entity that ties a matcher, a payload
// mapping, and a workflow together. Bindings live in the store, not in
// config.toml, so operators can author them from the dashboard while the
// daemon runs. Any number of bindings may share a source: each one applies to
// the event type its mapping belongs to.
//
// Version is bumped on every UpdateBinding so a later edit cannot
// silently rewrite the historical provenance of a task that already
// fired. Signing belongs to the source the matcher names, not to the
// binding (internal/domain/source).
type Binding struct {
	ID        string  `json:"id"`
	Name      string  `json:"name"`
	Matcher   Matcher `json:"matcher"`
	MappingID string  `json:"mapping_id"`
	// Filter is an optional CEL expression over the mapping's parameters
	// (CompileFilter). An event it excludes is not dispatched.
	Filter   string `json:"filter,omitempty"`
	Workflow string `json:"workflow"`
	// Owner and Repo pin a binding to a specific configured repo, so a
	// multi-repo deployment can dispatch correctly. Both empty means "no
	// pin" -- resolveBindingRepo falls back to the single-configured-repo
	// behaviour that predates this field. Setting only one is invalid
	// (Validate rejects it): a pin is a complete owner/repo pair or not a
	// pin at all, never a half-guess.
	Owner string `json:"owner,omitempty"`
	Repo  string `json:"repo,omitempty"`
	// RepoParam names the mapped parameter holding "owner/name" when the
	// repository comes from the event instead of a fixed Owner/Repo pin. The
	// named repository must still be a configured one.
	RepoParam string `json:"repo_param,omitempty"`
	// Inputs assigns the workflow's declared inputs, each from a mapped
	// parameter or a constant; CheckWorkflow checks them when the binding is
	// saved.
	Inputs    map[string]InputSource `json:"inputs,omitempty"`
	Version   int                    `json:"version"`
	Status    Status                 `json:"status"`
	CreatedAt time.Time              `json:"created_at"`
	UpdatedAt time.Time              `json:"updated_at"`
}

// Validate checks a binding is well-formed before it is persisted: a
// non-empty Name, Matcher.Source, MappingID and Workflow, and a complete
// owner/repo pin or none. Workflow existence is checked at the API/store
// layer because the domain package does not import the workflow registry.
func (b Binding) Validate() error {
	if strings.TrimSpace(b.Name) == "" {
		return fmt.Errorf("binding: name is required")
	}
	if strings.TrimSpace(b.Matcher.Source) == "" {
		return fmt.Errorf("binding: matcher.source is required")
	}
	if strings.TrimSpace(b.MappingID) == "" {
		return fmt.Errorf("binding: mapping_id is required")
	}
	if strings.TrimSpace(b.Workflow) == "" {
		return fmt.Errorf("binding: workflow is required")
	}
	if (b.Owner == "") != (b.Repo == "") {
		return fmt.Errorf("binding: owner and repo must both be set or both be empty")
	}
	if b.Owner != "" && b.RepoParam != "" {
		return fmt.Errorf("binding: a fixed owner/repo and a repository parameter cannot both be set")
	}
	for _, name := range sortedKeys(b.Inputs) {
		if err := b.Inputs[name].validate(name); err != nil {
			return err
		}
	}
	return nil
}

// Matches reports whether a captured event would trigger this binding. An
// event that is not dispatchable (no valid signature, and not from an
// approved unsigned source) never matches, however well it would otherwise.
// Matches is pure and has no I/O.
func (m Matcher) Matches(source string, dispatchable bool) bool {
	return dispatchable && m.Source == source
}
