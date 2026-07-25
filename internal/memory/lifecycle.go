package memory

// ── Lifecycle hook interfaces ─────────────────────────────────────────────
//
// Each hook is a separate sub-interface. Providers opt in by implementing
// the hooks they need. The MemoryManager discovers them via type assertion
// and calls them at the appropriate points in the agent lifecycle.
//
// All hooks are called asynchronously (fire-and-forget) to avoid blocking
// the main agent loop. Hook errors and panics are recovered — they must not
// interrupt the agent's operation.

// TurnStartHook is called at the beginning of each agent turn. Providers
// can use this to load fresh context, refresh caches, or prepare
// session-scoped state before the agent processes a new user message.
type TurnStartHook interface {
	// OnTurnStart is invoked when a new turn begins for the given session.
	OnTurnStart(sessionID string) error
}

// SessionEndHook is called when an agent session ends (normal termination
// or shutdown). Providers can use this to persist final state, flush
// writes, or archive session data.
type SessionEndHook interface {
	// OnSessionEnd is invoked when the session identified by sessionID ends.
	OnSessionEnd(sessionID string) error
}

// SessionSwitchHook is called when the agent switches to a different
// session context (e.g., a conversation fork or a user-initiated session
// change). Providers can use this to unload the old session's state and
// prepare for the new session.
type SessionSwitchHook interface {
	// OnSessionSwitch is invoked when the agent switches to a new session.
	// oldSessionID is the session being left; newSessionID is the session
	// being entered.
	OnSessionSwitch(oldSessionID, newSessionID string) error
}

// PreCompressHook is called just before the agent's context is compacted
// (summarized). Providers can use this to persist volatile context that
// would otherwise be lost, or to influence what gets compacted.
type PreCompressHook interface {
	// OnPreCompress is invoked before context compaction for the given
	// session. The provider should ensure any important state is persisted.
	OnPreCompress(sessionID string) error
}

// MemoryWriteHook is called whenever new content is written to memory.
// Providers can use this to update indexes, invalidate caches, or trigger
// downstream processing of the newly written content.
type MemoryWriteHook interface {
	// OnMemoryWrite is invoked after content has been written to memory.
	// sessionID identifies the session that performed the write.
	// content is the full text that was stored.
	OnMemoryWrite(sessionID string, content string) error
}

// DelegationHook is called when the agent delegates work to a sub-agent
// or another provider. Providers can use this to track delegation chains
// or set up context for the delegated agent.
type DelegationHook interface {
	// OnDelegation is invoked when a delegation occurs for the given session.
	OnDelegation(sessionID string) error
}
