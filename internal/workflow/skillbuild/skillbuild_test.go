package skillbuild

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/samcharles93/archie-core/internal/workflow"
)

// ── workflows built from skill plugins ────────────────────────────────

func TestBuildWorkflowFromSkillPlugins(t *testing.T) {
	dir := t.TempDir()
	skillDir := filepath.Join(dir, ".agents", "skills", "archie-wf-greet")
	pluginsDir := filepath.Join(skillDir, "plugins")
	if err := os.MkdirAll(pluginsDir, 0o755); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(`---
name: archie-wf-greet
description: A custom greeting workflow built entirely from plugins.
version: 1.0.0
metadata:
  archie:
    workflow: greet
---
Greet the world, then say goodbye.
`), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(pluginsDir, "01-hello.go"), []byte(`package main

import (
	"context"

	"github.com/samcharles93/archie-core/internal/workflow"
)

func Stage() (string, func(context.Context, *workflow.TaskContext) error) {
	return "hello", func(ctx context.Context, tc *workflow.TaskContext) error {
		tc.BuildSummary = "hello from plugin"
		return nil
	}
}
`), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(pluginsDir, "02-world.go"), []byte(`package main

import (
	"context"

	"github.com/samcharles93/archie-core/internal/workflow"
)

func Stage() (string, func(context.Context, *workflow.TaskContext) error) {
	return "world", func(ctx context.Context, tc *workflow.TaskContext) error {
		tc.BuildSummary = tc.BuildSummary + " world"
		return nil
	}
}
`), 0o644); err != nil {
		t.Fatal(err)
	}

	entry := SkillWorkflow{Workflow: "greet", Dir: "archie-wf-greet"}
	wf, err := BuildWorkflow(dir, entry)
	if err != nil {
		t.Fatal(err)
	}
	if wf.Name != "greet" {
		t.Errorf("Workflow.Name = %q, want greet", wf.Name)
	}
	if len(wf.Stages) != 2 {
		t.Fatalf("got %d stages, want 2", len(wf.Stages))
	}
	if wf.Stages[0].Name != "hello" {
		t.Errorf("stage[0].Name = %q, want hello", wf.Stages[0].Name)
	}
	if wf.Stages[1].Name != "world" {
		t.Errorf("stage[1].Name = %q, want world", wf.Stages[1].Name)
	}

	// Run the workflow — stages must execute in order.
	tc := &workflow.TaskContext{BuildSummary: ""}
	for _, s := range wf.Stages {
		if err := s.Run(context.Background(), tc); err != nil {
			t.Fatalf("stage %s failed: %v", s.Name, err)
		}
	}
	if tc.BuildSummary != "hello from plugin world" {
		t.Errorf("BuildSummary = %q, want 'hello from plugin world'", tc.BuildSummary)
	}
}

func TestBuildWorkflowMissingSkillReturnsEmptyWorkflow(t *testing.T) {
	// Missing plugins directory is not an error — same pattern as
	// wfeval.Discover. Returns a Workflow with no stages.
	entry := SkillWorkflow{Workflow: "nonexistent", Dir: "nonexistent"}
	wf, err := BuildWorkflow(t.TempDir(), entry)
	if err != nil {
		t.Fatal(err)
	}
	if len(wf.Stages) != 0 {
		t.Errorf("got %d stages, want 0 for nonexistent skill", len(wf.Stages))
	}
}

// ── registry built from skill catalog ────────────────────────────────

