// Package binding owns the playbook binding vocabulary: the matcher + mapping +
// workflow + state-machine entity that turns a captured webhook into an archie
// task.
package binding

import (
	"fmt"
	"strings"
	"time"

	"github.com/samcharles93/archie-core/internal/domain/org"
)

// Status is a binding's state. New bindings are pending_approval; only armed
// bindings dispatch; any edit returns to pending_approval; Approve arms. Pause
// stops an armed binding, and Resume returns it to pending_approval so it is
// approved again before it fires.
type Status string

const (
	StatusPendingApproval Status = "pending_approval"
	StatusArmed           Status = "armed"
	StatusPaused          Status = "paused"
)

// Matcher selects the captures a binding applies to, by source path segment.
type Matcher struct {
	Source string `json:"source"`
}

// Binding ties a source matcher, a payload mapping and a workflow together.
// Version increments on every update.
type Binding struct {
	ID        string  `json:"id"`
	Name      string  `json:"name"`
	Matcher   Matcher `json:"matcher"`
	MappingID string  `json:"mapping_id"`
	// Filter is an optional CEL expression over the mapping's parameters
	// (CompileFilter). An event it excludes is not dispatched.
	Filter   string `json:"filter,omitempty"`
	Workflow string `json:"workflow"`
	// Owner and Repo pin the binding to a repo. Both or neither must be set.
	Owner string `json:"owner,omitempty"`
	Repo  string `json:"repo,omitempty"`
	// RepoParam names the mapped parameter holding "owner/name" when the
	// repository comes from the event instead of a fixed Owner/Repo pin. The
	// named repository must still be a configured one.
	RepoParam string `json:"repo_param,omitempty"`
	// Inputs assigns the workflow's declared inputs, each from a mapped
	// parameter or a constant; CheckWorkflow checks them when the binding is
	// saved.
	Inputs map[string]InputSource `json:"inputs,omitempty"`
	// OrgID is the org that owns the binding; the store sets it and a
	// binding may target only a workflow that org has enabled.
	OrgID     org.OrgID `json:"org_id"`
	Version   int       `json:"version"`
	Status    Status    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
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
