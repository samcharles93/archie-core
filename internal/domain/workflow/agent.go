package workflow

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/domain/agentrun"
	"github.com/samcharles93/archie-core/internal/events"
	"github.com/samcharles93/archie-core/internal/skill"
)

// AgentStage runs one LLM-driven workflow stage on the agent runtime with its
// own mission, gate and result handler.
type AgentStage struct {
	Name string
	// Role selects the model via cfg.Models[Role]; falls back to
	// cfg.Models["builder"].
	Role     string
	ReadOnly bool
	// Mission produces the task statement from the current context.
	Mission func(*TaskContext) string
	// Gate returns the stage's quality gate; nil means ungated (e.g.
	// read-only analysis stages).
	Gate func(*TaskContext) agentrun.Gate
	// ExtraRules is appended to the system prompt.
	ExtraRules string
	// MaxSteps overrides the configured step budget when > 0 (planner
	// stages are cheaper than builder stages).
	MaxSteps int
	// ProtectGlobs blocks write/edit on matching paths for this stage, in
	// addition to the repo's protected suffixes.
	ProtectGlobs func(*TaskContext) []string
	// CaptureTools adds structured-output tools whose calls are returned
	// as data rather than receiving callbacks into daemon state.
	CaptureTools func(*TaskContext) []agentrun.CaptureTool
	// OnResult consumes a successful (passed) result. Parked and idle
	// results park the workflow before OnResult is called.
	OnResult func(*TaskContext, agentrun.Result) error
	// ReviewResult gates agent output before OnResult forwards it to human
	// channels. The daemon calls this hook to review stage output (issue
	// comments, PR bodies) before human delivery. Return an error to block the
	// stage. Nil means pass-through -- no review.
	ReviewResult func(*TaskContext, agentrun.Result) error
}

// Stage adapts the AgentStage to the engine.
func (a AgentStage) Stage() Stage {
	return Stage{Name: a.Name, Run: func(ctx context.Context, tc *TaskContext) error {
		// Lazy-load the skill body from the worktree (dir is set by the
		// prepare stage, which runs before any agent stage). If the body
		// was already loaded by the daemon, this is a no-op.
		if tc.SkillBody == "" && tc.Dir != "" {
			tc.SkillBody = loadSkillBody(tc)
		}
		if tc.Agent == nil {
			return fmt.Errorf("no LLM runtime configured (missing [providers] in config?)")
		}
		modelRef, err := a.resolveModel(tc)
		if err != nil {
			return err
		}
		req, buildErr := a.buildRequest(tc, modelRef)
		if buildErr != nil {
			return buildErr
		}
		// The agent call is a child of the stage's recorded step: the store
		// says what the stage's runtime did, not just what the stage did.
		res, runErr := tc.RunAgentChild(ctx, a.Name, func() (agentrun.Result, error) {
			return tc.Agent.Run(ctx, tc.Dir, req, tc.toolCallReporter(a.Name))
		})
		return a.handleResult(ctx, tc, req, res, runErr, modelRef)
	}}
}

// resolveModel picks the model reference for this stage's Role, falling
// back to "builder".
func (a AgentStage) resolveModel(tc *TaskContext) (string, error) {
	modelRef := tc.Cfg.Models[a.Role]
	if modelRef == "" {
		modelRef = tc.Cfg.Models["builder"]
	}
	if modelRef == "" {
		return "", fmt.Errorf("no model configured for role %q (set [models] in config)", a.Role)
	}
	return modelRef, nil
}

