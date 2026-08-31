package agentexec

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"

	aicore "github.com/samcharles93/ai-sdk/core"

	"github.com/samcharles93/archie-core/internal/tools"
)

// ToolLimits are the configured result limits for one chat turn or agent
// stage. It holds only settings, never accumulated state, so it is safe to
// store on a long-lived runner and share across tasks.
//
// A negative limit means no limit; see config.ToolPolicy for why zero cannot
// carry that meaning.
type ToolLimits struct {
	MaxResultChars int
	SpillDir       string
}

// EnsureSpillDir creates the spill directory if one is configured.
//
// Call it once at startup. Without it the directory never exists, and a
// failed spill write falls back to inline truncation -- spilling looks
// configured and does nothing. An empty SpillDir is not an error: it
// selects inline truncation deliberately.
func (l ToolLimits) EnsureSpillDir() error {
	if l.SpillDir == "" {
		return nil
	}
	// 0700: spill files are verbatim tool output and may hold anything the
	// tools could read.
	if err := os.MkdirAll(l.SpillDir, 0o700); err != nil {
		return fmt.Errorf("create tool spill directory %s: %w", l.SpillDir, err)
	}
	return nil
}

// Options builds the tool-set options for a single turn or stage.
// There is no aggregate turn output budget; only per-result caps apply.
func (l ToolLimits) Options(excludeToolsets ...string) ToolSetOptions {
	opts := ToolSetOptions{
		MaxResultChars:  l.MaxResultChars,
		ExcludeToolsets: excludeToolsets,
		SpillDir:        l.SpillDir,
	}
	return opts
}

// ToolSetOptions bounds what a built tool set may do and return.
//
// The zero value builds every available tool with no result limits, which is
// the behaviour this package had before the options existed.
type ToolSetOptions struct {
	// SpillDir receives complete results displaced by the per-tool cap. Empty
	// selects inline truncation. There is deliberately no aggregate turn cap.
	SpillDir string

	// MaxResultChars caps a single tool result. Entries setting
	// ToolEntry.MaxResultSizeChars override it. Zero or negative means no cap.
	MaxResultChars int

	// ExcludeToolsets omits entries whose ToolEntry.Toolset matches. Task
	// agents use it to drop the chat workspace's file and shell tools, which
	// are rooted at a directory that is not the task's worktree and which
	// bypass the agent loop's read-only and protected-path enforcement.
	ExcludeToolsets []string

	// Approval is the human-consent gate for tools marked RequiresApproval.
	// Nil means no approver is configured — such tools are omitted from the
	// built set rather than failing at call time.
	Approval tools.ApprovalRequester

	// OnToolCall, when non-nil, is notified once per completed tool call.
	// LoopRunner populates it so a workflow stage can surface tool
	// activity on the task timeline (archie-core-467's task-transcript
	// counterpart). Nil means no one is listening.
	OnToolCall ToolCallReporter
}

// ToolCallReport is one completed tool invocation, reported through a
// ToolCallReporter.
type ToolCallReport struct {
	Tool string
	// Detail is a clipped one-line summary: the tool's result, or
	// "error: <message>" if it failed.
	Detail string
	Failed bool
}

// ToolCallReporter receives one ToolCallReport per completed tool call. A nil
// reporter is valid and reports nothing.
type ToolCallReporter func(ToolCallReport)

// toolCallDetailBytes caps the summary ToolCallReport.Detail carries. This
// rides on every task's observability event stream, so one large file read
// must not dominate it the way baselineMissionBytes-sized content is allowed
// to dominate a builder's own context.
const toolCallDetailBytes = 300

func clipToolCallDetail(s string) string {
	if len(s) <= toolCallDetailBytes {
		return s
	}
	return s[:toolCallDetailBytes] + "…"
}

// reportToolCallCompletion notifies opts.OnToolCall, if set, of one
// completed call. Factored out of toolExecute's closure so the branching
// here doesn't add to that function's own cognitive-complexity budget.
func reportToolCallCompletion(opts ToolSetOptions, entry tools.ToolEntry, result string, err error) {
	if opts.OnToolCall == nil {
		return
	}
	outcome := result
	if err != nil {
		outcome = "error: " + err.Error()
	}
	opts.OnToolCall(ToolCallReport{Tool: entry.Name, Detail: clipToolCallDetail(outcome), Failed: err != nil})
}

// resultLimit returns the cap that applies to one entry.
func (o ToolSetOptions) resultLimit(entry tools.ToolEntry) int {
	if entry.MaxResultSizeChars != 0 {
		return entry.MaxResultSizeChars
	}
	return o.MaxResultChars
}

// excludes reports whether an entry's toolset is omitted.
func (o ToolSetOptions) excludes(entry tools.ToolEntry) bool {
	return slices.Contains(o.ExcludeToolsets, entry.Toolset)
}

// BuildToolSet converts every currently-available entry in reg into an
// ai-sdk core.ToolSet, ready to pass as core.GenerateOptions.Tools for a
// multi-step chat turn. A nil registry (memory/tools disabled) returns
// an empty set. Invalid availability callbacks and dynamic schemas return
// errors instead of panicking or silently hiding an advertised tool.
//
// core.Tool.Execute exchanges JSON-encoded strings with the model;
// tools.Handler exchanges a decoded map. The returned Execute functions
// do the JSON <-> map translation and surface decode failures as errors
// rather than silently dropping malformed model input.
//
// This is the only path from a registered tools.ToolEntry to a model, so it is
// also where result limits are enforced. The model runtime owns the tool loop
// and decides what to invoke, so archie cannot gate a batch -- but it builds
// every Execute closure here, which makes per-call accounting reachable.
func BuildToolSet(reg *tools.Registry, opts ToolSetOptions) (aicore.ToolSet, error) {
	if reg == nil {
		return aicore.ToolSet{}, nil
	}
	return BuildToolSetFrom(reg.All(), opts)
}

