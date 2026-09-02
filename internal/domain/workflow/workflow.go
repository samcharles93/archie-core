// Package workflow is archied's extensible pipeline engine. A Workflow
// is an ordered list of stages over a shared TaskContext; stages are
// either deterministic steps (git, gate, PR, comments) or agent stages
// (agentloop runs). New workflows compose from the shared step library  --
// adding one must never require reimplementing the engine or the steps.
package workflow

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/samcharles93/archie-core/internal/agentexec"
	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/events"
	"github.com/samcharles93/archie-core/internal/skill"
	"github.com/samcharles93/archie-core/internal/store"
	"github.com/samcharles93/archie-core/internal/tools"
)

// Forger is the subset of forge.Forge that workflow stages call mid-run.
// forge.Forge (the daemon's real implementations) and forgerpc.Client
// (archie-agent's NATS-backed proxy) both satisfy it.
type Forger interface {
	CloseIssue(ctx context.Context, owner, repo string, number int, comment string) error
	CreatePR(ctx context.Context, owner, repo, title, head, base, body string) (int, error)
	LinkBranch(ctx context.Context, owner, repo string, issueNumber int, branch string) error
}

// Trees is the subset of *worktree.Manager that workflow stages call
// mid-run. *worktree.Manager (the daemon's real manager, holding the push
// token) and a hybrid RPC-backed implementation (archie-agent proxies
// Prepare/Push, runs the rest locally) both satisfy it.
type Trees interface {
	Prepare(ctx context.Context, owner, repo, base string, issue int, title, body, labels string) (dir, branch string, err error)
	CommitAll(ctx context.Context, dir, message string) (bool, error)
	Push(ctx context.Context, dir, branch string) error
	Diff(ctx context.Context, dir, base string) (string, error)
	ChangedFiles(ctx context.Context, dir, base string) ([]string, error)
	ChangedLines(ctx context.Context, dir, base string) (int, error)
	// Snapshot exports HEAD's tracked files into destDir with no .git
	// directory -- no commit history, branch name, or reflog. Used to
	// build the adversarial reviewer's isolated workspace (see
	// docs/prds/adversarial-self-review.md).
	Snapshot(ctx context.Context, dir, destDir string) error
}

// TaskContext carries everything a stage may need. Stages communicate
// forward by mutating Task (persisted after every stage) and the
// scratch fields below.
type TaskContext struct {
	Task  *store.Task
	Repo  config.Repo
	Cfg   config.Config
	Forge Forger
	Store store.WorkflowStore
	Trees Trees
	Agent agentexec.Runner
	// Reviewer runs the adversarial self-review stage (StageReview). Nil
	// is only safe when Repo.ReviewEnabled is false; StageReview parks
	// rather than silently skipping if it is enabled with no Reviewer
	// wired.
	Reviewer Reviewer
	Bus      *events.Bus // nil-safe via Emit
	Log      *slog.Logger
	// CustomStages discovers a repo's per-repo custom stages (Yaegi-
	// interpreted from .archie/stages/*.go in the given worktree
	// directory), returning them in the order they should run. Application
	// composition injects it; nil disables discovery, so StageRepoStages is a
	// no-op. The composition layer provides the implementation rather
	// than importing the generated Yaegi symbol table here, which would create
	// a workflow package import cycle.
	CustomStages func(dir string) ([]Stage, error)

	// SkillBody is the Markdown body of the loaded SKILL.md for this
	// workflow's matching skill, injected into agent context. Empty
	// when no skill is found (backward compatible).
	SkillBody string
	// SkillPlugins holds the bundled Yaegi plugins loaded from the
	// skill's plugins/ directory. Populated by loadSkillBody alongside
	// SkillBody. Nil when no skill is loaded or the skill has no plugins.
	SkillPlugins []skill.Plugin
	// Dir/Branch are set by the prepare step.
	Dir    string
	Branch string
	// BuildSummary is the builder agent's finish summary  --  the PR body.
	BuildSummary string
	// BuildNoChanges is set when the builder returned StatusPassed but
	// made no file changes  --  the fix already exists or the issue is a
	// no-op. StageCommitPush closes the issue instead of erroring.
	BuildNoChanges bool
	// BaselineFixed is set when StageBaselineGate committed a real fix for
	// a pre-existing gate failure. It gates whether the build stage is
	// allowed to set BuildNoChanges: a baseline-fix commit is a real,
	// gate-verified change sitting in the worktree, so even if the actual
	// task needed no further work, StageCommitPush must still push and
	// open a PR rather than discarding it as "no changes required".
	BaselineFixed bool
	// ReproProof is the captured failing-test output from a TDD repro
	// stage, posted on the PR as evidence the bug was reproduced.
	ReproProof string
	// decision is the feasibility assess stage's verdict.
	decision *decision
	// Outcome describes where the task ended up; the engine applies it.
	Outcome Outcome

	// SystemPrompt, when non-nil, returns additional context to inject
	// before the agent's mission in every agent stage request. Wired by
	// the composition root (cmd/archied) from the MemoryManager.
	SystemPrompt func() string

	// Guardrails is the per-task guardrail engine reference. When non-nil,
	// agent stages record tool successes and failures for guardrail
	// enforcement. Wired by the composition root from the daemon.
	Guardrails *tools.GuardrailEngine

	// RunUsage accumulates every agent run's token breakdown (prompt,
	// completion, cached) for this workflow execution. It is presentation
	// scratch, not persisted -- Task.TokensUsed remains the durable,
	// authoritative total. It exists so PR bodies can show how much of the
	// reported prompt-token sum was cache hits (billed at a steep discount)
	// rather than fresh, full-price tokens; see formatTokenUsage.
	RunUsage agentexec.Usage
}

