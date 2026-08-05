package agentexec

import (
	"context"
	"encoding/json"
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
	MaxResultChars  int
	TurnBudgetChars int
	SpillDir        string
}

// EnsureSpillDir creates the spill directory if one is configured.
//
// Call it once at startup. Without it the directory never exists, and because
// TurnBudget's write failure is silent every oversized result falls back to
// inline truncation -- spilling looks configured and does nothing. An empty
// SpillDir is not an error: it selects inline truncation deliberately.
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

// Options builds the tool-set options for a single turn or stage, minting a
// fresh TurnBudget each time.
//
// The budget must never outlive one turn: a shared counter would let one
// task's tool output exhaust another's allowance, and nothing would reset it.
func (l ToolLimits) Options(excludeToolsets ...string) ToolSetOptions {
	opts := ToolSetOptions{
		MaxResultChars:  l.MaxResultChars,
		ExcludeToolsets: excludeToolsets,
	}
	if l.TurnBudgetChars > 0 {
		opts.Budget = tools.NewTurnBudget(l.TurnBudgetChars, l.SpillDir)
	}
	return opts
}

// ToolSetOptions bounds what a built tool set may do and return.
//
// The zero value builds every available tool with no result limits, which is
// the behaviour this package had before the options existed.
type ToolSetOptions struct {
	// Budget accounts for the aggregate size of tool results across one chat
	// turn or agent stage, and supplies the spill directory oversized results
	// are displaced to. Nil disables budget accounting entirely.
	Budget *tools.TurnBudget

	// MaxResultChars caps a single tool result. Entries setting
	// ToolEntry.MaxResultSizeChars override it. Zero or negative means no cap.
	MaxResultChars int

	// ExcludeToolsets omits entries whose ToolEntry.Toolset matches. Task
	// agents use it to drop the chat workspace's file and shell tools, which
	// are rooted at a directory that is not the task's worktree and which
	// bypass the agent loop's read-only and protected-path enforcement.
	ExcludeToolsets []string
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
	return func(ctx context.Context, input string) (string, error) {
		// A turn that has spent its budget must say so. Returning an error
		// tells the model it has run out; returning an empty result would
		// read as "the tool found nothing" and invite it to try again.
		if opts.Budget != nil && opts.Budget.Exceeded() {
			return "", fmt.Errorf("tool %s: turn budget exceeded (%d chars)", entry.Name, opts.Budget.MaxChars)
		}

		var args map[string]any
		if input != "" {
			if err := json.Unmarshal([]byte(input), &args); err != nil {
				return "", fmt.Errorf("tool %s: decode input: %w", entry.Name, err)
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

		payload := tools.CapPayload(entry.Name, string(data), limit, opts.Budget)
		if opts.Budget != nil {
			opts.Budget.Consume(len(payload))
		}
		return payload, nil
	}
}