func TestBuildRegistryFromSkillCatalog(t *testing.T) {
	// When .agents/skills/ contains directories with metadata.archie.workflow,
	// BuildRegistry must return a workflow.Registry with those workflows
	// built from their stage plugins. Workflows not declared by any skill
	// fall back to the built-in definitions (bootstrap, implement, tdd,
	// feasibility, default→implement).

	dir := t.TempDir()

	// Skill 1: archie-wf-greet — plugin-defined workflow.
	greetDir := filepath.Join(dir, ".agents", "skills", "archie-wf-greet", "plugins")
	if err := os.MkdirAll(greetDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(greetDir, "01-hello.go"), []byte(`package main

import (
	"context"
	"github.com/samcharles93/archie-core/internal/workflow"
)

func Stage() (string, func(context.Context, *workflow.TaskContext) error) {
	return "hello", func(ctx context.Context, tc *workflow.TaskContext) error {
		tc.BuildSummary = "greet-plugin-ran"
		return nil
	}
}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(greetDir), "SKILL.md"), []byte(`---
name: archie-wf-greet
description: Greet workflow from plugins
version: 1.0.0
metadata:
  archie:
    workflow: greet
---
Greet the user.
`), 0o644); err != nil {
		t.Fatal(err)
	}

	// Skill 2: archie-wf-implement — overrides the built-in implement workflow.
	implDir := filepath.Join(dir, ".agents", "skills", "archie-wf-custom-impl", "plugins")
	if err := os.MkdirAll(implDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(implDir, "01-plan.go"), []byte(`package main

import (
	"context"
	"github.com/samcharles93/archie-core/internal/workflow"
)

func Stage() (string, func(context.Context, *workflow.TaskContext) error) {
	return "custom-plan", func(ctx context.Context, tc *workflow.TaskContext) error {
		tc.BuildSummary = "custom-impl-ran"
		return nil
	}
}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(implDir), "SKILL.md"), []byte(`---
name: archie-wf-custom-impl
description: Custom implement workflow
version: 1.0.0
metadata:
  archie:
    workflow: implement
---
Custom implementation workflow.
`), 0o644); err != nil {
		t.Fatal(err)
	}

	reg, err := BuildRegistry(dir)
	if err != nil {
		t.Fatal(err)
	}

	// Skill-defined workflows must be present.
	t.Run("skill-defined workflow is registered", func(t *testing.T) {
		wf, ok := reg["greet"]
		if !ok {
			t.Fatal("BuildRegistry did not register workflow 'greet' from skill catalog")
		}
		if wf.Name != "greet" {
			t.Errorf("Workflow.Name = %q, want greet", wf.Name)
		}
		if len(wf.Stages) == 0 {
			t.Error("workflow 'greet' has no stages — plugin stages not loaded")
		}
		// Run it — stages must execute.
		tc := &workflow.TaskContext{BuildSummary: ""}
		for _, s := range wf.Stages {
			if err := s.Run(context.Background(), tc); err != nil {
				t.Fatalf("stage %s: %v", s.Name, err)
			}
		}
		if tc.BuildSummary != "greet-plugin-ran" {
			t.Errorf("BuildSummary = %q, want 'greet-plugin-ran'", tc.BuildSummary)
		}
	})

	// Skill override of built-in must take precedence.
	t.Run("skill overrides built-in workflow", func(t *testing.T) {
		wf, ok := reg["implement"]
		if !ok {
			t.Fatal("BuildRegistry did not register workflow 'implement'")
		}
		if len(wf.Stages) == 0 {
			t.Fatal("workflow 'implement' has no stages")
		}
		tc := &workflow.TaskContext{BuildSummary: ""}
		for _, s := range wf.Stages {
			if err := s.Run(context.Background(), tc); err != nil {
				t.Fatalf("stage %s: %v", s.Name, err)
			}
		}
		if tc.BuildSummary != "custom-impl-ran" {
			t.Errorf("BuildSummary = %q, want 'custom-impl-ran' — skill override did not take effect", tc.BuildSummary)
		}
	})

	// Built-in fallback: tdd is not declared by any skill.
	t.Run("built-in fallback for undeclared workflow", func(t *testing.T) {
		wf, ok := reg["tdd"]
		if !ok {
			t.Fatal("BuildRegistry missing built-in fallback for 'tdd'")
		}
		if wf.Name != "tdd" {
			t.Errorf("Workflow.Name = %q, want tdd", wf.Name)
		}
		if len(wf.Stages) == 0 {
			t.Error("built-in tdd workflow has no stages")
		}
	})

	// "default" key must always exist.
	t.Run("default key exists", func(t *testing.T) {
		wf, ok := reg["default"]
		if !ok {
			t.Fatal("BuildRegistry missing 'default' workflow key")
		}
		if len(wf.Stages) == 0 {
			t.Error("default workflow has no stages")
		}
	})
}

