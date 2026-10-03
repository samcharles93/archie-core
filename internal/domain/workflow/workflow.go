// Package workflow runs workflows: ordered stages over a shared TaskContext.
package workflow

import (
	"context"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/domain/agentrun"
	"github.com/samcharles93/archie-core/internal/domain/workflow/task"
	"github.com/samcharles93/archie-core/internal/events"
	"github.com/samcharles93/archie-core/internal/taskstate"
	"github.com/samcharles93/archie-core/internal/tools"
)

// Forger is the subset of forge.Forge that workflow stages call mid-run.
// Production reaches it through forgerpc.Client, which proxies each call to the
// daemon over NATS -- the worker holds no forge credentials, so the daemon stays
// the only caller of forge.Forge. Test fakes implement it directly.
type Forger interface {
	CloseIssue(ctx context.Context, owner, repo string, number int, comment string) error
	CreatePR(ctx context.Context, owner, repo, title, head, base, body string) (int, error)
	LinkBranch(ctx context.Context, owner, repo string, issueNumber int, branch string) error
	// CreateReviewComments posts line-anchored review comments on an open pull
	// request in one call. It posts only while the PR head is still
	// reviewedHeadSHA; empty skips that check.
	CreateReviewComments(ctx context.Context, owner, repo string, number int, reviewedHeadSHA string, comments []ReviewComment) error
	// Comment posts a plain, non-anchored PR comment and returns its ID.
	// The remediate workflow's round-cap stage uses this to tell an
	// operator why it stopped remediating, and a whole-review reply (no
	// single inline comment to thread onto) falls back to it.
	Comment(ctx context.Context, owner, repo string, number int, body string) (int64, error)
	// ReplyToReview posts a threaded reply to one review comment. The
	// remediate workflow calls it once per remediation run, summarising
	// what changed.
	ReplyToReview(ctx context.Context, owner, repo string, number int, commentID int64, body string) error
}

// ReviewComment is one line-anchored review comment: path, line and body.
type ReviewComment struct {
	Path string
	Line int
	Body string
}

// PrepareTarget is the branch a prepare stage positions the worktree on;
// empty prepares fresh from the base branch.
type PrepareTarget string

// PrepareFresh is the fresh-run target: position the worktree onto the base
// branch.
const PrepareFresh PrepareTarget = ""

// Trees is the worktree API workflow stages call mid-run.
type Trees interface {
	Prepare(ctx context.Context, owner, repo, base string, issue int, title, body, labels string, target PrepareTarget) (dir, branch string, err error)
	CommitAll(ctx context.Context, dir, message string) (bool, error)
	Push(ctx context.Context, dir, branch string) error
	Diff(ctx context.Context, dir, base string) (string, error)
	ChangedFiles(ctx context.Context, dir, base string) ([]string, error)
	ChangedLines(ctx context.Context, dir, base string) (int, error)
	// Snapshot exports HEAD's tracked files into destDir with no.git directory
	// -- no commit history, branch name, or reflog. Used to build the
	// reviewer's isolated workspace.
	Snapshot(ctx context.Context, dir, destDir string) error
}

