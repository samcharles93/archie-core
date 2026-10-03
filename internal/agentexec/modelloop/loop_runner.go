package modelloop

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"maps"
	"path/filepath"
	"slices"
	"strings"

	"github.com/samcharles93/archie-core/internal/agentexec"
	"github.com/samcharles93/archie-core/internal/domain/agentrun"

	"github.com/samcharles93/ai-sdk/agentloop"
	"github.com/samcharles93/ai-sdk/core"
	"github.com/samcharles93/ai-sdk/runtime"
	"github.com/samcharles93/ai-sdk/toolkit"

	"github.com/samcharles93/archie-core/internal/skill"
	"github.com/samcharles93/archie-core/internal/skillscript"
	"github.com/samcharles93/archie-core/internal/tools"
)

type loopFunc func(context.Context, agentloop.Config) (agentloop.Result, error)

// LoopRunner executes one workflow stage through ai-sdk's agent loop. It is
// worker-local: archie-agent owns the complete workflow and uses this runner
// for each stage inside its task-scoped container.
type LoopRunner struct {
	runtime *runtime.Runtime
	log     *slog.Logger
	run     loopFunc
	tools   *tools.Registry

	// Limits bounds the size of tool results fed back into the stage. The
	// zero value applies no limits.
	Limits ToolLimits
	// AllowTools is the profile's allowlist over archie-added tools. Empty allows
	// all. Capture tools and agentloop's built-in file tools are never
	// filtered.
	AllowTools []string
}

func NewLoopRunner(
	rt *runtime.Runtime,
	log *slog.Logger,
	registries ...*tools.Registry,
) *LoopRunner {
	runner := &LoopRunner{runtime: rt, log: log, run: agentloop.Run}
	if len(registries) > 0 {
		runner.tools = registries[0]
	}
	return runner
}

// workspaceToolset is the central toolset a task agent never gets: its tools
// work on the chat workspace and bypass ReadOnly and ProtectPaths.
const workspaceToolset = "workspace"

func (r *LoopRunner) Run(ctx context.Context, workspace string, req agentrun.Request, report agentrun.ToolCallReporter) (agentrun.Result, error) {
	if err := req.Validate(); err != nil {
		return agentrun.Result{}, err
	}
	if r.runtime == nil {
		return agentrun.Result{}, fmt.Errorf("agent runtime is not configured")
	}
	if r.run == nil {
		return agentrun.Result{}, fmt.Errorf("agent loop is not configured")
	}

	notes := &memoryNotes{initial: req.Notes}
	captures := make(map[string][]json.RawMessage)
	toolOpts := r.Limits.Options(workspaceToolset)
	toolOpts.OnToolCall = report
	centralTools, err := BuildToolSet(r.tools, toolOpts)
	if err != nil {
		return agentrun.Result{}, fmt.Errorf("build central tool set: %w", err)
	}
	captureTools := captureToolSet(req.CaptureTools, captures)
	scriptTools := scriptToolSet(workspace)
	pluginTools, err := pluginToolSet(req, workspace, centralTools, captureTools, scriptTools)
	if err != nil {
		return agentrun.Result{}, err
	}
	res, err := r.run(ctx, agentloop.Config{
		Runtime:    r.runtime,
		ModelRef:   req.Model,
		WorkDir:    workspace,
		Mission:    req.Mission,
		ExtraRules: agentexec.ProjectScopedRules(workspace, req.ExtraRules),
		Notes:      notes,
		Gate:       toAgentGate(req.Gate),
		Preflight:  toAgentCommands(req.Preflight),
		Budget: agentloop.Budget{
			MaxSteps:  req.Budget.MaxSteps,
			WallClock: req.Budget.WallClock,
		},
		Compact: agentloop.CompactionConfig{
			Enabled:        req.ContextWindow > 0,
			ContextWindow:  req.ContextWindow,
			ThresholdRatio: 0.5,
			TargetRatio:    0.15,
		},
		Completion:   agentloop.CompletionForceFinish,
		ReadOnly:     req.ReadOnly,
		ProtectPaths: agentexec.ProtectionMatcher(req.Protection, req.ReadOnly),
		Extra: mergeToolSets(
			allowedTools(centralTools, r.AllowTools),
			captureTools,
			allowedTools(scriptTools, r.AllowTools),
			allowedTools(pluginTools, r.AllowTools),
		),
		Logger: r.logger(req),
	})
	result := resultFromRun(req, res, notes.appended, captures)
	if cause := context.Cause(ctx); cause != nil {
		return result, cause
	}
	return result, err
}

func resultFromRun(req agentrun.Request, res agentloop.Result, appended []string, captures map[string][]json.RawMessage) agentrun.Result {
	result := agentrun.Result{
		Version:    agentrun.ProtocolVersion,
		TaskID:     req.TaskID,
		Attempt:    req.Attempt,
		Stage:      req.Stage,
		Status:     string(res.Status),
		StopReason: res.StopReason,
		Changes:    res.Changes,
		Iterations: res.Iterations,
		TokensUsed: res.TokensUsed,
		Usage: agentrun.Usage{
			PromptTokens:        res.Usage.PromptTokens,
			CompletionTokens:    res.Usage.CompletionTokens,
			TotalTokens:         res.Usage.TotalTokens,
			CachedTokens:        res.Usage.CachedTokens,
			CacheCreationTokens: res.Usage.CacheCreationTokens,
		},
		Summary:       res.Summary,
		Detail:        res.Detail,
		AppendedNotes: appended,
		Captures:      captures,
	}
	// agentloop may return a zero-valued status on early exit (model
	// connectivity failure, empty response, etc.). Normalise it to a
	// known value so protocol validation always passes.
	if result.Status == "" {
		result.Status = "blocked"
		result.Detail = "agent returned no status (possible model connectivity issue)"
	}
	return result
}