// buildRequest assembles the agentrun.Request for this stage's run.
func (a AgentStage) buildRequest(tc *TaskContext, modelRef string) (agentrun.Request, error) {
	budget := agentrun.Budget{
		MaxSteps:  tc.Cfg.Budgets.MaxSteps,
		WallClock: tc.Cfg.Budgets.WallClock.Std(),
	}
	if a.MaxSteps > 0 {
		budget.MaxSteps = a.MaxSteps
	}

	var gate agentrun.Gate
	if a.Gate != nil {
		gate = a.Gate(tc)
	}
	var captureTools []agentrun.CaptureTool
	if a.CaptureTools != nil {
		captureTools = a.CaptureTools(tc)
	}
	captureTools, err := tc.appendOutputTools(captureTools)
	if err != nil {
		return agentrun.Request{}, err
	}

	protection := agentrun.Protection{Suffixes: append([]string(nil), tc.Repo.Protect...)}
	if a.ProtectGlobs != nil {
		protection.Globs = a.ProtectGlobs(tc)
	}
	if a.ReadOnly {
		protection = agentrun.Protection{}
	}

	var preflight []agentrun.Command
	for _, argv := range tc.Repo.ResolvedPreflight() {
		if len(argv) == 0 {
			continue
		}
		preflight = append(preflight, agentrun.Command{Name: argv[0], Argv: argv})
	}

	return agentrun.Request{
		Version:       agentrun.ProtocolVersion,
		TaskID:        tc.Task.ID,
		Attempt:       tc.Task.Attempt,
		Stage:         a.Name,
		Workflow:      tc.Task.Workflow,
		Model:         modelRef,
		ContextWindow: modelContextBudget(tc.Cfg, modelRef),
		Mission:       missionWithInputs(tc.Task, missionWithSkill(tc, a.Mission(tc))),
		ExtraRules:    a.buildExtraRules(tc),
		ReadOnly:      a.ReadOnly,
		Budget:        budget,
		Gate:          gate,
		Preflight:     preflight,
		Protection:    protection,
		Notes:         tc.Task.Notes,
		CaptureTools:  captureTools,
	}, nil
}

// buildExtraRules prepends memory context, if the daemon wired a memory
// manager, ahead of the stage's own rules.
func (a AgentStage) buildExtraRules(tc *TaskContext) string {
	if tc.SystemPrompt == nil {
		return a.ExtraRules
	}
	memCtx := tc.SystemPrompt()
	if memCtx == "" {
		return a.ExtraRules
	}
	if a.ExtraRules == "" {
		return memCtx
	}
	return memCtx + "\n\n" + a.ExtraRules
}

// handleResult records guardrail state, validates and persists the agent
// result, then dispatches to ReviewResult/OnResult on success.
func (a AgentStage) handleResult(
	ctx context.Context, tc *TaskContext, req agentrun.Request, res agentrun.Result, runErr error, modelRef string,
) error {
	a.recordGuardrails(tc, res, runErr)

	if runErr != nil && res.Version == 0 {
		return fmt.Errorf("agent run: %w", runErr)
	}
	if validateErr := res.ValidateFor(req); validateErr != nil {
		return fmt.Errorf("validate agent result: %w", validateErr)
	}
	if err := persistAppendedNotes(ctx, tc, res); err != nil {
		return err
	}
	tc.Task.TokensUsed += res.TokensUsed
	tc.Task.Iterations += res.Iterations
	accumulateUsage(&tc.RunUsage, res.Usage)
	if emitErr := tc.EmitDurable(ctx, events.KindAgentFinish, a.Name, res.Summary, agentFinishData(res, modelRef)); emitErr != nil {
		return fmt.Errorf("persist agent finish: %w", emitErr)
	}
	if runErr != nil {
		return fmt.Errorf("agent run: %w", runErr)
	}
	return a.deliverResult(tc, res)
}

// recordGuardrails feeds this run's outcome to the guardrail engine: on
// success it checks no-progress thresholds, on failure it records the
// error pattern.
func (a AgentStage) recordGuardrails(tc *TaskContext, res agentrun.Result, runErr error) {
	if tc.Guardrails == nil {
		return
	}
	if runErr == nil && res.Status == agentrun.StatusPassed {
		tc.Guardrails.RecordSuccess("agent:" + a.Name)
	} else if runErr != nil {
		tc.Guardrails.RecordFailure("agent:"+a.Name, runErr)
	}
}

// persistAppendedNotes saves any notes the agent appended during the run.
func persistAppendedNotes(ctx context.Context, tc *TaskContext, res agentrun.Result) error {
	if len(res.AppendedNotes) == 0 {
		return nil
	}
	for _, note := range res.AppendedNotes {
		tc.Task.Notes += "- " + note + "\n"
	}
	if saveErr := tc.Store.Update(ctx, tc.Task); saveErr != nil {
		return fmt.Errorf("persist agent notes: %w", saveErr)
	}
	return nil
}

