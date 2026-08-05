package tools

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// ExecutionClass defines how a tool interacts with the dispatch gating engine.
type ExecutionClass int

const (
	// ExecNeverParallel marks tools that must run alone  --  typically state
	// mutators where concurrent execution could cause corruption.
	ExecNeverParallel ExecutionClass = iota

	// ExecParallelSafe marks tools that can run concurrently with any
	// other tool. Read-only tools (idempotent classification) are
	// automatically promoted to this class.
	ExecParallelSafe

	// ExecPathScoped marks tools that can run in parallel with tools
	// targeting different paths/scopes, but must serialize with tools
	// targeting the same path.
	ExecPathScoped
)

// String returns a human-readable representation of the execution class.
func (c ExecutionClass) String() string {
	switch c {
	case ExecNeverParallel:
		return "never-parallel"
	case ExecParallelSafe:
		return "parallel-safe"
	case ExecPathScoped:
		return "path-scoped"
	default:
		return "unknown"
	}
}

// ClassifyEntry derives the [ExecutionClass] from a [ToolEntry]'s
// classification flags. Idempotent tools are parallel-safe. Mutating
// tools are never-parallel unless explicitly marked otherwise.
// Unclassified tools default to never-parallel (conservative).
func ClassifyEntry(e ToolEntry) ExecutionClass {
	if e.Classification.IsIdempotent() {
		return ExecParallelSafe
	}
	if e.Classification.IsMutating() {
		return ExecNeverParallel
	}
	// Conservative default: treat unclassified tools as never-parallel.
	return ExecNeverParallel
}

// ---------------------------------------------------------------------------
// Dispatch types
// ---------------------------------------------------------------------------

// DispatchResult holds the outcome of a single tool invocation.
type DispatchResult struct {
	// ToolName is the name of the tool that was invoked.
	ToolName string

	// Output is the value returned by the tool's Handler. nil when
	// IsError is true.
	Output any

	// Preview is a truncated version of the output suitable for
	// inclusion in the conversation context. It is at most 500 chars.
	Preview string

	// Error is the error returned by the handler, or a dispatch-level
	// error (e.g. tool not found, budget exceeded).
	Error error

	// Duration is the wall-clock time spent executing the tool
	// (handler execution only; does not include spill time).
	Duration time.Duration
}

// IsError reports whether the result represents a failed invocation.
func (r DispatchResult) IsError() bool { return r.Error != nil }

// previewChars is the maximum length of a result preview.
const previewChars = 500

// truncatePreview returns a short summary of the output suitable for
// the conversation context.
func truncatePreview(output any) string {
	if output == nil {
		return ""
	}
	s := fmt.Sprint(output)
	if len(s) <= previewChars {
		return s
	}
	return s[:previewChars] + "... [truncated]"
}

// ---------------------------------------------------------------------------
// Tool invocation
// ---------------------------------------------------------------------------

// ToolCall represents a single pending tool invocation.
type ToolCall struct {
	// Entry is the tool to invoke. Handler must be non-nil.
	Entry ToolEntry

	// Input is the deserialized JSON input to pass to the handler.
	Input map[string]any

	// PathScope is an optional key used by ExecPathScoped gating.
	// Tools with the same non-empty PathScope are serialized.
	PathScope string
}

// invokeTool executes a single tool and returns a [DispatchResult].
func invokeTool(ctx context.Context, call ToolCall) DispatchResult {
	start := time.Now()
	result := DispatchResult{ToolName: call.Entry.Name}

	output, err := call.Entry.Handler(ctx, call.Input)
	result.Duration = time.Since(start)

	if err != nil {
		result.Error = err
		return result
	}

	result.Output = output
	result.Preview = truncatePreview(output)
	return result
}

// ---------------------------------------------------------------------------
// 17.5  --  Sequential dispatch
// ---------------------------------------------------------------------------

