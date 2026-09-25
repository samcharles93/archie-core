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
	// RequiredWhenTrue makes fields required only when a boolean field in
	// the same call is true, keyed by that boolean's name. It exists because
	// a flat RequiredFields entry cannot express "name a workflow, but only
	// when you have said one is needed": the alternatives were forcing a
	// meaningless answer on the other branch, or accepting the omission and
	// defaulting it, which lets the model skip the decision entirely.
	// A named field must also be a non-empty string when the trigger is true.
	RequiredWhenTrue map[string][]string `json:"required_when_true,omitempty"`
	MaxCalls         int                 `json:"max_calls,omitempty"`
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
	// Harness, when set, runs the stage on an external coding-agent CLI
	// instead of the built-in loop. The worker in a Kit container is the
	// daemon's own archie-agent build, so it always understands this field.
	Harness *HarnessSpec `json:"harness,omitempty"`
}

// HarnessSpec is how to drive a Kit's agent headlessly: its launch argv
// (image entrypoint and cmd) and the agent-sessions@1 verb tails appended
// to it. Prompt must carry {{.Prompt}}; Resume, when set, {{.SessionID}}.
type HarnessSpec struct {
	// User is the Kit's user, a name or "uid[:gid]", that every invocation
	// runs as. It is never root: the worker's credentials live in the same
	// container, readable only by the worker's user.
	User string `json:"user,omitempty"`
	// Env is the invocation's entire environment. Nothing is inherited from
	// the worker, whose environment carries its credentials.
	Env []string `json:"env,omitempty"`
	// Adapter names the output adapter reading the CLI's stream; empty is
	// a CLI with none.
	Adapter  string   `json:"adapter,omitempty"`
	Launch   []string `json:"launch"`
	Prompt   []string `json:"prompt"`
	Resume   []string `json:"resume,omitempty"`
	Continue []string `json:"continue,omitempty"`
	// MCPConfig registers archie's MCP server with the CLI: an argv tail
	// carrying {{.MCPConfig}}, the path of a standard mcpServers config.
	// A stage with capture tools needs it.
	MCPConfig []string `json:"mcp_config,omitempty"`
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