// TaskContext carries everything a stage may need. Stages communicate
// forward by mutating Task (persisted after every stage) and the
// scratch fields below.
type TaskContext struct {
	Task  *Task
	Repo  config.Repo
	Cfg   config.Config
	Forge Forger
	Store Store
	Trees Trees
	Agent agentrun.Runner
	// Calls starts workflow.call callees and reads them back while a
	// wait:true caller waits. Nil is only safe for a workflow with no
	// workflow.call step: such a step in a runner with no capability
	// fails the run with a named error.
	Calls task.Caller
	// PRSource fetches pull requests for the pr-review workflow. Nil fails those
	// stages.
	PRSource PRSource
	// prReview is the pr-review workflow's cross-stage scratch state (see
	// prreview_stages.go). It is unexported, following the pattern set by
	// the feasibility workflow's decision field: pipeline-private state has
	// no business being read or set outside its own stages.
	prReview *prReviewState
	Bus      *events.Bus // nil-safe via Emit
	Log      *slog.Logger
	// SkillBody is the Markdown body of the loaded SKILL.md for this
	// workflow's matching skill, injected into agent context. Empty
	// when no skill is found (backward compatible).
	SkillBody string
	// Dir/Branch are set by the prepare step.
	Dir    string
	Branch string
	// Stage is the name of the stage currently running, kept here purely so
	// park/finish can tag the event they emit with it.
	Stage string
	// StepID is the StepExecution the engine recorded for the stage whose
	// body is running: every agent call the stage makes records itself as
	// this step's child. Zero outside a stage body, which is how the stage
	// unit tests run their bodies.
	StepID int64
	// BuildSummary is the builder agent's finish summary  --  the PR body.
	BuildSummary string
	// BuildNoChanges is set when the builder returned StatusPassed but
	// made no file changes  --  the fix already exists or the issue is a
	// no-op. StageCommitPush closes the issue instead of erroring.
	BuildNoChanges bool
	// BaselineFixed is set when StageBaselineGate committed a fix, so
	// StageCommitPush pushes it even when the build changed nothing.
	BaselineFixed bool
	// ReproProof is the captured failing-test output from a TDD repro
	// stage, posted on the PR as evidence the bug was reproduced.
	ReproProof string
	// decision is the feasibility assess stage's verdict.
	decision *decision
	// reviewUnit is the remediate workflow's decoded Task.ReviewPayload,
	// stashed by its build stage so the commit-push and reply stages that
	// follow don't each re-decode the same JSON.
	reviewUnit ReviewUnit
	// Outcome describes where the task ended up; the engine applies it.
	Outcome Outcome

	// runInterface is the compiled workflow's declared interface, parsed from the
	// pinned YAML when a stage runs outside Run.
	runInterface    task.WorkflowInterface
	runInterfaceSet bool
	ifaceParsed     *task.WorkflowInterface

	// SystemPrompt, when non-nil, returns additional context to inject
	// before the agent's mission in every agent stage request. Wired by
	// the composition root (cmd/archied) from the MemoryManager.
	SystemPrompt func() string

	// Guardrails records agent tool outcomes for guardrail enforcement. Nil
	// disables it.
	Guardrails *tools.GuardrailEngine

	// RunUsage accumulates this run's token breakdown for PR bodies. Not
	// persisted; Task.TokensUsed is the total.
	RunUsage agentrun.Usage
}

// Emit publishes an observability event stamped with the task and its
// attempt. Safe on a nil bus.
func (tc *TaskContext) Emit(kind, stage, detail string, data map[string]any) {
	if tc.Bus == nil {
		return
	}
	tc.Bus.Publish(events.Event{
		Kind:     kind,
		TaskID:   tc.Task.ID,
		Repo:     tc.Task.Owner + "/" + tc.Task.Repo,
		Issue:    tc.Task.IssueNumber,
		Workflow: tc.Task.Workflow,
		Attempt:  tc.Task.Attempt,
		Stage:    stage,
		Detail:   detail,
		Data:     data,
	})
}

// EmitDurable persists evaluation-critical telemetry before publishing it to
// the lossy live bus. The assigned ID tells the daemon sink to broadcast
// without inserting a duplicate row.
func (tc *TaskContext) EmitDurable(ctx context.Context, kind, stage, detail string, data map[string]any) error {
	if tc.Store == nil {
		tc.Emit(kind, stage, detail, data)
		return nil
	}
	event := events.Event{
		At: time.Now().UTC(), Kind: kind, TaskID: tc.Task.ID, Repo: tc.Task.Owner + "/" + tc.Task.Repo,
		Issue: tc.Task.IssueNumber, Workflow: tc.Task.Workflow, Attempt: tc.Task.Attempt,
		Stage: stage, Detail: detail, Data: data,
	}
	id, err := tc.Store.InsertEvent(ctx, event)
	if err != nil {
		return err
	}
	event.ID = id
	if tc.Bus != nil {
		tc.Bus.Publish(event)
	}
	return nil
}

// toolCallReporter builds the agentrun.ToolCallReporter an agent stage passes
// to tc.Agent.Run, so every completed tool call during that run surfaces as a
// tool_call event on the task timeline. Safe to call on a nil Bus: Emit no-ops.
func (tc *TaskContext) toolCallReporter(stage string) agentrun.ToolCallReporter {
	return func(report agentrun.ToolCallReport) {
		tc.Emit(events.KindToolCall, stage, report.Detail, map[string]any{
			"tool":   report.Tool,
			"failed": report.Failed,
		})
	}
}

