// Package agentexec defines the unprivileged agent execution boundary.
// Workflow orchestration and external side effects remain daemon-owned.
package agentexec

import (
	"encoding/json"
	"fmt"
	"time"
)

const ProtocolVersion = 1

const StatusPassed = "passed"

// Command is one executable gate or preflight command.
type Command struct {
	Name          string   `json:"name"`
	Argv          []string `json:"argv"`
	ExpectFailure bool     `json:"expect_failure,omitempty"`
}

// Gate is the quality gate an agent must satisfy.
type Gate struct {
	Commands               []Command `json:"commands,omitempty"`
	MaxConsecutiveFailures int       `json:"max_consecutive_failures,omitempty"`
}

// Budget bounds one autonomous execution.
type Budget struct {
	MaxSteps  int           `json:"max_steps,omitempty"`
	MaxTokens int           `json:"max_tokens,omitempty"`
	WallClock time.Duration `json:"wall_clock,omitempty"`
}

// Protection describes paths the agent may read but not mutate.
type Protection struct {
	Suffixes []string `json:"suffixes,omitempty"`
	Globs    []string `json:"globs,omitempty"`
}

// CaptureTool declares a structured-output tool whose calls are returned
// to the daemon as data. It cannot call back into daemon state.
type CaptureTool struct {
	Name            string          `json:"name"`
	Description     string          `json:"description,omitempty"`
	Parameters      json.RawMessage `json:"parameters,omitempty"`
	RequiredFields  []string        `json:"required_fields,omitempty"`
	NonEmptyStrings []string        `json:"non_empty_strings,omitempty"`
	BooleanFields   []string        `json:"boolean_fields,omitempty"`
	MaxCalls        int             `json:"max_calls,omitempty"`
}

// Request is the complete, serializable input for one agent stage. The
// workspace path is supplied separately by Runner so a container runner can
// map a host path to a fixed path without exposing host layout in the wire
// protocol.
type Request struct {
	Version      int           `json:"version"`
	TaskID       int64         `json:"task_id"`
	Attempt      int           `json:"attempt"`
	Stage        string        `json:"stage"`
	Model        string        `json:"model"`
	Mission      string        `json:"mission"`
	ExtraRules   string        `json:"extra_rules,omitempty"`
	ReadOnly     bool          `json:"read_only,omitempty"`
	Budget       Budget        `json:"budget"`
	Gate         Gate          `json:"gate"`
	Preflight    []Command     `json:"preflight,omitempty"`
	Protection   Protection    `json:"protection"`
	Notes        string        `json:"notes,omitempty"`
	CaptureTools []CaptureTool `json:"capture_tools,omitempty"`
}

// Validate rejects requests that this runner cannot safely interpret.
func (r Request) Validate() error {
	if r.Version != ProtocolVersion {
		return fmt.Errorf("agent protocol version %d is unsupported (want %d)", r.Version, ProtocolVersion)
	}
	if r.TaskID <= 0 {
		return fmt.Errorf("agent request task_id must be positive")
	}
	if r.Attempt <= 0 {
		return fmt.Errorf("agent request attempt must be positive")
	}
	if r.Stage == "" {
		return fmt.Errorf("agent request stage is required")
	}
	if r.Model == "" {
		return fmt.Errorf("agent request model is required")
	}
	return nil
}

// Result is the serializable outcome of one agent stage.
type Result struct {
	Version       int                          `json:"version"`
	TaskID        int64                        `json:"task_id"`
	Attempt       int                          `json:"attempt"`
	Stage         string                       `json:"stage"`
	Status        string                       `json:"status"`
	StopReason    string                       `json:"stop_reason,omitempty"`
	Changes       []string                     `json:"changes,omitempty"`
	Iterations    int                          `json:"iterations,omitempty"`
	TokensUsed    int                          `json:"tokens_used,omitempty"`
	Summary       string                       `json:"summary,omitempty"`
	Detail        string                       `json:"detail,omitempty"`
	AppendedNotes []string                     `json:"appended_notes,omitempty"`
	Captures      map[string][]json.RawMessage `json:"captures,omitempty"`
}

// ValidateFor rejects incompatible or misrouted results before the daemon
// applies them to task state.
func (r Result) ValidateFor(req Request) error {
	if r.Version != ProtocolVersion {
		return fmt.Errorf("agent result protocol version %d is unsupported (want %d)", r.Version, ProtocolVersion)
	}
	if r.TaskID != req.TaskID || r.Attempt != req.Attempt || r.Stage != req.Stage {
		return fmt.Errorf(
			"agent result identity %d/%d/%s does not match request %d/%d/%s",
			r.TaskID, r.Attempt, r.Stage, req.TaskID, req.Attempt, req.Stage,
		)
	}
	if r.Status == "" {
		return fmt.Errorf("agent result status is required")
	}
	return nil
}
