package agentworker

import (
	"context"
	"fmt"
	"strings"

	"github.com/samcharles93/ai-sdk/agent"
	"github.com/samcharles93/ai-sdk/core"
	"github.com/samcharles93/ai-sdk/runtime"
	"github.com/samcharles93/ai-sdk/toolkit"

	"github.com/samcharles93/archie-core/internal/agentexec"
	"github.com/samcharles93/archie-core/internal/domain/workflow"
	"github.com/samcharles93/archie-core/internal/taskrun"
)

// newReviewerFor builds the workflow.Reviewer for one task run, or nil
// when the repo has not opted into review (Repo.ReviewEnabled == false)
// or no providers are configured. Model resolution mirrors AgentStage's
// own role->"builder" fallback (agent.go): Models["reviewer"], falling
// back to Models["builder"].
func newReviewerFor(req taskrun.Request) workflow.Reviewer {
	if !req.Repo.ReviewEnabled {
		return nil
	}
	modelRef := req.Cfg.Models["reviewer"]
	if modelRef == "" {
		modelRef = req.Cfg.Models["builder"]
	}
	if modelRef == "" {
		return nil
	}
	rt := agentexec.NewRuntime(req.Providers)
	if rt == nil {
		return nil
	}
	return newSubagentReviewer(rt, modelRef)
}

// defaultReviewMaxSteps bounds the reviewer's tool loop when neither
// ReviewRequest.MaxSteps nor the repo's configured budget set one.
const defaultReviewMaxSteps = 10

// subagentReviewer implements workflow.Reviewer using ai-sdk's
// agent.Subagent: a nested, synchronous generation in this same worker
// process, with its own model, system prompt, toolset, and step budget.
//
// Isolation is two-fold. Conversation isolation is structural: Subagent.Run
// only ever sends a prompt string, never the implementer's message
// history. Workspace isolation is built by the caller (StageReview via
// workflow.Trees.Snapshot): reviewerToolSet is rooted at that snapshot
// directory and holds only read-only tools -- never the worker's own
// toolset, which carries the worktreerpc publication grant a reviewer must
// not be able to reach.
type subagentReviewer struct {
	runtime  *runtime.Runtime
	modelRef string
}

// newSubagentReviewer constructs a Reviewer that runs on modelRef, resolved
// through rt (the same provider runtime the worker's own agent runner
// uses). rt must be non-nil.
func newSubagentReviewer(rt *runtime.Runtime, modelRef string) *subagentReviewer {
	return &subagentReviewer{runtime: rt, modelRef: modelRef}
}

var _ workflow.Reviewer = (*subagentReviewer)(nil)

// Review runs one adversarial review. It never returns a Go error: every
// failure mode (unresolvable model, a Subagent.Run error, a truncated
// run) folds into the returned ReviewReport, per
// docs/prds/adversarial-self-review.md section 4's fail-closed contract.
func (s *subagentReviewer) Review(ctx context.Context, req workflow.ReviewRequest) workflow.ReviewReport {
	if s.runtime == nil {
		return workflow.NewNotRunReviewReport("no provider runtime configured for the reviewer")
	}
	provider, model, err := s.runtime.ChatProvider(ctx, s.modelRef)
	if err != nil {
		return workflow.NewNotRunReviewReport(fmt.Sprintf("resolve reviewer model %q: %v", s.modelRef, err))
	}

	var findings []workflow.ReviewFinding
	tools := reviewerToolSet(req.SnapshotDir, &findings)

	maxSteps := req.MaxSteps
	if maxSteps <= 0 {
		maxSteps = defaultReviewMaxSteps
	}

	sub := agent.Subagent{
		Provider: provider,
		Model:    model,
		System:   reviewerSystemPrompt,
		Tools:    tools,
		MaxSteps: maxSteps,
	}

	summary, runErr := sub.Run(ctx, reviewerPrompt(req))
	if runErr != nil {
		// A truncated or errored run still yields whatever the reviewer
		// captured before it ran out of budget -- those findings are real
		// and must not be discarded just because the run's own
		// conclusion never arrived (docs/prds/adversarial-self-review.md
		// section 1, "findings return through a closure"). Zero captured
		// findings on error means the reviewer never got anywhere, which
		// fail-closed treats as not having run at all.
		if len(findings) == 0 {
			return workflow.NewNotRunReviewReport(runErr.Error())
		}
		return workflow.NewCompletedReviewReport(findings, "")
	}
	return workflow.NewCompletedReviewReport(findings, summary)
}

