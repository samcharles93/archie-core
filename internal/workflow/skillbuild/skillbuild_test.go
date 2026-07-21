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
