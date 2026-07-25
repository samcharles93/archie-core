package tools

import (
	"fmt"
	"sync"
)

// ---------------------------------------------------------------------------
// 17.10  --  ToolCallGuardrailConfig
// ---------------------------------------------------------------------------

// ToolCallGuardrailConfig defines threshold values for the guardrail
// decision engine. All thresholds are per-session unless otherwise noted.
type ToolCallGuardrailConfig struct {
	// ExactFailureWarnAfter is how many times the exact same tool+error
	// combination must occur before the engine issues a warning.
	ExactFailureWarnAfter int

	// SameToolFailureWarnAfter is how many consecutive failures from the
	// same tool (regardless of error) trigger a warning.
	SameToolFailureWarnAfter int

	// NoProgressWarnAfter is the number of tool calls without completing
	// the turn goal before the engine issues a warning.
	NoProgressWarnAfter int

	// HardStopAfterWarnRepeat is how many times the same warning category
	// must fire before the engine upgrades to a hard stop. Zero means
	// never hard-stop (warn only).
	HardStopAfterWarnRepeat int
}

// DefaultGuardrailConfig returns a [ToolCallGuardrailConfig] with sensible
// defaults: 3 exact-failure warnings, 5 same-tool-failure warnings,
// 10 no-progress warnings, and hard-stop after 2 warning repeats.
func DefaultGuardrailConfig() ToolCallGuardrailConfig {
	return ToolCallGuardrailConfig{
		ExactFailureWarnAfter:    3,
		SameToolFailureWarnAfter: 5,
		NoProgressWarnAfter:      10,
		HardStopAfterWarnRepeat:  2,
	}
}

// Validate reports whether the config values are within reasonable bounds.
// Negative values are rejected; zero values mean "never trigger" which is
// accepted.
func (c ToolCallGuardrailConfig) Validate() error {
	if c.ExactFailureWarnAfter < 0 {
		return fmt.Errorf("ExactFailureWarnAfter must be >= 0, got %d", c.ExactFailureWarnAfter)
	}
	if c.SameToolFailureWarnAfter < 0 {
		return fmt.Errorf("SameToolFailureWarnAfter must be >= 0, got %d", c.SameToolFailureWarnAfter)
	}
	if c.NoProgressWarnAfter < 0 {
		return fmt.Errorf("NoProgressWarnAfter must be >= 0, got %d", c.NoProgressWarnAfter)
	}
	if c.HardStopAfterWarnRepeat < 0 {
		return fmt.Errorf("HardStopAfterWarnRepeat must be >= 0, got %d", c.HardStopAfterWarnRepeat)
	}
	return nil
}

// ---------------------------------------------------------------------------
// 17.13  --  Guardrail decision engine
// ---------------------------------------------------------------------------

// GuardrailDecision represents the outcome of a guardrail evaluation.
type GuardrailDecision int

const (
	// DecisionAllow means the tool call should proceed normally.
	DecisionAllow GuardrailDecision = iota

	// DecisionWarn means a warning should be sent to the LLM but the
	// tool call may proceed.
	DecisionWarn

	// DecisionHardStop means the tool loop should abort immediately.
	DecisionHardStop
)

// String returns a human-readable representation of the decision.
func (d GuardrailDecision) String() string {
	switch d {
	case DecisionAllow:
		return "allow"
	case DecisionWarn:
		return "warn"
	case DecisionHardStop:
		return "hard-stop"
	default:
		return "unknown"
	}
}

// GuardrailEngine tracks per-session and per-turn tool call outcomes and
// evaluates them against a [ToolCallGuardrailConfig] to decide whether to
// allow, warn, or hard-stop.
//
// All methods are safe for concurrent use.
type GuardrailEngine struct {
	config ToolCallGuardrailConfig

	mu sync.Mutex

	// exactFailures maps "toolName:errorKey" → count.
	exactFailures map[string]int

	// sameToolConsecutiveFailures maps toolName → consecutive failure count.
	// Reset to 0 on any success for that tool.
	sameToolConsecutiveFailures map[string]int

	// totalCalls counts every tool invocation this session.
	totalCalls int

	// warnCounts maps warning category → how many times warned.
	warnCounts map[string]int

	// hardStopped is set to true when the engine issues a hard stop.
	// Once true, every subsequent evaluation returns DecisionHardStop.
	hardStopped bool
}