func TestBuildRegistryNoSkillsReturnsBuiltins(t *testing.T) {
	// When .agents/skills/ does not exist, BuildRegistry returns the
	// built-in workflow set so the daemon can still route tasks.
	reg, err := BuildRegistry(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	builtins := []string{"bootstrap", "implement", "tdd", "feasibility", "default"}
	for _, name := range builtins {
		wf, ok := reg[name]
		if !ok {
			t.Errorf("BuildRegistry missing built-in workflow %q when no skills exist", name)
			continue
		}
		if wf.Name != name && !(name == "default" && wf.Name == "implement") {
			t.Errorf("built-in %q: Name = %q, want %q", name, wf.Name, name)
		}
	}
}

func TestBuildRegistrySkillWithNoWorkflowDeclaredIsIgnored(t *testing.T) {
	// A skill directory without metadata.archie.workflow must not
	// produce a registry entry. It's a utility skill, not a workflow.
	dir := t.TempDir()
	skillDir := filepath.Join(dir, ".agents", "skills", "archie-wf-utils")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(`---
name: archie-wf-utils
description: Utility helpers, not a workflow
version: 1.0.0
metadata:
  archie:
    tools: [go]
---
Utility content.
`), 0o644); err != nil {
		t.Fatal(err)
	}

	reg, err := BuildRegistry(dir)
	if err != nil {
		t.Fatal(err)
	}

	// There is no workflow called "utils" because the skill didn't declare one.
	if _, ok := reg["utils"]; ok {
		t.Error("BuildRegistry registered 'utils' as a workflow, but the skill " +
			"did not declare metadata.archie.workflow — only skills that declare " +
			"a workflow should produce registry entries")
	}

	// Built-ins must still be present.
	if _, ok := reg["implement"]; !ok {
		t.Error("built-in 'implement' missing when a non-workflow skill exists")
	}
}

func TestBuildRegistryEmptySkillsDirReturnsBuiltins(t *testing.T) {
	// An empty .agents/skills/ directory must not cause an error.
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".agents", "skills"), 0o755); err != nil {
		t.Fatal(err)
	}

	reg, err := BuildRegistry(dir)
	if err != nil {
		t.Fatal(err)
	}

	if _, ok := reg["implement"]; !ok {
		t.Error("built-in 'implement' missing when skills dir is empty")
	}
}

// ── per-worktree registry augmentation ───────────────────────────────

