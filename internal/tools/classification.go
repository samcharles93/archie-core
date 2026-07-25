package tools

// ToolClassification is a bitmask that describes a tool's behavioral
// characteristics for dispatch gating, guardrail decisions, and budget
// enforcement.
type ToolClassification int

const (
	// ClassIdempotent marks a read-only tool. Idempotent tools can be
	// safely retried and run in parallel with other idempotent tools.
	ClassIdempotent ToolClassification = 1 << iota

	// ClassMutating marks a tool that modifies external state (writes files,
	// creates resources, sends messages, etc.). Mutating tools require
	// serialization, checkpointing, and stricter guardrail monitoring.
	ClassMutating
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

// String returns a human-readable representation of the classification.
func (c ToolClassification) String() string {
	switch {
	case c == 0:
		return "default"
	case c.IsIdempotent() && c.IsMutating():
		return "idempotent|mutating"
	case c.IsIdempotent():
		return "idempotent"
	case c.IsMutating():
		return "mutating"
	default:
		return "unknown"
	}
}