// DispatchSequential invokes tools one at a time, capturing per-result
// previews and classifying errors. Each result includes a truncated
// preview for inclusion in the conversation context.
//
// If budget is non-nil, each result's output size is consumed from the
// budget. When the budget is exceeded, remaining calls are skipped with
// a budget-exceeded error.
//
// If guardrail is non-nil, failures are recorded and the dispatch may
// abort early on a hard-stop decision.
func DispatchSequential(
	ctx context.Context,
	calls []ToolCall,
	budget *TurnBudget,
	guardrail *GuardrailEngine,
) []DispatchResult {
	results := make([]DispatchResult, 0, len(calls))

	for _, call := range calls {
		// Check for hard-stop before dispatching.
		if guardrail != nil && guardrail.HardStopped() {
			results = append(results, DispatchResult{
				ToolName: call.Entry.Name,
				Error:    fmt.Errorf("guardrail hard-stop active"),
			})
			break
		}

		// Check budget before dispatching. Once exceeded, skip all
		// remaining calls (break, not continue  --  no point appending
		// redundant budget-exceeded errors for every remaining call).
		if budget != nil && budget.Exceeded() {
			results = append(results, DispatchResult{
				ToolName: call.Entry.Name,
				Error:    fmt.Errorf("turn budget exceeded (%d chars)", budget.MaxChars),
			})
			break
		}

		result := dispatchOne(ctx, call, budget, guardrail)
		results = append(results, result)

		// Abort on hard-stop.
		if guardrail != nil && guardrail.HardStopped() {
			break
		}
	}

	return results
}

// ---------------------------------------------------------------------------
// 17.6  --  Concurrent dispatch
// ---------------------------------------------------------------------------

// gatedGroup holds a set of calls that must run sequentially within the
// group but can run concurrently with other groups.
type gatedGroup struct {
	calls []ToolCall
	class ExecutionClass
	scope string // non-empty only for ExecPathScoped
}

// DispatchConcurrent invokes tools concurrently where safe, applying
// execution gating:
//
//   - ExecNeverParallel: runs alone, blocking all other execution.
//   - ExecParallelSafe: runs concurrently with any other tool.
//   - ExecPathScoped: same-path tools serialize, different paths run
//     in parallel.
//
// Results are returned in the same order as calls.
func DispatchConcurrent(
	ctx context.Context,
	calls []ToolCall,
	budget *TurnBudget,
	guardrail *GuardrailEngine,
) []DispatchResult {
	if len(calls) == 0 {
		return nil
	}

	// Classify each call and build gating groups.
	groups := buildGatingGroups(calls)

	// Execute groups: never-parallel groups block all concurrency.
	// Others run in parallel within their group, sequentially between groups.
	results := make([]DispatchResult, len(calls))
	resultIdx := 0

	for _, group := range groups {
		if group.class == ExecNeverParallel || len(group.calls) == 1 {
			// Run sequentially.
			for _, call := range group.calls {
				results[resultIdx] = dispatchOne(ctx, call, budget, guardrail)
				resultIdx++
			}
		} else {
			// Run in parallel within group.
			res := dispatchParallel(ctx, group.calls, budget, guardrail)
			for _, r := range res {
				results[resultIdx] = r
				resultIdx++
			}
		}
	}

	return results
}

// buildGatingGroups partitions calls into groups based on their execution
// class. Never-parallel calls get isolated groups. Path-scoped calls share
// a group if they have the same path; otherwise they get separate groups.
// Parallel-safe calls all share one group.
//
// Groups are tracked by index (not pointer) to avoid the value-copy bug
// where appending to a map-stored pointer was invisible to the slice copy.
func buildGatingGroups(calls []ToolCall) []gatedGroup {
	if len(calls) == 0 {
		return nil
	}

	var groups []gatedGroup

	// Track path-scoped groups by scope key to index in the groups slice.
	pathScopedIdx := make(map[string]int)

	for _, call := range calls {
		class := ClassifyEntry(call.Entry)

		switch class {
		case ExecNeverParallel:
			// Isolate: one group per never-parallel call.
			groups = append(groups, gatedGroup{
				calls: []ToolCall{call},
				class: ExecNeverParallel,
			})

		case ExecPathScoped:
			scope := call.PathScope
			if scope == "" {
				// No scope specified  --  treat as never-parallel.
				groups = append(groups, gatedGroup{
					calls: []ToolCall{call},
					class: ExecNeverParallel,
				})
				continue
			}
			if idx, ok := pathScopedIdx[scope]; ok {
				// Update existing group in-place through the index.
				groups[idx].calls = append(groups[idx].calls, call)
			} else {
				idx := len(groups)
				groups = append(groups, gatedGroup{
					calls: []ToolCall{call},
					class: ExecPathScoped,
					scope: scope,
				})
				pathScopedIdx[scope] = idx
			}

		case ExecParallelSafe:
			// Batch all parallel-safe calls into one group.
			if idx, ok := firstParallelSafeIdx(groups); ok {
				groups[idx].calls = append(groups[idx].calls, call)
			} else {
				groups = append(groups, gatedGroup{
					calls: []ToolCall{call},
					class: ExecParallelSafe,
				})
			}
		}
	}

	return groups
}