// reviewerSystemPrompt instructs the reviewer per CLAUDE.md's own
// adversarial-review discipline: assume every line is wrong until proven
// correct, work the checklist, report only what a fresh reader can verify
// from what it was given.
const reviewerSystemPrompt = `You are a fresh adversarial code reviewer. You did not write this change and have no access to how or why it was written -- only what is given to you below.

Assume every line is wrong until you have verified it is correct. Read whole files with your read tool, not just diff hunks, before concluding a call site or interface is satisfied. Use grep/find to check for other call sites a change affects.

Check for: dead code, unchecked errors, hardcoded values that should be parameters, interface-satisfaction defects, nil-pointer risk, goroutine leaks, races and unsynchronized shared state.

For every defect you find, call record_finding with: the file and line, a one-sentence defect statement, a concrete failure scenario (specific inputs or state that produce the wrong output or a crash -- not a hypothetical), a verdict, a level, and a category.

Verdict is "confirmed" only when you have traced the actual failure -- read the code paths involved and can state exactly how it goes wrong. Verdict is "plausible" for a real worry you have not fully traced. Only a "confirmed" finding at level "error" blocks the pull request; do not inflate a plausible worry to confirmed/error to make it count -- an unjustified block is worse than a missed one, because it teaches the operator to stop trusting your reports.

Level is "error" for a defect that blocks; "warn" for something worth knowing but not blocking.

When you have finished checking, reply with a short summary of what you checked and what you found (or that you found nothing). If you find nothing, say so plainly -- reporting zero findings on properties you actually checked is a valid and expected outcome, not a failure to find something.`

// reviewerPrompt renders the reviewer's mission: the diff and issue text.
// The snapshot itself is not embedded here -- it reaches the reviewer only
// through its read-only toolset, rooted at SnapshotDir, never as prompt
// text the reviewer could mistake for something it already checked.
func reviewerPrompt(req workflow.ReviewRequest) string {
	var b strings.Builder
	b.WriteString("Review the following change. The full file tree at the reviewed commit is available through your tools; ")
	b.WriteString("start by reading the files the diff touches, not just the diff itself.\n\n")
	b.WriteString("<issue>\n")
	b.WriteString(req.IssueText)
	b.WriteString("\n</issue>\n\n<diff>\n")
	b.WriteString(req.Diff)
	b.WriteString("\n</diff>\n")
	return b.String()
}

// reviewerToolSet builds the reviewer's read-only toolset, rooted at
// snapshotDir, plus the structured findings-capture tool that appends
// directly to findings. This is a distinct toolset built fresh for each
// review -- never the worker's own registry, which holds write/edit/shell
// tools and the worktreerpc publication grant.
func reviewerToolSet(snapshotDir string, findings *[]workflow.ReviewFinding) core.ToolSet {
	reg := toolkit.NewRegistry()
	readOnly := []toolkit.Tool{
		toolkit.NewReadTool(snapshotDir, toolkit.NewReadTracker()),
		toolkit.NewGrepTool(snapshotDir),
		toolkit.NewFindTool(snapshotDir),
	}
	for _, tool := range readOnly {
		_ = reg.Register(tool) // names are fixed and distinct; Register cannot fail here
	}
	set := reg.CoreToolSet(toolkit.NonInteractiveBridge{})
	set["record_finding"] = recordFindingTool(findings)
	return set
}

// findingInput is the reviewer-facing shape of workflow.ReviewFinding: the
// model reports plain strings for Verdict/Level/Category, validated and
// converted before being appended.
type findingInput struct {
	File            string `json:"file" jsonschema:"description=Repo-relative file path where the defect is located."`
	Line            int    `json:"line" jsonschema:"description=Line number where the defect is located."`
	Defect          string `json:"defect" jsonschema:"description=One-sentence statement of the defect."`
	FailureScenario string `json:"failure_scenario" jsonschema:"description=Concrete inputs or state that produce the wrong output or a crash -- not a hypothetical."`
	Verdict         string `json:"verdict" jsonschema:"description=Only 'confirmed' may block. Use 'plausible' for a real worry you have not fully traced.,enum=confirmed|plausible"`
	Level           string `json:"level" jsonschema:"description=error blocks when confirmed; warn is advisory only.,enum=error|warn"`
	Category        string `json:"category" jsonschema:"enum=dead-code|unchecked-error|hardcoded-value|interface-satisfaction|nil-risk|goroutine-leak|race|other"`
}

// recordFindingTool builds the typed capture tool the reviewer must call
// to report a defect. Rejecting an invalid finding with a plain-text
// message (rather than a Go error) lets the model see what was wrong and
// retry, the same environmental-enforcement pattern the rest of the
// agent-loop tool contract uses.
func recordFindingTool(findings *[]workflow.ReviewFinding) *core.Tool {
	return core.NewTypedTool(
		"record_finding",
		"Report one defect found during review. Call this once per defect; do not summarize multiple defects into one call.",
		func(_ context.Context, in findingInput) (string, error) {
			finding := workflow.ReviewFinding{
				File:            in.File,
				Line:            in.Line,
				Defect:          in.Defect,
				FailureScenario: in.FailureScenario,
				Verdict:         workflow.ReviewVerdict(in.Verdict),
				Level:           workflow.ReviewLevel(in.Level),
				Category:        workflow.ReviewCategory(in.Category),
			}
			if err := finding.Validate(); err != nil {
				return "record_finding rejected: " + err.Error(), nil //nolint:nilerr // rejection feedback lets the model retry with a valid finding
			}
			*findings = append(*findings, finding)
			return "finding recorded", nil
		},
	)
}
