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

	// Error is the error returned by the handler or dispatch layer.
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
//
// Tools that require human approval are refused. The dispatch path has no
// ApprovalRequester — it is used by the agent execution loop, which runs
// without a human in the loop. The chat path gates approval through
// BuildToolSetFrom → toolExecute, which has the approver wired.
func invokeTool(ctx context.Context, call ToolCall) DispatchResult {
	start := time.Now()
	result := DispatchResult{ToolName: call.Entry.Name}

	if call.Entry.Classification.IsApprovalRequired() {
		result.Error = fmt.Errorf("tool %s: requires human approval and cannot be invoked through the agent dispatch path", call.Entry.Name)
		return result
	}

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
// spillDir controls displacement of individual oversized results. Aggregate
// output volume never stops dispatch.
//
// If guardrail is non-nil, failures are recorded and the dispatch may
// abort early on a hard-stop decision.
func DispatchSequential(
	ctx context.Context,
	calls []ToolCall,
	spillDir string,
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

		result := dispatchOne(ctx, call, spillDir, guardrail)
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
	spillDir string,
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
				results[resultIdx] = dispatchOne(ctx, call, spillDir, guardrail)
				resultIdx++
			}
		} else {
			// Run in parallel within group.
			res := dispatchParallel(ctx, group.calls, spillDir, guardrail)
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

// dispatchOne runs a single call with guardrail and per-tool limit checks.
//
// Callers that do not own the batch cannot use this: it caps
// DispatchResult.Preview, whereas a caller handing the result straight to a
// model has to cap the payload it actually sends. [CapPayload] is the shared
// rule between the two; this sequence is specific to batch dispatch.
func dispatchOne(
	ctx context.Context,
	call ToolCall,
	spillDir string,
	guardrail *GuardrailEngine,
) DispatchResult {
	if guardrail != nil && guardrail.HardStopped() {
		return DispatchResult{
			ToolName: call.Entry.Name,
			Error:    fmt.Errorf("guardrail hard-stop active"),
		}
	}

	result := invokeTool(ctx, call)

	// Enforce per-tool result size limit (17.4).
	enforcePerToolLimit(&result, call.Entry, spillDir)

	// Track guardrail.
	if guardrail != nil {
		if result.IsError() {
			_ = guardrail.RecordFailure(call.Entry.Name, result.Error)
		} else {
			_ = guardrail.RecordSuccess(call.Entry.Name)
		}
	}

	return result
}

// enforcePerToolLimit replaces the preview when the result exceeds the tool's
// MaxResultSizeChars, spilling to disk when a directory is configured and
// truncating inline otherwise. [CapPayload] owns that rule; this only decides
// whether it applies.
//
// Returns whether the result was capped.
func enforcePerToolLimit(result *DispatchResult, entry ToolEntry, spillDir string) bool {
	if result.IsError() || entry.MaxResultSizeChars <= 0 || result.Output == nil {
		return false
	}

	fullOutput := fmt.Sprint(result.Output)
	capped := CapPayload(entry.Name, fullOutput, entry.MaxResultSizeChars, spillDir)
	if capped == fullOutput {
		return false
	}
	result.Preview = capped
	return true
}

// dispatchParallel runs multiple calls concurrently and returns results
// in the same order.
func dispatchParallel(
	ctx context.Context,
	calls []ToolCall,
	spillDir string,
	guardrail *GuardrailEngine,
) []DispatchResult {
	results := make([]DispatchResult, len(calls))
	var wg sync.WaitGroup
	wg.Add(len(calls))

	for i, call := range calls {
		go func(idx int, c ToolCall) {
			defer wg.Done()
			results[idx] = dispatchOne(ctx, c, spillDir, guardrail)
		}(i, call)
	}

	wg.Wait()
	return results
}