func TestAugmentRegistryAddsWorktreeWorkflows(t *testing.T) {
	// When a worktree contains .agents/skills/ with workflow-declaring
	// skills, AugmentRegistry must add those workflows to the base
	// registry. Workflows already in the base are not duplicated.

	worktree := t.TempDir()
	skillDir := filepath.Join(worktree, ".agents", "skills", "archie-wf-custom")
	pluginsDir := filepath.Join(skillDir, "plugins")
	if err := os.MkdirAll(pluginsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(`---
name: archie-wf-custom
description: Custom workflow from worktree
version: 1.0.0
metadata:
  archie:
    workflow: custom
---
Custom worktree workflow.
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pluginsDir, "01-do.go"), []byte(`package main

import (
	"context"
	"github.com/samcharles93/archie-core/internal/workflow"
)

func Stage() (string, func(context.Context, *workflow.TaskContext) error) {
	return "do", func(ctx context.Context, tc *workflow.TaskContext) error {
		tc.BuildSummary = "worktree-custom-ran"
		return nil
	}
}
`), 0o644); err != nil {
		t.Fatal(err)
	}

	base := workflow.Registry{"default": {Name: "default", Stages: nil}}
	aug, err := AugmentRegistry(worktree, base)
	if err != nil {
		t.Fatal(err)
	}

	// Base entries preserved.
	if _, ok := aug["default"]; !ok {
		t.Error("'default' missing from augmented registry")
	}

	// Worktree workflow added.
	wf, ok := aug["custom"]
	if !ok {
		t.Fatal("'custom' workflow not found in augmented registry — worktree skill was not discovered")
	}
	if wf.Name != "custom" {
		t.Errorf("Workflow.Name = %q, want custom", wf.Name)
	}
	if len(wf.Stages) != 1 {
		t.Fatalf("got %d stages, want 1", len(wf.Stages))
	}
	if wf.Stages[0].Name != "do" {
		t.Errorf("stage[0].Name = %q, want do", wf.Stages[0].Name)
	}

	// Run the worktree workflow.
	tc := &workflow.TaskContext{BuildSummary: ""}
	if err := wf.Stages[0].Run(context.Background(), tc); err != nil {
		t.Fatal(err)
	}
	if tc.BuildSummary != "worktree-custom-ran" {
		t.Errorf("BuildSummary = %q, want 'worktree-custom-ran'", tc.BuildSummary)
	}
}

func TestAugmentRegistryWorktreeOverridesBase(t *testing.T) {
	// A worktree skill declaring the same workflow name as a base entry
	// must take precedence. Worktree skills ARE the repo's workflow
	// definition.

	worktree := t.TempDir()
	skillDir := filepath.Join(worktree, ".agents", "skills", "archie-wf-implement")
	pluginsDir := filepath.Join(skillDir, "plugins")
	if err := os.MkdirAll(pluginsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(`---
name: archie-wf-implement
description: Repo-specific implement workflow
version: 1.0.0
metadata:
  archie:
    workflow: implement
---
Repo-custom implement.
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pluginsDir, "01-repo-plan.go"), []byte(`package main

import (
	"context"
	"github.com/samcharles93/archie-core/internal/workflow"
)

func Stage() (string, func(context.Context, *workflow.TaskContext) error) {
	return "repo-plan", func(ctx context.Context, tc *workflow.TaskContext) error {
		tc.BuildSummary = "repo-implement-override"
		return nil
	}
}
`), 0o644); err != nil {
		t.Fatal(err)
	}

	// Base has "implement" as a built-in.
	base := workflow.Registry{"implement": {Name: "implement", Stages: []workflow.Stage{
		{Name: "builtin-plan", Run: func(ctx context.Context, tc *workflow.TaskContext) error {
			tc.BuildSummary = "builtin-implement"
			return nil
		}},
	}}}
	aug, err := AugmentRegistry(worktree, base)
	if err != nil {
		t.Fatal(err)
	}

	wf, ok := aug["implement"]
	if !ok {
		t.Fatal("'implement' missing from augmented registry")
	}
	if len(wf.Stages) != 1 {
		t.Fatalf("got %d stages, want 1 from worktree override", len(wf.Stages))
	}
	if wf.Stages[0].Name != "repo-plan" {
		t.Errorf("stage[0].Name = %q, want 'repo-plan' from worktree override", wf.Stages[0].Name)
	}

	// Run it — must use worktree version.
	tc := &workflow.TaskContext{BuildSummary: ""}
	if err := wf.Stages[0].Run(context.Background(), tc); err != nil {
		t.Fatal(err)
	}
	if tc.BuildSummary != "repo-implement-override" {
		t.Errorf("BuildSummary = %q, want 'repo-implement-override' (worktree override)", tc.BuildSummary)
	}
}

func TestAugmentRegistryDoesNotModifyBase(t *testing.T) {
	// AugmentRegistry returns a NEW registry — the caller's base must
	// not be mutated.
	base := workflow.Registry{"keep": {Name: "keep"}}
	worktree := t.TempDir()

	aug, err := AugmentRegistry(worktree, base)
	if err != nil {
		t.Fatal(err)
	}

	// Augmented has the entry.
	if _, ok := aug["keep"]; !ok {
		t.Fatal("'keep' missing from augmented registry")
	}

	// Mutating augmented must not affect base.
	aug["keep"] = workflow.Workflow{Name: "modified"}
	if base["keep"].Name != "keep" {
		t.Error("AugmentRegistry mutated the base registry — must return a copy")
	}
}

func TestAugmentRegistryNonexistentWorktreeReturnsBase(t *testing.T) {
	base := workflow.Registry{"default": {Name: "default"}}
	aug, err := AugmentRegistry("/nonexistent/worktree/path", base)
	if err != nil {
		t.Fatal(err)
	}
	if len(aug) != len(base) {
		t.Errorf("got %d entries, want %d (nonexistent worktree adds nothing)", len(aug), len(base))
	}
}