// NewGuardrailEngine creates a [GuardrailEngine] with the given config.
func NewGuardrailEngine(config ToolCallGuardrailConfig) *GuardrailEngine {
	return &GuardrailEngine{
		config:                      config,
		exactFailures:               make(map[string]int),
		sameToolConsecutiveFailures: make(map[string]int),
		warnCounts:                  make(map[string]int),
	}
}

// RecordSuccess records a successful tool invocation. It resets the
// consecutive-failure counter for the tool and checks whether the
// no-progress threshold has been exceeded, returning the appropriate
// guardrail decision (allow, warn, or hard-stop).
func (g *GuardrailEngine) RecordSuccess(toolName string) GuardrailDecision {
	g.mu.Lock()
	defer g.mu.Unlock()

	if g.hardStopped {
		return DecisionHardStop
	}

	g.totalCalls++
	delete(g.sameToolConsecutiveFailures, toolName)

	// Check no-progress threshold.
	if g.config.NoProgressWarnAfter > 0 && g.totalCalls >= g.config.NoProgressWarnAfter {
		return g.issueDecisionLocked("no_progress")
	}
	return DecisionAllow
}

// RecordFailure records a failed tool invocation. Returns the guardrail
// decision (allow, warn, or hard-stop).
func (g *GuardrailEngine) RecordFailure(toolName string, err error) GuardrailDecision {
	g.mu.Lock()
	defer g.mu.Unlock()

	if g.hardStopped {
		return DecisionHardStop
	}

	g.totalCalls++

	if err == nil {
		// Defensive: treat nil error as "unknown failure" to avoid a
		// nil-pointer dereference if a caller passes a nil error.
		return DecisionAllow
	}

	errKey := err.Error()

	// Track exact-failure count.
	exactKey := toolName + ":" + errKey
	g.exactFailures[exactKey]++

	// Track same-tool consecutive failures.
	g.sameToolConsecutiveFailures[toolName]++

	// Evaluate thresholds.
	return g.evaluateLocked(toolName, errKey)
}

// Evaluate checks whether the current state warrants a warning or hard-stop,
// without recording a new event. Used to check budget and no-progress
// thresholds.
func (g *GuardrailEngine) Evaluate(category string) GuardrailDecision {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.hardStopped {
		return DecisionHardStop
	}
	return g.evaluateLocked("", category)
}

// MarkProgress resets the no-progress counter. Call this when the turn
// makes meaningful forward progress (e.g. the LLM produces a final answer).
func (g *GuardrailEngine) MarkProgress() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.totalCalls = 0
}

// TotalCalls returns the total number of tool calls in the current session.
func (g *GuardrailEngine) TotalCalls() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.totalCalls
}

// HardStopped reports whether the engine has issued a hard stop.
func (g *GuardrailEngine) HardStopped() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.hardStopped
}

// Reset clears all counters. The hard-stop flag is cleared.
func (g *GuardrailEngine) Reset() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.exactFailures = make(map[string]int)
	g.sameToolConsecutiveFailures = make(map[string]int)
	g.totalCalls = 0
	g.warnCounts = make(map[string]int)
	g.hardStopped = false
}

// evaluateLocked runs the threshold logic. Must be called with g.mu held.
func (g *GuardrailEngine) evaluateLocked(toolName, category string) GuardrailDecision {
	c := g.config

	// 1. Check exact-failure threshold.
	if c.ExactFailureWarnAfter > 0 && toolName != "" {
		exactKey := toolName + ":" + category
		if g.exactFailures[exactKey] >= c.ExactFailureWarnAfter {
			return g.issueDecisionLocked("exact_failure")
		}
	}

	// 2. Check same-tool consecutive failure threshold.
	if c.SameToolFailureWarnAfter > 0 && toolName != "" {
		if g.sameToolConsecutiveFailures[toolName] >= c.SameToolFailureWarnAfter {
			return g.issueDecisionLocked("same_tool_failure")
		}
	}

	// 3. Check no-progress threshold.
	if c.NoProgressWarnAfter > 0 {
		if g.totalCalls >= c.NoProgressWarnAfter {
			return g.issueDecisionLocked("no_progress")
		}
	}

	return DecisionAllow
}

// issueDecisionLocked determines whether to warn or hard-stop based on
// repeat counts. Must be called with g.mu held.
func (g *GuardrailEngine) issueDecisionLocked(category string) GuardrailDecision {
	g.warnCounts[category]++

	if g.config.HardStopAfterWarnRepeat > 0 &&
		g.warnCounts[category] >= g.config.HardStopAfterWarnRepeat {
		g.hardStopped = true
		return DecisionHardStop
	}

	return DecisionWarn
}
