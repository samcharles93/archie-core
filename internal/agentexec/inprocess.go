package agentexec

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"maps"
	"path/filepath"
	"strings"

	"github.com/samcharles93/ai-sdk/agentloop"
	"github.com/samcharles93/ai-sdk/core"
	"github.com/samcharles93/ai-sdk/runtime"

	"github.com/samcharles93/archie-core/internal/skillscript"
	"github.com/samcharles93/archie-core/internal/tools"
)

// Runner executes one autonomous stage against an already prepared workspace.
type Runner interface {
	Run(ctx context.Context, workspace string, req Request) (Result, error)
}

type loopFunc func(context.Context, agentloop.Config) (agentloop.Result, error)

// InProcessRunner preserves the current execution behavior behind the v1
// agent protocol. It is replaced by a subprocess or container runner without
// changing workflow orchestration.
type InProcessRunner struct {
	runtime *runtime.Runtime
	log     *slog.Logger
	run     loopFunc
	tools   *tools.Registry

	// Limits bounds the size of tool results fed back into the stage. The
	// zero value applies no limits.
	Limits ToolLimits
}

func NewInProcessRunner(
	rt *runtime.Runtime,
	log *slog.Logger,
	registries ...*tools.Registry,
) *InProcessRunner {
	runner := &InProcessRunner{runtime: rt, log: log, run: agentloop.Run}
	if len(registries) > 0 {
		runner.tools = registries[0]
	}
	return runner
}

// workspaceToolset names the central-registry toolset a task agent must not
// inherit. Those tools are rooted at the chat workspace, not the task's
// worktree, and they reach the filesystem through their own handlers -- so
// agentloop's ReadOnly and ProtectPaths, which gate only the tools agentloop
// registers itself, do not apply to them. A feasibility stage running
// ReadOnly could otherwise write, and a TDD fix stage could edit the very
// tests it is being graded against.
//
// This is an invariant of task execution rather than a deployment choice, so
// it is not configurable.
const workspaceToolset = "workspace"

func (r *InProcessRunner) Run(ctx context.Context, workspace string, req Request) (Result, error) {
	if err := req.Validate(); err != nil {
		return Result{}, err
	}
	if r.runtime == nil {
		return Result{}, fmt.Errorf("agent runtime is not configured")
	}
	if r.run == nil {
		return Result{}, fmt.Errorf("agent loop is not configured")
	}

	notes := &memoryNotes{initial: req.Notes}
	captures := make(map[string][]json.RawMessage)
	centralTools, err := BuildToolSet(r.tools, r.Limits.Options(workspaceToolset))
	if err != nil {
		return Result{}, fmt.Errorf("build central tool set: %w", err)
	}
	res, err := r.run(ctx, agentloop.Config{
		Runtime:      r.runtime,
		ModelRef:     req.Model,
		WorkDir:      workspace,
		Mission:      req.Mission,
		ExtraRules:   req.ExtraRules,
		Notes:        notes,
		Gate:         toAgentGate(req.Gate),
		Preflight:    toAgentCommands(req.Preflight),
		Budget:       agentloop.Budget(req.Budget),
		ReadOnly:     req.ReadOnly,
		ProtectPaths: protectionMatcher(req.Protection, req.ReadOnly),
		Extra: mergeToolSets(
			centralTools,
			captureToolSet(req.CaptureTools, captures),
			scriptToolSet(workspace),
		),
		Logger: r.logger(req),
	})
	result := Result{
		Version:       ProtocolVersion,
		TaskID:        req.TaskID,
		Attempt:       req.Attempt,
		Stage:         req.Stage,
		Status:        string(res.Status),
		StopReason:    res.StopReason,
		Changes:       res.Changes,
		Iterations:    res.Iterations,
		TokensUsed:    res.TokensUsed,
		Summary:       res.Summary,
		Detail:        res.Detail,
		AppendedNotes: notes.appended,
		Captures:      captures,
	}
	// agentloop may return a zero-valued status on early exit (model
	// connectivity failure, empty response, etc.). Normalise it to a
	// known value so protocol validation always passes.
	if result.Status == "" {
		result.Status = "blocked"
		result.Detail = "agent returned no status (possible model connectivity issue)"
	}
	if cause := context.Cause(ctx); cause != nil {
		return result, cause
	}
	return result, err
}

func (r *InProcessRunner) logger(req Request) *slog.Logger {
	if r.log == nil {
		return slog.New(slog.DiscardHandler)
	}
	return r.log.With("task", req.TaskID, "attempt", req.Attempt, "stage", req.Stage, "model", req.Model)
}

func toAgentGate(g Gate) agentloop.GateConfig {
	return agentloop.GateConfig{
		Commands:               toAgentCommands(g.Commands),
		MaxConsecutiveFailures: g.MaxConsecutiveFailures,
	}
}

func toAgentCommands(commands []Command) []agentloop.GateCommand {
	out := make([]agentloop.GateCommand, 0, len(commands))
	for _, command := range commands {
		out = append(out, agentloop.GateCommand{
			Name: command.Name, Argv: command.Argv, ExpectFailure: command.ExpectFailure,
		})
	}
	return out
}

