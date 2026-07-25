package workflow

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/samcharles93/archie-core/internal/agentexec"
	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/skill"
)

// AgentStage is the reusable bridge from a workflow stage to an
// agentloop run. Every LLM-driven stage in any workflow (implement's
// planner/builder, tdd's test-writer/fixer, feasibility's analyst)
// is an AgentStage with a different mission, gate, and result handler  -- 
// never a new engine.
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
	Gate func(*TaskContext) agentexec.Gate
	// ExtraRules is appended to the system prompt.
	ExtraRules string
	// MaxSteps overrides the configured step budget when > 0 (planner
	// stages are cheaper than builder stages).
	MaxSteps int
	// ProtectGlobs blocks write/edit on matching paths for this stage  -- 
	// an environmental constraint, not a prompt rule (TDD's fix stage
	// protects the committed repro tests; every builder stage protects
	// the repo's generated files). The returned globs are combined
	// with the repo's configured protected suffixes.
	ProtectGlobs func(*TaskContext) []string
	// CaptureTools adds structured-output tools whose calls are returned
	// as data rather than receiving callbacks into daemon state.
	CaptureTools func(*TaskContext) []agentexec.CaptureTool
	// OnResult consumes a successful (passed) result. Parked and idle
	// results park the workflow before OnResult is called.
	OnResult func(*TaskContext, agentexec.Result) error
	// ReviewResult gates agent output before OnResult forwards it to
	// human channels. The daemon calls this hook to review stage output
	// (issue comments, PR bodies) before human delivery. Return an
	// error to block the stage. Nil means pass-through  --  no review.
	// PRD §1: daemon reviews agent responses before forwarding.
	ReviewResult func(*TaskContext, agentexec.Result) error
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
		modelRef := tc.Cfg.Models[a.Role]
		if modelRef == "" {
			modelRef = tc.Cfg.Models["builder"]
		}
		if modelRef == "" {
			return fmt.Errorf("no model configured for role %q (set [models] in config)", a.Role)
		}

		budget := agentexec.Budget{
			MaxSteps:  tc.Cfg.Budgets.MaxSteps,
			MaxTokens: tc.Cfg.Budgets.MaxTokens,
			WallClock: tc.Cfg.Budgets.WallClock.Std(),
		}
		if a.MaxSteps > 0 {
			budget.MaxSteps = a.MaxSteps
		}

		var gate agentexec.Gate
		if a.Gate != nil {
			gate = a.Gate(tc)
		}
		var captureTools []agentexec.CaptureTool
		if a.CaptureTools != nil {
			captureTools = a.CaptureTools(tc)
		}

		protection := agentexec.Protection{Suffixes: append([]string(nil), tc.Repo.Protect...)}
		if a.ProtectGlobs != nil {
			protection.Globs = a.ProtectGlobs(tc)
		}
		if a.ReadOnly {
			protection = agentexec.Protection{}
		}

		var preflight []agentexec.Command
		for _, argv := range tc.Repo.ResolvedPreflight() {
			if len(argv) == 0 {
				continue
			}
			preflight = append(preflight, agentexec.Command{Name: argv[0], Argv: argv})
		}

		req := agentexec.Request{
			Version:      agentexec.ProtocolVersion,
			TaskID:       tc.Task.ID,
			Attempt:      tc.Task.Attempt,
			Stage:        a.Name,
			Workflow:     tc.Task.Workflow,
			Model:        modelRef,
			Mission:      missionWithSkill(tc, a.Mission(tc)),
			ExtraRules:   a.ExtraRules,
			ReadOnly:     a.ReadOnly,
			Budget:       budget,
			Gate:         gate,
			Preflight:    preflight,
			Protection:   protection,
			Notes:        tc.Task.Notes,
			CaptureTools: captureTools,
			Plugins:      pluginSpecs(tc.SkillPlugins),
		}
		res, err := tc.Agent.Run(ctx, tc.Dir, req)
		if err != nil && res.Version == 0 {
			return fmt.Errorf("agent run: %w", err)
		}
		if validateErr := res.ValidateFor(req); validateErr != nil {
			return fmt.Errorf("validate agent result: %w", validateErr)
		}
		if len(res.AppendedNotes) > 0 {
			for _, note := range res.AppendedNotes {
				tc.Task.Notes += "- " + note + "\n"
			}
			if saveErr := tc.Store.Update(ctx, tc.Task); saveErr != nil {
				return fmt.Errorf("persist agent notes: %w", saveErr)
			}
		}
		if err != nil {
			return fmt.Errorf("agent run: %w", err)
		}

		tc.Task.TokensUsed += res.TokensUsed
		tc.Task.Iterations += res.Iterations
		tc.Emit("agent_finish", a.Name, res.Summary, map[string]any{
			"status": string(res.Status), "stop_reason": res.StopReason,
			"tokens": res.TokensUsed, "iterations": res.Iterations, "model": modelRef,
		})

		if res.Status != agentexec.StatusPassed {
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
		if a.OnResult != nil {
			return a.OnResult(tc, res)
		}
		return nil
	}}
}

// GateFromRepo converts the repo's configured command lists into an
// agent execution gate.
func GateFromRepo(repo config.Repo, budgets config.Budgets) agentexec.Gate {
	cmds := make([]agentexec.Command, 0, len(repo.Gate))
	for _, argv := range repo.Gate {
		if len(argv) == 0 {
			continue
		}
		cmds = append(cmds, agentexec.Command{Name: argv[0], Argv: argv})
	}
	return agentexec.Gate{Commands: cmds, MaxConsecutiveFailures: budgets.GateMaxFailures}
}

// pluginSpecs converts skill.Plugin to agentexec.PluginSpec for transport.
func pluginSpecs(plugins []skill.Plugin) []agentexec.PluginSpec {
	if len(plugins) == 0 {
		return nil
	}
	out := make([]agentexec.PluginSpec, len(plugins))
	for i, p := range plugins {
		out[i] = agentexec.PluginSpec{Name: p.Name, Src: p.Src}
	}
	return out
}

// missionWithSkill prepends the skill body (loaded from SKILL.md) to the
// stage's mission. When no skill is loaded, the mission is returned unchanged.
func missionWithSkill(tc *TaskContext, mission string) string {
	if tc.SkillBody == "" {
		return mission
	}
	return "Follow these project-specific guidelines:\n\n" + tc.SkillBody + "\n\n---\n\n" + mission
}

// loadSkillBody loads the SKILL.md body and plugins for the current
// workflow from the worktree's .agents/skills/ directory. Skills declare
// their workflow in metadata.archie.workflow  --  no hardcoded mapping.
// When metadata.archie.plugins is non-empty, only the listed plugin files
// are loaded in declared order; otherwise all *.go files are globbed.
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
	if body != "" && tc.SkillPlugins == nil {
		var pluginNames []string
		if fm.Metadata.Archie != nil {
			pluginNames = fm.Metadata.Archie.Plugins
		}
		plugins, _ := skill.LoadPlugins(tc.Dir, entry.Dir, pluginNames)
		tc.SkillPlugins = plugins
	}
	return body
}