// Emit publishes an observability event stamped with the task's
// identity. Safe on a nil bus.
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
		Issue: tc.Task.IssueNumber, Workflow: tc.Task.Workflow, Stage: stage, Detail: detail, Data: data,
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

// toolCallReporter builds the agentexec.ToolCallReporter an agent stage
// passes to tc.Agent.Run, so every completed tool call during that run
// surfaces as a tool_call event on the task timeline (archie-core-467's
// task-transcript counterpart -- the interactive chat gateway has its own,
// separate ToolCallEvent). Safe to call on a nil Bus: Emit no-ops.
func (tc *TaskContext) toolCallReporter(stage string) agentexec.ToolCallReporter {
	return func(report agentexec.ToolCallReport) {
		tc.Emit(events.KindToolCall, stage, report.Detail, map[string]any{
			"tool":   report.Tool,
			"failed": report.Failed,
		})
	}
}

// Outcome is a stage's terminal decision for the whole workflow. Stages
// that don't end the workflow leave it zero.
type Outcome struct {
	Status string // store.Status* value; empty = continue to next stage
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
}

// Registry maps workflow names to definitions.
type Registry map[string]Workflow

// Definition is the operator-safe snapshot of an executable workflow. It
// contains only identity and stage order, never executable function values.
type Definition struct {
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	Origin  string   `json:"origin"`
	Enabled bool     `json:"enabled"`
	Stages  []string `json:"stages"`
}

// Definitions snapshots a registry deterministically so installed workflows
// remain visible even when they have not produced persisted run statistics.
func Definitions(reg Registry) []Definition {
	return DefinitionsWithOrigins(reg, nil)
}