// Outcome is a stage's terminal decision for the whole workflow. Stages
// that don't end the workflow leave it zero.
type Outcome struct {
	Status string // Status* value; empty = continue to next stage
	Detail string // park reason / close rationale / PR body context
}

// Stage is one named step. Returning an error parks the task with the
// error text; setting ctx.Outcome ends the workflow with that status.
type Stage struct {
	Name string
	Run  func(ctx context.Context, tc *TaskContext) error
}

// Workflow is a named, ordered stage list.
type Workflow struct {
	Name   string
	Stages []Stage
	// Interface declares this workflow's inputs, repository mode and profile.
	Interface task.WorkflowInterface
}

// Registry maps workflow names to definitions.
type Registry map[string]Workflow

// Route picks the workflow for a task. A pre-assigned workflow wins
// (the waiting_human → approved handoff requeues under "implement");
// otherwise labels decide, then the default.
func Route(t *Task, reg Registry) Workflow {
	if t.Workflow != "" {
		if wf, ok := reg[t.Workflow]; ok {
			return wf
		}
		return Workflow{Name: "none", Stages: []Stage{{
			Name: "fail",
			Run: func(context.Context, *TaskContext) error {
				return fmt.Errorf("requested workflow %q is unavailable", t.Workflow)
			},
		}}}
	}
	if wf, ok := workflowForLabels(reg, t.Labels); ok {
		return wf
	}
	// No explicit workflow, no label match: a labelled task already has a free,
	// reliable signal and never reaches here. Everything else -- overwhelmingly
	// chat-spawned tasks, which rarely carry labels -- gets classified by
	// triage instead of defaulting straight to the heaviest workflow.
	if wf, ok := reg["triage"]; ok {
		return wf
	}
	if wf, ok := reg["implement"]; ok {
		return wf
	}
	if wf, ok := reg["default"]; ok {
		return wf
	}
	// Registry always ships a default; this is a config error backstop.
	return Workflow{Name: "none", Stages: []Stage{{
		Name: "fail",
		Run: func(context.Context, *TaskContext) error {
			return fmt.Errorf("no workflow registered for task")
		},
	}}}
}

