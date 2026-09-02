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
// dangerousAction / pendingApproval shape: armed is the only state that
// evaluates against incoming events; any edit drops armed back to
// pending_approval; only an explicit Approve call moves
// pending_approval -> armed.
type Status string

const (
	StatusDraft           Status = "draft"
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
// daemon runs.
//
// Version is bumped on every UpdateBinding so a later edit cannot
// silently rewrite the historical provenance of a task that already
// fired. Secret is the HMAC-SHA256 shared secret a sender must sign with
// for an event to be marked authenticated; it is never returned by
// GET handlers.
type Binding struct {
	ID        int64   `json:"id"`
	Name      string  `json:"name"`
	Matcher   Matcher `json:"matcher"`
	MappingID int64   `json:"mapping_id"`
	Workflow  string  `json:"workflow"`
	// Owner and Repo pin a binding to a specific configured repo, so a
	// multi-repo deployment can dispatch correctly. Both empty means "no
	// pin" -- resolveBindingRepo falls back to the single-configured-repo
	// behaviour that predates this field. Setting only one is invalid
	// (Validate rejects it): a pin is a complete owner/repo pair or not a
	// pin at all, never a half-guess.
	Owner     string    `json:"owner,omitempty"`
	Repo      string    `json:"repo,omitempty"`
	Version   int       `json:"version"`
	Status    Status    `json:"status"`
	Secret    string    `json:"secret,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// minSecretLen is the floor for an acceptable shared secret. A trivially
// short secret makes accidental binding to a known HMAC trivial; the
// floor is chosen so a one-character typo cannot arm.
const minSecretLen = 16

// Validate checks a binding is well-formed before it is newly persisted.
// A non-empty Name, a non-empty Matcher.Source, a positive MappingID, a
// non-empty Workflow, and a sufficiently long Secret are all required.
// Workflow existence is checked at the API/store layer because the domain
// package does not import the workflow registry. Use ValidateForUpdate
// instead when editing an existing binding, where an empty Secret is a
// meaningful "keep the current one," not a missing value.
func (b Binding) Validate() error {
	if err := b.validateCommon(); err != nil {
		return err
	}
	if len(b.Secret) < minSecretLen {
		return fmt.Errorf("binding: secret must be at least %d bytes", minSecretLen)
	}
	return nil
}

// ValidateForUpdate is Validate for the edit path: every other field is
// checked identically, but Secret is only length-checked when the caller
// actually supplied one. UpdateBinding's own store-layer contract already
// treats an empty Secret as "preserve the existing one" (COALESCE against
// NULLIF) -- rejecting that here at the validation gate before the store
// ever sees it would make the store's own documented behavior
// unreachable. A non-empty Secret (a genuine change) is still held to the
// same floor Validate enforces.
func (b Binding) ValidateForUpdate() error {
	if err := b.validateCommon(); err != nil {
		return err
	}
	if b.Secret != "" && len(b.Secret) < minSecretLen {
		return fmt.Errorf("binding: secret must be at least %d bytes", minSecretLen)
	}
	return nil
}

func (b Binding) validateCommon() error {
	if strings.TrimSpace(b.Name) == "" {
		return fmt.Errorf("binding: name is required")
	}
	if strings.TrimSpace(b.Matcher.Source) == "" {
		return fmt.Errorf("binding: matcher.source is required")
	}
	if b.MappingID <= 0 {
		return fmt.Errorf("binding: mapping_id must be positive")
	}
	if strings.TrimSpace(b.Workflow) == "" {
		return fmt.Errorf("binding: workflow is required")
	}
	if (b.Owner == "") != (b.Repo == "") {
		return fmt.Errorf("binding: owner and repo must both be set or both be empty")
	}
	return nil
}

// Matches reports whether the given captured event would match this
// binding's matcher. The Authenticated clause encodes t2db.5 point 1 --
// an unauthenticated event can never trigger a binding, no matter how
// well it would otherwise match.
//
// Note: the caller is expected to be the dispatch loop or handleCapture,
// both of which receive a CapturedEvent with the Authenticated flag
// already set by HMAC verification. Matches is pure and has no I/O.
func (m Matcher) Matches(source string, authenticated bool) bool {
	if !authenticated {
		return false
	}
	return m.Source == source
}

// Overlaps reports whether two matchers cover any plausible event
// together. With a single-string matcher this collapses to: both have
// a non-empty Source equal to each other. Two empty matchers would
// overlap trivially; Validate rejects empty Sources upstream so this
// case cannot arise in practice.
func (m Matcher) Overlaps(other Matcher) bool {
	if m.Source == "" || other.Source == "" {
		return false
	}
	return m.Source == other.Source
}

// Normalize clamps an arbitrary string into a recognised Status, falling
// back to StatusDraft for any unknown value. The DB CHECK constraint
// already rejects bad strings at write time; Normalize is the API-edge
// guard for inputs that arrive before they ever reach the store.
func Normalize(s Status) Status {
	switch s {
	case StatusDraft, StatusPendingApproval, StatusArmed:
		return s
	default:
		return StatusDraft
	}
}
