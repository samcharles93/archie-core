package workflow

import (
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/samcharles93/archie-core/internal/agentexec"
	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/store"
)

// ── regression: Gap 7  --  plugins discovered but not executed ─────────

func TestSkillPluginsAvailableDuringStageExecution(t *testing.T) {
	// Gap 7: skill.Discover() populates Skill.Plugins but nothing calls
	// plugin.Run() during stage execution. Plugins must be loaded from
	// the skill directory alongside the SKILL.md body and made available
	// to the agent as tools during stage runs.
	//
	// This test creates a worktree with a skill that has a bundled plugin.
	// It runs an AgentStage and verifies the plugin was loaded and is
	// accessible via the TaskContext.
	//
	// Additionally (Gap 8): when metadata.archie.plugins lists specific
	// plugin files, only those are loaded  --  unlisted *.go files are ignored.

	dir := t.TempDir()
	skillsDir := filepath.Join(dir, ".agents", "skills", "archie-wf-tdd")
	pluginsDir := filepath.Join(skillsDir, "plugins")
	if err := os.MkdirAll(pluginsDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Write the skill SKILL.md with explicit plugin list.
	if err := os.WriteFile(filepath.Join(skillsDir, "SKILL.md"), []byte(`---
name: tdd-bugfix
description: TDD workflow
version: 1.0.0
metadata:
  archie:
    tools: [go]
    engine: any
    workflow: tdd
    plugins:
      - plugins/security-check.go
---
Analyse the bug, write repro tests, fix.
`), 0o644); err != nil {
		t.Fatal(err)
	}

	// Write the listed bundled plugin.
	if err := os.WriteFile(filepath.Join(pluginsDir, "security-check.go"), []byte(`package main

func Run(input string) string {
	return "security: " + input
}
`), 0o644); err != nil {
		t.Fatal(err)
	}

	// Write an unlisted plugin  --  it must NOT be loaded.
	if err := os.WriteFile(filepath.Join(pluginsDir, "extra.go"), []byte(`package main

func Run(input string) string {
	return "extra"
}
`), 0o644); err != nil {
		t.Fatal(err)
	}

	// Run an AgentStage against this worktree.
	pluginCalls := 0
	runner := &fakeAgentRunner{result: agentexec.Result{
		Version: agentexec.ProtocolVersion, Status: agentexec.StatusPassed,
		Summary: "done", TokensUsed: 1,
	}}

	tc := &TaskContext{
		Task:  &store.Task{ID: 1, Attempt: 1, Workflow: "tdd"},
		Dir:   dir,
		Agent: runner,
		Log:   slog.New(slog.DiscardHandler),
		Cfg:   config.Config{Models: map[string]string{"planner": "provider/model"}},
	}

	// Load the skill body (existing behavior).
	body := loadSkillBody(tc)
	if body == "" {
		t.Fatal("skill body not loaded  --  worktree setup is wrong")
	}

	// Gap 7 assertion: loadSkillBody must also populate skill plugins
	// on TaskContext so agent stages can access them.
	if len(tc.SkillPlugins) == 0 {
		t.Error("Gap 7: loadSkillBody() loaded the body but did not populate " +
			"TaskContext.SkillPlugins. Plugins from .agents/skills/<name>/plugins/*.go " +
			"must be loaded alongside the SKILL.md body and made available to agent " +
			"stages via TaskContext.")
		return
	}

	// Gap 8 assertion: only the frontmatter-listed plugin must be loaded.
	// The unlisted extra.go must NOT appear in SkillPlugins.
	if len(tc.SkillPlugins) != 1 {
		t.Errorf("Gap 8: got %d plugins, want 1  --  loadSkillBody must honor "+
			"metadata.archie.plugins and exclude unlisted *.go files", len(tc.SkillPlugins))
	}
	for _, p := range tc.SkillPlugins {
		if p.Name == "extra" {
			t.Error("Gap 8: unlisted plugin 'extra' was loaded  --  " +
				"loadSkillBody must only load plugins declared in metadata.archie.plugins")
		}
	}

	// Verify plugins are runnable.
	for _, p := range tc.SkillPlugins {
		out, err := p.Run("test-input")
		if err != nil {
			t.Errorf("plugin %s: Run failed: %v", p.Name, err)
			continue
		}
		if out == "" {
			t.Errorf("plugin %s: Run returned empty output", p.Name)
		}
		pluginCalls++
	}
	if pluginCalls == 0 {
		t.Error("Gap 7: plugins were loaded but none could be executed. " +
			"Plugins must be runnable via plugin.Run().")
	}
}