// firstParallelSafeIdx returns the index of the first parallel-safe group
// in the slice. Used by buildGatingGroups to batch parallel-safe calls.
func firstParallelSafeIdx(groups []gatedGroup) (int, bool) {
	for i := range groups {
		if groups[i].class == ExecParallelSafe {
			return i, true
		}
	}
	return 0, false
}

// dispatchOne runs a single call with budget, guardrail, and per-tool
// limit checks.
//
// Callers that do not own the batch cannot use this: it caps
// DispatchResult.Preview, whereas a caller handing the result straight to a
// model has to cap the payload it actually sends. [CapPayload] is the shared
// rule between the two; this sequence is specific to batch dispatch.
func dispatchOne(
	ctx context.Context,
	call ToolCall,
	budget *TurnBudget,
	guardrail *GuardrailEngine,
) DispatchResult {
	// Check budget.
	if budget != nil && budget.Exceeded() {
		return DispatchResult{
			ToolName: call.Entry.Name,
			Error:    fmt.Errorf("turn budget exceeded (%d chars)", budget.MaxChars),
		}
	}

	if guardrail != nil && guardrail.HardStopped() {
		return DispatchResult{
			ToolName: call.Entry.Name,
			Error:    fmt.Errorf("guardrail hard-stop active"),
		}
	}

	result := invokeTool(ctx, call)

	// Enforce per-tool result size limit (17.4).
	capped := enforcePerToolLimit(&result, call.Entry, budget)

	// Track guardrail.
	if guardrail != nil {
		if result.IsError() {
			_ = guardrail.RecordFailure(call.Entry.Name, result.Error)
		} else {
			_ = guardrail.RecordSuccess(call.Entry.Name)
		}
	}

	// Consume budget.
	if budget != nil && !result.IsError() {
		consumeBudgetForResult(&result, call.Entry, budget, capped)
	}

	return result
}

// enforcePerToolLimit replaces the preview when the result exceeds the tool's
// MaxResultSizeChars, spilling to disk when the budget offers a directory and
// truncating inline otherwise. [CapPayload] owns that rule; this only decides
// whether it applies.
//
// Returns whether the result was capped, so the budget accounting below does
// not spill the same output a second time.
func enforcePerToolLimit(result *DispatchResult, entry ToolEntry, budget *TurnBudget) bool {
	if result.IsError() || entry.MaxResultSizeChars <= 0 || result.Output == nil {
		return false
	}

	fullOutput := fmt.Sprint(result.Output)
	capped := CapPayload(entry.Name, fullOutput, entry.MaxResultSizeChars, budget)
	if capped == fullOutput {
		return false
	}
	result.Preview = capped
	return true
}

// consumeBudgetForResult accounts for the result against the turn budget.
// When the budget is exceeded during consumption, the full output is
// spilled to disk and the preview is replaced with a spill reference.
func consumeBudgetForResult(result *DispatchResult, entry ToolEntry, budget *TurnBudget, alreadyCapped bool) {
	if budget == nil || result.IsError() {
		return
	}

	if budget.Consume(len(result.Preview)) {
		return
	}

	// Budget exhausted on this result. Spill the full output so it is
	// displaced rather than lost -- unless the per-tool limit already did,
	// in which case repeating it would write a second identical file and
	// orphan the first, whose path the preview already names.
	if alreadyCapped || result.Output == nil {
		return
	}
	fullOutput := fmt.Sprint(result.Output)
	if ref := budget.WriteSpill(entry.Name, []byte(fullOutput)); ref.Path != "" {
		result.Preview = fmt.Sprintf("[spilled to %s, %d chars]", ref.Path, ref.SizeChars)
	}
}

// dispatchParallel runs multiple calls concurrently and returns results
// in the same order.
func dispatchParallel(
	ctx context.Context,
	calls []ToolCall,
	budget *TurnBudget,
	guardrail *GuardrailEngine,
) []DispatchResult {
	results := make([]DispatchResult, len(calls))
	var wg sync.WaitGroup
	wg.Add(len(calls))

	for i, call := range calls {
		go func(idx int, c ToolCall) {
			defer wg.Done()
			results[idx] = dispatchOne(ctx, c, budget, guardrail)
		}(i, call)
	}

	wg.Wait()
	return results
}