// Run executes the workflow: stages in order, task persisted after each,
// errors park the task with a comment on the issue  --  never silently.
func Run(ctx context.Context, wf Workflow, tc *TaskContext) {
	t := tc.Task
	t.Workflow = wf.Name
	// The run's declared interface (outputs.go) is the compiled definition's
	// own, so every write/publish/finish check judges the YAML the task row
	// pinned.
	tc.runInterface, tc.runInterfaceSet = wf.Interface, true
	log := tc.Log.With("workflow", wf.Name, "repo", tc.Repo.FullName(), "issue", t.IssueNumber)

	for _, stage := range wf.Stages {
		tc.Stage = stage.Name
		if err := tc.Store.Update(ctx, t); err != nil {
			park(ctx, tc, fmt.Sprintf("persist task before stage %s: %v", stage.Name, err))
			return
		}
		// Record the stage as a step; a failed write parks the execution.
		stepID, startEvent, err := tc.Store.StartStep(ctx, StepStart{
			ExecutionID: t.ID, Attempt: t.Attempt, Kind: task.StepKindStage, Name: stage.Name,
		})
		if err != nil {
			park(ctx, tc, fmt.Sprintf("stage %s could not be recorded: %v", stage.Name, err))
			return
		}
		publishEvent(tc, startEvent)
		// The stage's own step is the parent of every agent or call step the
		// stage body records; zero it when the body returns, so a caller that
		// outlives the stage cannot parent to a step that has finished.
		tc.StepID = stepID
		defer func() { tc.StepID = 0 }()
		// Bind the stage name onto the logger for this stage only.
		previousLog := tc.Log
		stageLog := previousLog.With("stage", stage.Name)
		tc.Log = stageLog
		stageLog.Info("stage starting")

		err = stage.Run(ctx, tc)
		tc.Log = previousLog

		// Shutdown is not a failure: record the step as interrupted and return
		// without parking.
		if ctx.Err() != nil && err != nil {
			finishCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			finishEvent, recordErr := tc.Store.FinishStep(finishCtx, StepFinish{
				StepID: stepID, ExecutionID: t.ID,
				From: taskstate.StepRunning, To: taskstate.StepInterrupted, Detail: err.Error(),
			})
			cancel()
			if recordErr != nil {
				stageLog.Warn("interrupted step finish could not be recorded", "stage", stage.Name, "err", recordErr)
			} else {
				publishEvent(tc, finishEvent)
			}
			stageLog.Info("stage interrupted", "err", err)
			return
		}

		finishTo := taskstate.StepSucceeded
		finishDetail := ""
		if err != nil {
			finishTo = taskstate.StepFailed
			finishDetail = err.Error()
		}
		// The outcome event is the store's own stage_finish row, written in the
		// FinishStep transaction -- not a second copy emitted beside it. A
		// failed recording write parks the execution, like a failed start.
		finishEvent, recordErr := tc.Store.FinishStep(ctx, StepFinish{
			StepID: stepID, ExecutionID: t.ID,
			From: taskstate.StepRunning, To: finishTo, Detail: finishDetail,
		})
		if recordErr != nil {
			stageLog.Error("stage finish could not be recorded", "stage", stage.Name, "err", recordErr)
			park(ctx, tc, fmt.Sprintf("stage %s could not be recorded: %v", stage.Name, recordErr))
			return
		}
		publishEvent(tc, finishEvent)

		if err != nil {
			// Parking here publishes the failure; the store has already
			// recorded the step that produced it.
			t.ParkReason = fmt.Sprintf("stage %s: %v", stage.Name, err)
			park(ctx, tc, t.ParkReason)
			return
		}
		if tc.Outcome.Status != "" {
			finish(ctx, tc, log)
			return
		}
	}
	// A workflow must end with an explicit outcome; not doing so is a
	// definition bug, which still must not vanish silently.
	park(ctx, tc, "workflow ended without an outcome (definition bug)")
}

// publishEvent puts an already-persisted event on the run's bus after its
// write committed: the assigned ID is what the daemon's event sink reads to
// skip the second insert, the same convention EmitDurable established.
func publishEvent(tc *TaskContext, e events.Event) {
	if tc.Bus == nil || e.ID == 0 {
		return
	}
	tc.Bus.Publish(e)
}

func finish(ctx context.Context, tc *TaskContext, log *slog.Logger) {
	t := tc.Task
	if tc.Outcome.Status == StatusParked {
		park(ctx, tc, tc.Outcome.Detail)
		return
	}
	// The attempt's declared outputs are validated before the outcome
	// transition: an undeclared, mistyped or required-but-missing value
	// parks the run here, so no caller observes a terminal state that
	// breaks the workflow's own promise
	if err := tc.validateFinishOutputs(); err != nil {
		park(ctx, tc, err.Error())
		return
	}
	if err := tc.Store.Update(ctx, t); err != nil {
		park(ctx, tc, fmt.Sprintf("persist workflow outcome: %v", err))
		return
	}
	if err := tc.Store.Transition(ctx, t.ID, StatusRunning, tc.Outcome.Status, tc.Outcome.Detail); err != nil {
		log.Error("workflow outcome transition failed", "err", err)
		return
	}
	tc.Emit(events.KindOutcome, tc.Stage, tc.Outcome.Detail, map[string]any{"status": tc.Outcome.Status})
	log.Info("workflow finished", "status", tc.Outcome.Status)
}

func park(ctx context.Context, tc *TaskContext, reason string) {
	t := tc.Task
	t.ParkReason = reason
	if err := tc.Store.Update(ctx, t); err != nil {
		tc.Log.Error("task fields could not be persisted while parking", "err", err)
	}
	if err := tc.Store.Transition(ctx, t.ID, StatusRunning, StatusParked, reason); err != nil {
		tc.Log.Error("task park transition failed", "err", err)
		return
	}
	tc.Emit(events.KindParked, tc.Stage, reason, nil)
	// The run's own log is the surface an operator downloads to answer "why did
	// this park?", and the event above lands on the timeline, which the log
	// never carries. This is the one place a park's reason is final, so it is
	// the one place that records it -- the stage names itself in the reason.
	tc.Log.Error("task parked", "stage", tc.Stage, "reason", reason)
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "\n…(truncated)"
}