// DefinitionsWithOrigins snapshots a registry with composition-supplied
// provenance. Entries absent from origins are conservatively labelled
// "registry", which keeps the helper useful for tests and non-catalog callers.
func DefinitionsWithOrigins(reg Registry, origins map[string]string) []Definition {
	ids := make([]string, 0, len(reg))
	for id := range reg {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	definitions := make([]Definition, 0, len(ids))
	for _, id := range ids {
		wf := reg[id]
		stages := make([]string, 0, len(wf.Stages))
		for _, stage := range wf.Stages {
			stages = append(stages, stage.Name)
		}
		origin := origins[id]
		if origin == "" {
			origin = "registry"
		}
		definitions = append(definitions, Definition{ID: id, Name: wf.Name, Origin: origin, Enabled: true, Stages: stages})
	}
	return definitions
}

// Route picks the workflow for a task. A pre-assigned workflow wins
// (the waiting_human → approved handoff requeues under "implement");
// otherwise labels decide, then the default.
func Route(t *store.Task, reg Registry) Workflow {
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
	// No explicit workflow, no label match: a labelled task already has a
	// free, reliable signal and never reaches here. Everything else --
	// overwhelmingly chat-spawned tasks, which rarely carry labels -- gets
	// classified by triage instead of defaulting straight to the heaviest
	// workflow. See docs/prds/dynamic-workflow-triage.md.
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
	log := tc.Log.With("workflow", wf.Name, "repo", tc.Repo.FullName(), "issue", t.IssueNumber)

	for _, stage := range wf.Stages {
		t.Stage = stage.Name
		_ = tc.Store.Update(ctx, t)
		log.Info("stage starting", "stage", stage.Name)
		tc.Emit(events.KindStageStart, stage.Name, "", nil)
		started := time.Now()

		err := stage.Run(ctx, tc)
		data := map[string]any{"duration_ms": time.Since(started).Milliseconds()}
		if err != nil {
			data["error"] = err.Error()
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				data["interrupted"] = true
			}
		}
		tc.Emit(events.KindStageFinish, stage.Name, "", data)

		if err != nil {
			// Daemon shutdown is not a workflow failure. Leave the task running
			// so Startup's existing crash recovery requeues it; parking here
			// would publish a false failure and require manual intervention.
			if ctx.Err() != nil {
				log.Info("stage interrupted", "stage", stage.Name, "err", err)
				return
			}
			t.ParkReason = fmt.Sprintf("stage %s: %v", stage.Name, err)
			log.Warn("stage failed  --  parking", "stage", stage.Name, "err", err)
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

func finish(ctx context.Context, tc *TaskContext, log *slog.Logger) {
	t := tc.Task
	if tc.Outcome.Status == store.StatusParked {
		park(ctx, tc, tc.Outcome.Detail)
		return
	}
	_ = tc.Store.Update(ctx, t)
	_ = tc.Store.Transition(ctx, t.ID, store.StatusRunning, tc.Outcome.Status, tc.Outcome.Detail)
	tc.Emit(events.KindOutcome, t.Stage, tc.Outcome.Detail, map[string]any{"status": tc.Outcome.Status})
	log.Info("workflow finished", "status", tc.Outcome.Status)
}

func park(ctx context.Context, tc *TaskContext, reason string) {
	t := tc.Task
	t.ParkReason = reason
	_ = tc.Store.Update(ctx, t)
	_ = tc.Store.Transition(ctx, t.ID, store.StatusRunning, store.StatusParked, reason)
	tc.Emit(events.KindParked, t.Stage, reason, nil)
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "\n…(truncated)"
}

// clipTail keeps the last n bytes of s, breaking on a rune boundary so a
// multi-byte character straddling the cut point isn't split into invalid
// UTF-8 -- this return value can be persisted (e.g. into a task's park
// reason), not just logged, so mangled bytes would survive past this call.
//
// Head-clipping (clip, above) is right when the useful part comes first,
// such as a compiler error. It is wrong for command output where the
// explanation is at the end, such as a test runner's "--- FAIL" block after
// pages of "ok" lines -- clip would keep exactly the part that doesn't say
// why it failed.
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

// formatTokenUsage renders a token total for a human, breaking out how much
// was a cache hit (billed at a steep discount) versus fresh, full-price
// tokens. Without this, a heavily-cached run's raw prompt-token sum reads as
// roughly 10x its actual bill.
//
// It falls back to the plain total when no usage breakdown is available
// (usage is the zero value -- e.g. a workflow that never ran an agent stage,
// or an older code path that hasn't been wired to populate RunUsage), so
// callers never need to special-case an empty Usage themselves.
func formatTokenUsage(total int, usage agentexec.Usage) string {
	if usage.PromptTokens == 0 && usage.CompletionTokens == 0 && usage.CachedTokens == 0 {
		return fmt.Sprintf("%d tokens", total)
	}
	fresh := max(usage.PromptTokens-usage.CachedTokens+usage.CompletionTokens, 0)
	return fmt.Sprintf("%d tokens (%d fresh + %d cached)", total, fresh, usage.CachedTokens)
}

// extractFailingGateOutput trims a gate command's combined output down to
// the lines relevant to diagnosing a failure, dropping "ok" lines for
// packages that already pass. `go test ./...` output interleaves one
// "ok  \t<pkg>\t<time>" line per passing package with the failing packages'
// "--- FAIL:"/"FAIL\t<pkg>" blocks; in a large repo the passing-package
// lines dominate the byte count and get squeezed out by clip's size bound,
// pushing the actual failure detail out of the mission prompt on later
// retries.
//
// It only trims when the output actually looks like recognized `go test`
// failure output (at least one "--- FAIL:" or "FAIL\t" marker survives the
// filter); otherwise it returns s unchanged, so a non-Go gate command or an
// unexpected output shape degrades to the previous full-output behaviour
// instead of silently dropping real failure information.
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