func pluginToolSet(req agentrun.Request, workspace string, occupied ...core.ToolSet) (core.ToolSet, error) {
	if req.ReadOnly || len(req.Protection.Suffixes)+len(req.Protection.Globs) > 0 || len(req.Gate.Commands) > 0 {
		return nil, nil
	}
	builtins := toolkit.NewRegistry()
	if err := toolkit.RegisterBuiltins(builtins, workspace); err != nil {
		return nil, fmt.Errorf("register built-in tools for plugin collision check: %w", err)
	}
	reserved := map[string]struct{}{"finish": {}, "write_note": {}}
	for _, name := range builtins.Names() {
		reserved[name] = struct{}{}
	}
	set := make(core.ToolSet, len(req.Plugins))
	for _, spec := range req.Plugins {
		if _, exists := reserved[spec.Name]; exists {
			return nil, fmt.Errorf("plugin tool %q conflicts with an agent-loop tool", spec.Name)
		}
		for _, tools := range occupied {
			if _, exists := tools[spec.Name]; exists {
				return nil, fmt.Errorf("plugin tool %q conflicts with an existing tool", spec.Name)
			}
		}
		plugin := skill.Plugin{Name: spec.Name, Src: spec.Src}
		set[spec.Name] = core.NewTypedTool(
			spec.Name,
			"Run the project-bundled "+spec.Name+" plugin.",
			func(_ context.Context, args struct {
				Input string `json:"input"`
			},
			) (string, error) {
				return plugin.Run(args.Input)
			},
		)
	}
	return set, nil
}

func (r *LoopRunner) logger(req agentrun.Request) *slog.Logger {
	if r.log == nil {
		return slog.New(slog.DiscardHandler)
	}
	return r.log.With("task", req.TaskID, "attempt", req.Attempt, "stage", req.Stage, "model", req.Model)
}

func toAgentGate(g agentrun.Gate) agentloop.GateConfig {
	return agentloop.GateConfig{
		Commands:               toAgentCommands(g.Commands),
		MaxConsecutiveFailures: g.MaxConsecutiveFailures,
	}
}

func toAgentCommands(commands []agentrun.Command) []agentloop.GateCommand {
	out := make([]agentloop.GateCommand, 0, len(commands))
	for _, command := range commands {
		out = append(out, agentloop.GateCommand{
			Name: command.Name, Argv: command.Argv, ExpectFailure: command.ExpectFailure,
		})
	}
	return out
}

func captureToolSet(specs []agentrun.CaptureTool, captures map[string][]json.RawMessage) core.ToolSet {
	tools := make(core.ToolSet, len(specs))
	for _, spec := range specs {
		tools[spec.Name] = core.NewTool(spec.Name, spec.Description, spec.Parameters,
			makeCaptureHandler(spec, captures))
	}
	return tools
}

// makeCaptureHandler builds a tool handler that validates and records
// capture-tool invocations.
func makeCaptureHandler(spec agentrun.CaptureTool, captures map[string][]json.RawMessage) func(context.Context, string) (string, error) {
	return func(_ context.Context, input string) (string, error) {
		value := json.RawMessage(input)
		reply, ok := agentexec.AcceptCapture(spec, len(captures[spec.Name]), value)
		if ok {
			captures[spec.Name] = append(captures[spec.Name], append(json.RawMessage(nil), value...))
		}
		return reply, nil
	}
}

// scriptToolSet exposes run_go_script, which runs a Yaegi Go script from the
// workspace and returns its output.
func scriptToolSet(workspace string) core.ToolSet {
	return core.ToolSet{
		"run_go_script": core.NewTypedTool(
			"run_go_script",
			"Run a Yaegi-interpreted Go script (e.g. a skill's bundled scripts/*.go helper) and return everything it printed.",
			func(ctx context.Context, args struct {
				Path string `json:"path" jsonschema:"description=Path to the .go script, relative to the workspace root."`
			},
			) (string, error) {
				if strings.TrimSpace(args.Path) == "" {
					return "run_go_script rejected: arguments must be a JSON object with a non-empty path field", nil
				}
				full := filepath.Join(workspace, args.Path)
				if rel, err := filepath.Rel(workspace, full); err != nil || strings.HasPrefix(rel, "..") {
					return "run_go_script rejected: path escapes the workspace", nil //nolint:nilerr // rejection feedback lets the model retry with a valid path
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

// allowedTools keeps the tools allow names; an empty allow keeps them all.
func allowedTools(set core.ToolSet, allow []string) core.ToolSet {
	if len(allow) == 0 {
		return set
	}
	kept := core.ToolSet{}
	for name, tool := range set {
		if slices.Contains(allow, name) {
			kept[name] = tool
		}
	}
	return kept
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
