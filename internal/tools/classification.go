package tools

import "strings"

// ToolClassification is a bitmask that describes a tool's behavioral
// characteristics for dispatch gating, guardrail decisions, and per-result
// limit enforcement.
type ToolClassification int

const (
	// ClassIdempotent marks a read-only tool. Idempotent tools can be
	// safely retried and run in parallel with other idempotent tools.
	ClassIdempotent ToolClassification = 1 << iota

	// ClassMutating marks a tool that modifies external state (writes files,
	// creates resources, sends messages, etc.). Mutating tools require
	// serialization, checkpointing, and stricter guardrail monitoring.
	ClassMutating

	// RequiresApproval marks a tool that must not execute without a human
	// consent decision. The dispatch layer blocks the call until an
	// ApprovalRequester resolves it (approve, permanently approve, or deny).
	//
	// It is independent of ClassMutating: writing a file is mutating but
	// not necessarily gated (the agent loop's own path enforcement handles
	// that), while deleting a session is gated because it is irreversible
	// on a path the model controls.
	RequiresApproval
)

// IsIdempotent reports whether the classification includes the idempotent
// flag. A zero-value classification reports false.
func (c ToolClassification) IsIdempotent() bool {
	return c&ClassIdempotent != 0
}

// IsMutating reports whether the classification includes the mutating flag.
// A zero-value classification reports false.
func (c ToolClassification) IsMutating() bool {
	return c&ClassMutating != 0
}

// IsApprovalRequired reports whether the classification includes the
// RequiresApproval flag. A zero-value classification reports false.
func (c ToolClassification) IsApprovalRequired() bool {
	return c&RequiresApproval != 0
}

// String returns a human-readable representation of the classification.
func (c ToolClassification) String() string {
	if c == 0 {
		return "default"
	}
	var parts []string
	if c.IsIdempotent() {
		parts = append(parts, "idempotent")
	}
	if c.IsMutating() {
		parts = append(parts, "mutating")
	}
	if c.IsApprovalRequired() {
		parts = append(parts, "requires_approval")
	}
	if len(parts) == 0 {
		return "unknown"
	}
	return strings.Join(parts, "|")
}