func protectionMatcher(p Protection, readOnly bool) func(string) bool {
	if readOnly || len(p.Suffixes)+len(p.Globs) == 0 {
		return nil
	}
	return func(path string) bool {
		for _, suffix := range p.Suffixes {
			if strings.HasSuffix(path, suffix) {
				return true
			}
		}
		for _, glob := range p.Globs {
			matchPath := path
			if !strings.ContainsAny(glob, `/\`) {
				matchPath = filepath.Base(path)
			}
			if matched, err := filepath.Match(glob, matchPath); err == nil && matched {
				return true
			}
		}
		return false
	}
}

func captureToolSet(specs []CaptureTool, captures map[string][]json.RawMessage) core.ToolSet {
	tools := make(core.ToolSet, len(specs))
	for _, spec := range specs {
		tools[spec.Name] = core.NewTool(spec.Name, spec.Description, spec.Parameters,
			makeCaptureHandler(spec, captures))
	}
	return tools
}

// makeCaptureHandler builds a tool handler that validates and records
// capture-tool invocations.
func makeCaptureHandler(spec CaptureTool, captures map[string][]json.RawMessage) func(context.Context, string) (string, error) {
	return func(_ context.Context, input string) (string, error) {
		if spec.MaxCalls > 0 && len(captures[spec.Name]) >= spec.MaxCalls {
			return fmt.Sprintf("%s rejected: maximum call count is %d", spec.Name, spec.MaxCalls), nil
		}
		value := json.RawMessage(input)
		if !json.Valid(value) {
			return spec.Name + " rejected: arguments are not valid JSON", nil
		}
		rejection, ok := validateCaptureArgs(spec, value)
		if !ok {
			return rejection, nil
		}
		captures[spec.Name] = append(captures[spec.Name], append(json.RawMessage(nil), value...))
		return spec.Name + " recorded", nil
	}
}

// validateCaptureArgs checks that the capture tool arguments satisfy all
// constraints. Returns a rejection message and false on failure, or "" and
// true on success.
func validateCaptureArgs(spec CaptureTool, value json.RawMessage) (string, bool) {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(value, &object); err != nil {
		return spec.Name + " rejected: arguments must be a JSON object", false //nolint:nilerr // the agent loop must see malformed tool arguments as feedback it can correct, not as a failed tool call
	}
	for _, field := range spec.RequiredFields {
		if _, ok := object[field]; !ok {
			return fmt.Sprintf("%s rejected: %s is required", spec.Name, field), false
		}
	}
	for _, field := range spec.NonEmptyStrings {
		var text string
		if raw, ok := object[field]; !ok || json.Unmarshal(raw, &text) != nil || strings.TrimSpace(text) == "" {
			return fmt.Sprintf("%s rejected: %s must be a non-empty string", spec.Name, field), false //nolint:nilerr // same: a rejection message lets the model retry with a valid argument
		}
	}
	for _, field := range spec.BooleanFields {
		var val bool
		if raw, ok := object[field]; !ok || json.Unmarshal(raw, &val) != nil {
			return fmt.Sprintf("%s rejected: %s must be a boolean", spec.Name, field), false //nolint:nilerr // same: a rejection message lets the model retry with a valid argument
		}
	}
	return "", true
}

// scriptToolSet exposes run_go_script: interpreting a Yaegi Go script
// bundled with a skill (.agents/skills/<skill>/scripts/*.go) or living
// anywhere else in the workspace, and returning what it printed. This is
// how an agent following a skill's instructions ("run scripts/gitleaks.go
// to scan for secrets") actually executes a .go helper without a Go
// toolchain in the sandbox.
func scriptToolSet(workspace string) core.ToolSet {
	const params = `{"type":"object","properties":{"path":{"type":"string","description":"Path to the .go script, relative to the workspace root."}},"required":["path"]}`
	return core.ToolSet{
		"run_go_script": core.NewTool(
			"run_go_script",
			"Run a Yaegi-interpreted Go script (e.g. a skill's bundled scripts/*.go helper) and return everything it printed.",
			json.RawMessage(params),
			func(ctx context.Context, input string) (string, error) {
				var args struct {
					Path string `json:"path"`
				}
				if err := json.Unmarshal([]byte(input), &args); err != nil || strings.TrimSpace(args.Path) == "" {
					return "run_go_script rejected: arguments must be a JSON object with a non-empty path field", nil //nolint:nilerr // same: a rejection message lets the model retry with a valid path
				}
				full := filepath.Join(workspace, args.Path)
				if rel, err := filepath.Rel(workspace, full); err != nil || strings.HasPrefix(rel, "..") {
					return "run_go_script rejected: path escapes the workspace", nil //nolint:nilerr // same: a rejection message lets the model retry with a valid path
				}
				out, err := skillscript.RunContext(ctx, full)
				if err != nil {
					return fmt.Sprintf("run_go_script failed: %v\n%s", err, out), nil
				}
				return out, nil
			},
		),
	}
}

// mergeToolSets combines tool sets into one; later sets win on name
// collisions.
func mergeToolSets(sets ...core.ToolSet) core.ToolSet {
	merged := core.ToolSet{}
	for _, set := range sets {
		maps.Copy(merged, set)
	}
	return merged
}

type memoryNotes struct {
	initial  string
	appended []string
}

func (n *memoryNotes) Load(context.Context) (string, error) { return n.initial, nil }

func (n *memoryNotes) Append(_ context.Context, entry string) error {
	n.appended = append(n.appended, entry)
	return nil
}
