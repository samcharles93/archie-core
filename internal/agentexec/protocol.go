// Package agentexec defines the unprivileged agent execution boundary.
// Workflow orchestration and external side effects remain daemon-owned.
package agentexec

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
)

const ProtocolVersion = 1

// ErrBlocked is returned by ReviewResult when the daemon blocks agent output
// from reaching human channels.
var ErrBlocked = errors.New("agent output blocked by daemon review")

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
	Workflow     string        `json:"workflow,omitempty"`
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

// Provider describes how the worker resolves a model provider. API keys are
// referenced by environment variable name and never serialized.
type Provider struct {
	Class     string `json:"class"`
	APIKeyEnv string `json:"api_key_env,omitempty"`
	BaseURL   string `json:"base_url,omitempty"`
}

func (p Provider) validate() error {
	if strings.TrimSpace(p.Class) == "" {
		return fmt.Errorf("provider class is required")
	}
	if p.BaseURL == "" {
		return nil
	}
	u, err := url.Parse(p.BaseURL)
	if err != nil {
		return fmt.Errorf("parse provider base_url: %w", err)
	}
	if u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return fmt.Errorf("provider base_url must not contain userinfo, query parameters, or a fragment")
	}
	return nil
}

// Invocation is the single request accepted by archie-agent on stdin.
type Invocation struct {
	Version   int                 `json:"version"`
	Workspace string              `json:"workspace"`
	Request   Request             `json:"request"`
	Providers map[string]Provider `json:"providers"`
}

// Validate rejects invocations that cannot be executed safely.
func (i Invocation) Validate() error {
	if i.Version != ProtocolVersion {
		return fmt.Errorf("agent invocation protocol version %d is unsupported (want %d)", i.Version, ProtocolVersion)
	}
	if i.Workspace == "" {
		return fmt.Errorf("agent invocation workspace is required")
	}
	if len(i.Providers) == 0 {
		return fmt.Errorf("agent invocation providers are required")
	}
	for name, provider := range i.Providers {
		if err := provider.validate(); err != nil {
			return fmt.Errorf("agent invocation provider %q: %w", name, err)
		}
	}
	return i.Request.Validate()
}

// Response is the single envelope written by archie-agent on stdout.
// Error carries execution errors after the worker successfully decoded the
// invocation; process-level protocol failures use a non-zero exit status.
type Response struct {
	Version int    `json:"version"`
	Result  Result `json:"result"`
	Error   string `json:"error,omitempty"`
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