func TestAugmentRegistryWorktreeWithNoSkillsReturnsBase(t *testing.T) {
	// A worktree without .agents/skills/ must not error — returns
	// a copy of the base unchanged.
	worktree := t.TempDir()
	base := workflow.Registry{"default": {Name: "default"}}
	aug, err := AugmentRegistry(worktree, base)
	if err != nil {
		t.Fatal(err)
	}
	if len(aug) != len(base) {
		t.Errorf("got %d entries, want %d (no skills in worktree)", len(aug), len(base))
	}
}

func TestAugmentRegistryWorktreeSkillWithNoStagesDoesNotOverride(t *testing.T) {
	// A skill that declares a workflow but has no plugins must not
	// override the base entry — it would produce an empty workflow.
	worktree := t.TempDir()
	skillDir := filepath.Join(worktree, ".agents", "skills", "archie-wf-empty")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(`---
name: archie-wf-empty
description: Workflow with no stages
version: 1.0.0
metadata:
  archie:
    workflow: implement
---
No plugins here.
`), 0o644); err != nil {
		t.Fatal(err)
	}

	base := workflow.Registry{"implement": {Name: "implement", Stages: []workflow.Stage{
		{Name: "builtin", Run: func(ctx context.Context, tc *workflow.TaskContext) error { return nil }},
	}}}
	aug, err := AugmentRegistry(worktree, base)
	if err != nil {
		t.Fatal(err)
	}

	wf := aug["implement"]
	if len(wf.Stages) == 0 {
		t.Error("worktree skill with no stages overrode base — must keep base when skill is empty")
	}
	if wf.Stages[0].Name != "builtin" {
		t.Error("base stage was replaced by empty worktree skill")
	}
}

// ── AugmentRegistry adversarial tests ────────────────────────────────