// BuildToolSetFrom converts a loose slice of entries under the same rules as
// [BuildToolSet].
//
// It exists for tools that are bound per turn rather than registered globally
// -- the task tools carry the calling gateway's identity, so there is one set
// of them per gateway and they cannot live in the process-wide registry. Pass
// the same ToolSetOptions to both calls so a single turn shares one budget.
func BuildToolSetFrom(entries []tools.ToolEntry, opts ToolSetOptions) (aicore.ToolSet, error) {
	set := aicore.ToolSet{}
	for _, entry := range entries {
		if opts.excludes(entry) {
			continue
		}
		// A tool that requires approval must not appear in a set built without
		// an approver: the model would see a tool it can never invoke. With an
		// approver present the tool is built and the execute closure gates each
		// call on the human's decision.
		if entry.Classification.IsApprovalRequired() && opts.Approval == nil {
			continue
		}
		available, err := safeToolAvailable(entry)
		if err != nil {
			return nil, fmt.Errorf("tool %s availability: %w", entry.Name, err)
		}
		if !available {
			continue
		}
		schema, err := safeResolvedSchema(entry)
		if err != nil {
			return nil, fmt.Errorf("tool %s schema: %w", entry.Name, err)
		}
		if schema == nil {
			schema = tools.JSONSchema{
				"type":       "object",
				"properties": map[string]any{},
			}
		}
		params, err := json.Marshal(schema)
		if err != nil {
			return nil, fmt.Errorf("tool %s schema: %w", entry.Name, err)
		}
		set[entry.Name] = aicore.NewTool(entry.Name, entry.Description, params, toolExecute(entry, opts))
	}
	return set, nil
}

func safeToolAvailable(entry tools.ToolEntry) (available bool, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("panic: %v", recovered)
		}
	}()
	return entry.Available(), nil
}

func safeResolvedSchema(entry tools.ToolEntry) (schema tools.JSONSchema, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("panic: %v", recovered)
		}
	}()
	return entry.ResolvedSchema(), nil
}

// toolExecute adapts a tools.ToolEntry's Handler to core.Tool's
// Execute(ctx, jsonInput string) (jsonOutput string, err error) shape, and
// applies the turn's result accounting.
//
// The cap is applied to the marshalled payload rather than to the handler's
// return value, because the marshalled form is what actually reaches the
// model: JSON escaping can change the length materially, so capping the Go
// value would bound the wrong string.
func toolExecute(entry tools.ToolEntry, opts ToolSetOptions) func(context.Context, string) (string, error) {
	limit := opts.resultLimit(entry)
	return func(ctx context.Context, input string) (result string, err error) {
		defer func() { reportToolCallCompletion(opts, entry, result, err) }()

		var args map[string]any
		if input != "" {
			if err := json.Unmarshal([]byte(input), &args); err != nil {
				return "", fmt.Errorf("tool %s: decode input: %w", entry.Name, err)
			}
		}
		if entry.Classification.IsApprovalRequired() {
			if err := requireApproval(ctx, entry, opts, args); err != nil {
				return "", err
			}
		}
		out, err := entry.Handler(ctx, args)
		if err != nil {
			return "", err
		}
		data, err := json.Marshal(out)
		if err != nil {
			return "", fmt.Errorf("tool %s: encode output: %w", entry.Name, err)
		}

		payload := tools.CapPayload(entry.Name, string(data), limit, opts.SpillDir)
		return payload, nil
	}
}

// requireApproval blocks until entry's approval is granted, returning an
// error if it is denied, times out, or the approver misbehaves.
func requireApproval(ctx context.Context, entry tools.ToolEntry, opts ToolSetOptions, args map[string]any) error {
	if opts.Approval == nil {
		return fmt.Errorf("tool %s requires approval but no approver is configured", entry.Name)
	}
	approveCtx, cancel := context.WithTimeout(ctx, tools.ToolApprovalTimeout)
	defer cancel()
	desc := entry.Description
	if entry.BuildApprovalDescription != nil {
		desc = entry.BuildApprovalDescription(args)
	}
	decision, err := opts.Approval.RequestApproval(approveCtx, entry.Name, desc)
	if err != nil {
		// Distinguish a tool-level timeout (our own deadline) from a
		// turn-level cancellation (/stop). A turn cancellation must
		// propagate as a context error; a tool timeout must surface as
		// a plain error so the model can report it instead of the turn
		// aborting.
		if (errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled)) && ctx.Err() == nil {
			return fmt.Errorf("tool %s: approval timed out after %v", entry.Name, tools.ToolApprovalTimeout)
		}
		return fmt.Errorf("tool %s: approval: %w", entry.Name, err)
	}
	switch decision {
	case tools.ApprovalApproved, tools.ApprovalPermanentlyApproved:
		// PermanentlyApproved is handled by the adapter caching the
		// decision; the dispatch layer treats both the same.
		return nil
	case tools.ApprovalDenied:
		return fmt.Errorf("tool %s: %w", entry.Name, tools.ErrApprovalDenied)
	default:
		// Fail closed: an unrecognised decision value from a faulty or
		// future Approver must not execute the tool. ApprovalDecision is
		// an exported int type, so a value returned with nil error is a
		// defect in the approver, not a grant.
		return fmt.Errorf("tool %s: approval returned unexpected decision %v", entry.Name, decision)
	}
}