// deliverResult checks the run passed, then runs ReviewResult and OnResult.
func (a AgentStage) deliverResult(tc *TaskContext, res agentrun.Result) error {
	if res.Status != agentrun.StatusPassed {
		detail := res.Detail
		if detail == "" {
			detail = res.Summary
		}
		return fmt.Errorf("agent %s (%s): %s", res.Status, res.StopReason, clip(detail, 2000))
	}
	// Gatekeeping: the daemon reviews agent output before human delivery.
	if a.ReviewResult != nil {
		if err := a.ReviewResult(tc, res); err != nil {
			return fmt.Errorf("review: %w", err)
		}
	}
	if err := tc.applyOutputCaptures(res); err != nil {
		return err
	}
	if a.OnResult != nil {
		return a.OnResult(tc, res)
	}
	return nil
}

func modelContextBudget(cfg config.Config, modelRef string) int {
	limits := cfg.ModelLimits[modelRef]
	if limits.ContextWindow <= limits.MaxOutputTokens {
		return 0
	}
	return limits.ContextWindow - limits.MaxOutputTokens
}

// accumulateUsage adds a single agent run's token breakdown onto a
// workflow-run running total. Extracted so every accumulation site (agent.go
// and implement.go's baseline-fix repair loop) stays in sync.
func accumulateUsage(total *agentrun.Usage, delta agentrun.Usage) {
	total.PromptTokens += delta.PromptTokens
	total.CompletionTokens += delta.CompletionTokens
	total.TotalTokens += delta.TotalTokens
	total.CachedTokens += delta.CachedTokens
	total.CacheCreationTokens += delta.CacheCreationTokens
}

func agentFinishData(res agentrun.Result, modelRef string) map[string]any {
	return map[string]any{
		"status": res.Status, "stop_reason": res.StopReason,
		"tokens": res.TokensUsed, "iterations": res.Iterations, "model": modelRef,
		"prompt_tokens": res.Usage.PromptTokens, "completion_tokens": res.Usage.CompletionTokens,
		"cached_tokens": res.Usage.CachedTokens, "cache_creation_tokens": res.Usage.CacheCreationTokens,
	}
}

// GateFromRepo converts the repo's configured command lists into an
// agent execution gate.
func GateFromRepo(repo config.Repo, budgets config.Budgets) agentrun.Gate {
	cmds := make([]agentrun.Command, 0, len(repo.Gate))
	for _, argv := range repo.Gate {
		if len(argv) == 0 {
			continue
		}
		cmds = append(cmds, agentrun.Command{Name: argv[0], Argv: argv})
	}
	return agentrun.Gate{Commands: cmds, MaxConsecutiveFailures: budgets.GateMaxFailures}
}

// missionWithSkill prepends the skill body (loaded from SKILL.md) to the
// stage's mission. When no skill is loaded, the mission is returned unchanged.
func missionWithSkill(tc *TaskContext, mission string) string {
	if tc.SkillBody == "" {
		return mission
	}
	return "Follow these project-specific guidelines:\n\n" + tc.SkillBody + "\n\n---\n\n" + mission
}

// loadSkillBody loads the SKILL.md body for the current workflow, matched by
// metadata.archie.workflow.
func loadSkillBody(tc *TaskContext) string {
	catalog, _ := skill.Catalog(tc.Dir)
	entry := skill.SkillForWorkflow(catalog, tc.Task.Workflow)
	if entry == nil {
		return ""
	}
	// Read and parse SKILL.md once to get both body and frontmatter.
	skillPath := filepath.Join(tc.Dir, ".agents", "skills", entry.Dir, "SKILL.md")
	data, err := os.ReadFile(skillPath)
	if err != nil {
		return ""
	}
	fm, body, err := skill.Parse(data)
	if err != nil || fm == nil {
		return ""
	}
	body = strings.TrimSpace(body)
	return body
}
