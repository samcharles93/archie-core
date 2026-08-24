// Package agentexec defines worker-local workflow-stage execution. archie-agent
// owns workflow orchestration; authority-bearing forge, store, and push effects
// remain daemon-owned behind scoped RPC boundaries.
package agentexec

import (
	"encoding/json"
	"errors"
	"fmt"
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
	Version       int           `json:"version"`
	TaskID        int64         `json:"task_id"`
	Attempt       int           `json:"attempt"`
	Stage         string        `json:"stage"`
	Workflow      string        `json:"workflow,omitempty"`
	Model         string        `json:"model"`
	ContextWindow int           `json:"context_window,omitempty"`
	Mission       string        `json:"mission"`
	ExtraRules    string        `json:"extra_rules,omitempty"`
	ReadOnly      bool          `json:"read_only,omitempty"`
	Budget        Budget        `json:"budget"`
	Gate          Gate          `json:"gate"`
	Preflight     []Command     `json:"preflight,omitempty"`
	Protection    Protection    `json:"protection"`
	Notes         string        `json:"notes,omitempty"`
	CaptureTools  []CaptureTool `json:"capture_tools,omitempty"`
	// Plugins are bundled Yaegi plugins from the skill's plugins/
	// directory. Each entry carries the name and source so the agent
	// can register them as tools. PRD section 5 Layer 1.
	Plugins []PluginSpec `json:"plugins,omitempty"`
}

// PluginSpec is a bundled Yaegi plugin passed from daemon to agent.
type PluginSpec struct {
	Name string `json:"name"`
	Src  string `json:"src"`
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
	Usage         Usage                        `json:"usage"`
	Summary       string                       `json:"summary,omitempty"`
	Detail        string                       `json:"detail,omitempty"`
	AppendedNotes []string                     `json:"appended_notes,omitempty"`
	Captures      map[string][]json.RawMessage `json:"captures,omitempty"`
}

// Usage preserves provider-reported token economics for evaluation. Total is
// retained separately as TokensUsed for compatibility with stored task rows.
type Usage struct {
	PromptTokens        int `json:"prompt_tokens,omitempty"`
	CompletionTokens    int `json:"completion_tokens,omitempty"`
	TotalTokens         int `json:"total_tokens,omitempty"`
	CachedTokens        int `json:"cached_tokens,omitempty"`
	CacheCreationTokens int `json:"cache_creation_tokens,omitempty"`
}

// Provider describes how the worker resolves a model provider. API keys are
// referenced by environment variable name and never serialized.
type Provider struct {
	Class     string `json:"class"`
	APIKeyEnv string `json:"api_key_env,omitempty"`
	BaseURL   string `json:"base_url,omitempty"`
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