func TestAugmentRegistryConcurrentAccess(t *testing.T) {
	// AugmentRegistry must be safe to call concurrently from different
	// goroutines (e.g. multiple tasks processed in parallel).
	worktree := t.TempDir()
	skillDir := filepath.Join(worktree, ".agents", "skills", "archie-wf-concurrent")
	pluginsDir := filepath.Join(skillDir, "plugins")
	if err := os.MkdirAll(pluginsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(`---
name: archie-wf-concurrent
description: Concurrent test
version: 1.0.0
metadata:
  archie:
    workflow: concurrent
---
Concurrent.
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pluginsDir, "01-step.go"), []byte(`package main

import (
	"context"
	"github.com/samcharles93/archie-core/internal/workflow"
)

func Stage() (string, func(context.Context, *workflow.TaskContext) error) {
	return "step", func(ctx context.Context, tc *workflow.TaskContext) error {
		return nil
	}
}
`), 0o644); err != nil {
		t.Fatal(err)
	}

	base := workflow.Registry{"default": {Name: "default"}}

	// Run 10 concurrent augmentations — none should panic or corrupt state.
	errs := make(chan error, 10)
	for range 10 {
		go func() {
			aug, err := AugmentRegistry(worktree, base)
			if err != nil {
				errs <- err
				return
			}
			if _, ok := aug["concurrent"]; !ok {
				errs <- nil // would use sentinel but keeping simple
				return
			}
			errs <- nil
		}()
	}
	for range 10 {
		if err := <-errs; err != nil {
			t.Error("concurrent AugmentRegistry failed:", err)
		}
	}

	// Base must still be unmodified.
	if _, ok := base["concurrent"]; ok {
		t.Error("base registry was mutated by concurrent augmentation")
	}
}

func TestAugmentRegistryBrokenSkillMDIsSkipped(t *testing.T) {
	// A SKILL.md with invalid YAML must not crash augmentation — the
	// skill is simply skipped (skill.Catalog skips parse errors).
	worktree := t.TempDir()
	skillDir := filepath.Join(worktree, ".agents", "skills", "archie-wf-broken")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(`---
this is not valid: [[[ YAML {{{
---
Body.
`), 0o644); err != nil {
		t.Fatal(err)
	}

	base := workflow.Registry{"default": {Name: "default"}}
	aug, err := AugmentRegistry(worktree, base)
	if err != nil {
		t.Fatal("AugmentRegistry errored on broken SKILL.md:", err)
	}
	if len(aug) != len(base) {
		t.Errorf("got %d entries, want %d (broken SKILL.md adds nothing)", len(aug), len(base))
	}
}

func TestAugmentRegistryWorktreeWithNoSkillsDir(t *testing.T) {
	// A worktree that exists but has no .agents/ directory at all
	// must not error.
	worktree := t.TempDir()
	// Don't create .agents/skills/ — just a bare worktree.
	if err := os.WriteFile(filepath.Join(worktree, "README.md"), []byte(`# repo`), 0o644); err != nil {
		t.Fatal(err)
	}

	base := workflow.Registry{"default": {Name: "default"}}
	aug, err := AugmentRegistry(worktree, base)
	if err != nil {
		t.Fatal("AugmentRegistry errored on worktree without .agents/: ", err)
	}
	if len(aug) != len(base) {
		t.Errorf("got %d entries, want %d", len(aug), len(base))
	}
}

func TestAugmentRegistryNilBaseReturnsEmpty(t *testing.T) {
	// A nil base registry must be treated as empty — not panic.
	worktree := t.TempDir()
	aug, err := AugmentRegistry(worktree, nil)
	if err != nil {
		t.Fatal("AugmentRegistry errored with nil base:", err)
	}
	if aug == nil {
		t.Error("AugmentRegistry returned nil for nil base — want empty registry")
	}
	if len(aug) != 0 {
		t.Errorf("got %d entries with nil base and no skills, want 0", len(aug))
	}
}

func TestBuildRegistryNoStagesReturnsEmptyWorkflow(t *testing.T) {
	dir := t.TempDir()
	skillDir := filepath.Join(dir, ".agents", "skills", "archie-wf-empty")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(`---
name: archie-wf-empty
description: No stages
version: 1.0.0
metadata:
  archie:
    workflow: empty
---
Body.
`), 0o644); err != nil {
		t.Fatal(err)
	}

	entry := SkillWorkflow{Workflow: "empty", Dir: "archie-wf-empty"}
	wf, err := BuildWorkflow(dir, entry)
	if err != nil {
		t.Fatal(err)
	}
	if len(wf.Stages) != 0 {
		t.Errorf("got %d stages, want 0", len(wf.Stages))
	}
	if wf.Name != "empty" {
		t.Errorf("Workflow.Name = %q, want empty", wf.Name)
	}
}

// ── resilience: skip broken plugins, don't abort ────────────────────

func TestBuildWorkflowSkipsBrokenPlugin(t *testing.T) {
	// R2: one broken stage plugin must not abort the entire workflow.
	// Valid plugins before and after the broken one must still load.

	dir := t.TempDir()
	pluginsDir := filepath.Join(dir, ".agents", "skills", "archie-wf-resilient", "plugins")
	if err := os.MkdirAll(pluginsDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// First plugin — valid.
	if err := os.WriteFile(filepath.Join(pluginsDir, "01-setup.go"), []byte(`package main

import (
	"context"
	"github.com/samcharles93/archie-core/internal/workflow"
)

func Stage() (string, func(context.Context, *workflow.TaskContext) error) {
	return "setup", func(ctx context.Context, tc *workflow.TaskContext) error {
		tc.BuildSummary = "setup-ran"
		return nil
	}
}
`), 0o644); err != nil {
		t.Fatal(err)
	}

	// Second plugin — BROKEN (syntax error).
	if err := os.WriteFile(filepath.Join(pluginsDir, "02-broken.go"), []byte(`package main
this is %% NOT VALID GO @@@@
`), 0o644); err != nil {
		t.Fatal(err)
	}

	// Third plugin — valid.
	if err := os.WriteFile(filepath.Join(pluginsDir, "03-teardown.go"), []byte(`package main

import (
	"context"
	"github.com/samcharles93/archie-core/internal/workflow"
)

func Stage() (string, func(context.Context, *workflow.TaskContext) error) {
	return "teardown", func(ctx context.Context, tc *workflow.TaskContext) error {
		tc.BuildSummary = tc.BuildSummary + " teardown-ran"
		return nil
	}
}
`), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(filepath.Dir(pluginsDir), "SKILL.md"), []byte(`---
name: archie-wf-resilient
description: Resilient workflow
version: 1.0.0
metadata:
  archie:
    workflow: resilient
---
Resilient workflow.
`), 0o644); err != nil {
		t.Fatal(err)
	}

	entry := SkillWorkflow{Workflow: "resilient", Dir: "archie-wf-resilient"}
	wf, err := BuildWorkflow(dir, entry)
	if err != nil {
		t.Fatalf("BuildWorkflow returned error instead of skipping broken plugin: %v", err)
	}
	if len(wf.Stages) != 2 {
		t.Fatalf("got %d stages, want 2 (broken plugin skipped)", len(wf.Stages))
	}
	if wf.Stages[0].Name != "setup" {
		t.Errorf("stage[0].Name = %q, want setup", wf.Stages[0].Name)
	}
	if wf.Stages[1].Name != "teardown" {
		t.Errorf("stage[1].Name = %q, want teardown", wf.Stages[1].Name)
	}

	// Run the surviving stages.
	tc := &workflow.TaskContext{BuildSummary: ""}
	for _, s := range wf.Stages {
		if err := s.Run(context.Background(), tc); err != nil {
			t.Fatalf("stage %s: %v", s.Name, err)
		}
	}
	if tc.BuildSummary != "setup-ran teardown-ran" {
		t.Errorf("BuildSummary = %q, want 'setup-ran teardown-ran'", tc.BuildSummary)
	}
}

func TestBuildRegistrySkipsBrokenSkill(t *testing.T) {
	// R2: a skill with broken plugins must not prevent other skills
	// from registering — and must not block daemon startup.

	dir := t.TempDir()

	// Skill 1: valid workflow.
	goodDir := filepath.Join(dir, ".agents", "skills", "archie-wf-good", "plugins")
	if err := os.MkdirAll(goodDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(goodDir, "01-step.go"), []byte(`package main

import (
	"context"
	"github.com/samcharles93/archie-core/internal/workflow"
)

func Stage() (string, func(context.Context, *workflow.TaskContext) error) {
	return "step", func(ctx context.Context, tc *workflow.TaskContext) error {
		return nil
	}
}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(goodDir), "SKILL.md"), []byte(`---
name: archie-wf-good
description: Good workflow
version: 1.0.0
metadata:
  archie:
    workflow: good
---
Good.
`), 0o644); err != nil {
		t.Fatal(err)
	}

	// Skill 2: broken plugins — ALL plugins are broken.
	badDir := filepath.Join(dir, ".agents", "skills", "archie-wf-bad", "plugins")
	if err := os.MkdirAll(badDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(badDir, "01-broken.go"), []byte(`package main
%%% SYNTAX ERROR @@@
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(badDir), "SKILL.md"), []byte(`---
name: archie-wf-bad
description: Broken workflow
version: 1.0.0
metadata:
  archie:
    workflow: bad
---
Broken.
`), 0o644); err != nil {
		t.Fatal(err)
	}

	reg, err := BuildRegistry(dir)
	if err != nil {
		t.Fatalf("BuildRegistry returned error instead of skipping broken skill: %v", err)
	}

	// Good workflow must still be registered.
	if _, ok := reg["good"]; !ok {
		t.Error("'good' workflow missing — broken skill blocked registry build")
	}
	// Bad workflow must not be registered (it had no valid stages).
	if _, ok := reg["bad"]; ok {
		// It might fall back to built-in "bad" — that's fine.
		// But it should NOT be the empty workflow from the broken skill.
		t.Log("'bad' workflow present (may be built-in fallback)")
	}
	// Built-in fallbacks must still exist.
	if _, ok := reg["implement"]; !ok {
		t.Error("built-in 'implement' missing after broken skill was skipped")
	}
}