// clipTail keeps the last n bytes of s, cut on a rune boundary.
func clipTail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := len(s) - n
	for cut < len(s) && !utf8.RuneStart(s[cut]) {
		cut++
	}
	return "…(truncated)\n" + s[cut:]
}

// formatTokenUsage renders a token total split into cached and fresh tokens,
// or the plain total when no breakdown exists.
func formatTokenUsage(total int, usage agentrun.Usage) string {
	if usage.PromptTokens == 0 && usage.CompletionTokens == 0 && usage.CachedTokens == 0 {
		return fmt.Sprintf("%d tokens", total)
	}
	fresh := max(usage.PromptTokens-usage.CachedTokens+usage.CompletionTokens, 0)
	return fmt.Sprintf("%d tokens (%d fresh + %d cached)", total, fresh, usage.CachedTokens)
}

// extractFailingGateOutput drops passing-package "ok" lines from go test
// output when it contains failure markers; other output is returned
// unchanged.
func extractFailingGateOutput(s string) string {
	lines := strings.Split(s, "\n")
	kept := make([]string, 0, len(lines))
	sawFailureMarker := false
	for _, line := range lines {
		if goTestOKLine.MatchString(line) {
			continue
		}
		kept = append(kept, line)
		if strings.HasPrefix(line, "--- FAIL:") || strings.HasPrefix(line, "FAIL\t") || strings.HasPrefix(line, "FAIL ") {
			sawFailureMarker = true
		}
	}
	if !sawFailureMarker {
		return s
	}
	return strings.Join(kept, "\n")
}

// goTestOKLine matches a `go test` passing-package summary line, e.g.
// "ok  \tgithub.com/example/pkg\t0.004s".
var goTestOKLine = regexp.MustCompile(`^ok\s+\S+`)

// RunAgentChild records one agent call as a child step of the current stage.
// A failed write parks the execution. Outside a recorded run it just runs.
func (tc *TaskContext) RunAgentChild(ctx context.Context, name string, run func() (agentrun.Result, error)) (agentrun.Result, error) {
	stepID, _, err := tc.startChildStep(ctx, task.StepKindAgent, name)
	if err != nil {
		return agentrun.Result{}, err
	}
	res, runErr := run()
	to, detail := taskstate.StepSucceeded, res.Summary
	if runErr != nil {
		to, detail = taskstate.StepFailed, runErr.Error()
	}
	if ferr := tc.finishChildStep(ctx, stepID, to, detail, int64(res.TokensUsed)); ferr != nil {
		return res, ferr
	}
	return res, runErr
}

// startChildStep records one agent or call step under the stage step this
// run is executing. A run with no recorded stage step records nothing and
// reports step id 0 without an error, which is how the stage unit tests keep
// running their bodies exactly as they did before the record existed.
func (tc *TaskContext) startChildStep(ctx context.Context, kind, name string) (int64, events.Event, error) {
	if tc.Store == nil || tc.StepID == 0 {
		return 0, events.Event{}, nil
	}
	stepID, event, err := tc.Store.StartStep(ctx, task.StepStart{
		ExecutionID: tc.Task.ID, Attempt: tc.Task.Attempt, ParentID: tc.StepID, Kind: kind, Name: name,
	})
	if err != nil {
		return 0, events.Event{}, err
	}
	publishEvent(tc, event)
	return stepID, event, nil
}

// finishChildStep closes a child step and publishes the transition's
// persisted event, the same post-commit convention the stage steps use.
func (tc *TaskContext) finishChildStep(ctx context.Context, stepID int64, to taskstate.StepStatus, detail string, tokensUsed int64) error {
	if stepID == 0 {
		return nil
	}
	event, err := tc.Store.FinishStep(ctx, task.StepFinish{
		StepID: stepID, ExecutionID: tc.Task.ID,
		From: taskstate.StepRunning, To: to, Detail: detail, TokensUsed: tokensUsed,
	})
	if err != nil {
		return err
	}
	publishEvent(tc, event)
	return nil
}
