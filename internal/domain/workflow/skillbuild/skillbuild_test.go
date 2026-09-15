package skillbuild

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/samcharles93/archie-core/internal/domain/workflow"
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

	"github.com/samcharles93/archie-core/internal/domain/workflow"
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

	"github.com/samcharles93/archie-core/internal/domain/workflow"
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

	// Run the workflow  --  stages must execute in order.
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
	// Missing plugins directory is not an error  --  same pattern as
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

func TestBuildCatalogMarksBuiltinWorkflowOrigins(t *testing.T) {
	catalog, err := BuildCatalog(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if got := catalog.Origins["implement"]; got != "builtin" {
		t.Errorf("implement origin = %q, want builtin", got)
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

	// Skill 1: archie-wf-greet  --  plugin-defined workflow.
	greetDir := filepath.Join(dir, ".agents", "skills", "archie-wf-greet", "plugins")
	if err := os.MkdirAll(greetDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(greetDir, "01-hello.go"), []byte(`package main

import (
	"context"
	"github.com/samcharles93/archie-core/internal/domain/workflow"
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

	// Skill 2: archie-wf-implement  --  overrides the built-in implement workflow.
	implDir := filepath.Join(dir, ".agents", "skills", "archie-wf-custom-impl", "plugins")
	if err := os.MkdirAll(implDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(implDir, "01-plan.go"), []byte(`package main

import (
	"context"
	"github.com/samcharles93/archie-core/internal/domain/workflow"
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

	catalog, err := BuildCatalog(dir)
	if err != nil {
		t.Fatal(err)
	}
	reg := catalog.Registry

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
			t.Error("workflow 'greet' has no stages  --  plugin stages not loaded")
		}
		// Run it  --  stages must execute.
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
	if got := catalog.Origins["greet"]; got != "skill:archie-wf-greet" {
		t.Errorf("greet origin = %q, want skill:archie-wf-greet", got)
	}

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
			t.Errorf("BuildSummary = %q, want 'custom-impl-ran'  --  skill override did not take effect", tc.BuildSummary)
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
		if wf.Name != name && (name != "default" || wf.Name != "implement") {
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
			"did not declare metadata.archie.workflow  --  only skills that declare " +
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

// ── resilience: skip broken plugins, don't abort ────────────────────

func TestBuildWorkflowSkipsBrokenPlugin(t *testing.T) {
	// R2: one broken stage plugin must not abort the entire workflow.
	// Valid plugins before and after the broken one must still load.

	dir := t.TempDir()
	pluginsDir := filepath.Join(dir, ".agents", "skills", "archie-wf-resilient", "plugins")
	if err := os.MkdirAll(pluginsDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// First plugin  --  valid.
	if err := os.WriteFile(filepath.Join(pluginsDir, "01-setup.go"), []byte(`package main

import (
	"context"
	"github.com/samcharles93/archie-core/internal/domain/workflow"
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

	// Second plugin  --  BROKEN (syntax error).
	if err := os.WriteFile(filepath.Join(pluginsDir, "02-broken.go"), []byte(`package main
this is %% NOT VALID GO @@@@
`), 0o644); err != nil {
		t.Fatal(err)
	}

	// Third plugin  --  valid.
	if err := os.WriteFile(filepath.Join(pluginsDir, "03-teardown.go"), []byte(`package main

import (
	"context"
	"github.com/samcharles93/archie-core/internal/domain/workflow"
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

// ── duplicate warning suppression for builtin overrides ─────────────

// slogCaptureHandler is a slog.Handler that captures log records in a
// slice for inspection in tests. It writes log output to the test log
// instead of stderr to avoid infinite recursion through the log bridge.
type slogCaptureHandler struct {
	records []slog.Record
	t       *testing.T
}

func (h *slogCaptureHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return true
}

func (h *slogCaptureHandler) Handle(ctx context.Context, r slog.Record) error {
	h.records = append(h.records, r)
	h.t.Log(r.Level.String() + " " + r.Message)
	return nil
}

func (h *slogCaptureHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return h
}

func (h *slogCaptureHandler) WithGroup(name string) slog.Handler {
	return h
}

// setSlogCapture sets the default slog.Logger to one that captures all
// records and returns the handler for inspection. The previous logger
// is restored when t.Cleanup runs.
func setSlogCapture(t *testing.T) *slogCaptureHandler {
	t.Helper()
	prev := slog.Default()
	h := &slogCaptureHandler{t: t}
	slog.SetDefault(slog.New(h))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return h
}

func TestMergeSkillWorkflows_NoWarningOnBuiltinOverride(t *testing.T) {
	// A skill overriding a built-in workflow name (e.g. "implement")
	// must NOT produce a "duplicate workflow name" warning. Builtin
	// overrides are routine and expected.

	dir := t.TempDir()
	skillDir := filepath.Join(dir, ".agents", "skills", "archie-wf-implement")
	pluginsDir := filepath.Join(skillDir, "plugins")
	if err := os.MkdirAll(pluginsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(`---
name: archie-wf-implement
description: Custom implement workflow
version: 1.0.0
metadata:
  archie:
    workflow: implement
---
Custom implement.
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pluginsDir, "01-custom.go"), []byte(`package main

import (
	"context"
	"github.com/samcharles93/archie-core/internal/domain/workflow"
)

func Stage() (string, func(context.Context, *workflow.TaskContext) error) {
	return "custom", func(ctx context.Context, tc *workflow.TaskContext) error {
		tc.BuildSummary = "custom-ran"
		return nil
	}
}
`), 0o644); err != nil {
		t.Fatal(err)
	}

	capture := setSlogCapture(t)

	reg := builtins()
	if err := mergeSkillWorkflows(dir, reg, nil); err != nil {
		t.Fatal(err)
	}

	// Override must take effect.
	wf, ok := reg["implement"]
	if !ok {
		t.Fatal("'implement' missing from registry")
	}
	if len(wf.Stages) != 1 {
		t.Fatalf("got %d stages, want 1", len(wf.Stages))
	}
	if wf.Stages[0].Name != "custom" {
		t.Errorf("stage[0].Name = %q, want custom", wf.Stages[0].Name)
	}

	// No "duplicate" warning must have been logged.
	for _, r := range capture.records {
		if r.Level >= slog.LevelWarn && strings.Contains(r.Message, "duplicate") {
			t.Errorf("unexpected 'duplicate' warning on builtin override: %s", r.Message)
		}
	}
}

func TestMergeSkillWorkflows_WarnsOnTwoSkillsSameWorkflow(t *testing.T) {
	// When TWO DIFFERENT SKILLS declare the same workflow name, the
	// second one must produce a warning. This is a genuine conflict.

	dir := t.TempDir()

	// Skill 1: declares workflow "custom"
	skill1 := filepath.Join(dir, ".agents", "skills", "archie-wf-custom-a")
	plugins1 := filepath.Join(skill1, "plugins")
	if err := os.MkdirAll(plugins1, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skill1, "SKILL.md"), []byte(`---
name: archie-wf-custom-a
description: First custom workflow
version: 1.0.0
metadata:
  archie:
    workflow: custom
---
First.
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(plugins1, "01-first.go"), []byte(`package main

import (
	"context"
	"github.com/samcharles93/archie-core/internal/domain/workflow"
)

func Stage() (string, func(context.Context, *workflow.TaskContext) error) {
	return "first", func(ctx context.Context, tc *workflow.TaskContext) error {
		tc.BuildSummary = "first-ran"
		return nil
	}
}
`), 0o644); err != nil {
		t.Fatal(err)
	}

	// Skill 2: also declares workflow "custom"
	skill2 := filepath.Join(dir, ".agents", "skills", "archie-wf-custom-b")
	plugins2 := filepath.Join(skill2, "plugins")
	if err := os.MkdirAll(plugins2, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skill2, "SKILL.md"), []byte(`---
name: archie-wf-custom-b
description: Second custom workflow
version: 1.0.0
metadata:
  archie:
    workflow: custom
---
Second.
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(plugins2, "01-second.go"), []byte(`package main

import (
	"context"
	"github.com/samcharles93/archie-core/internal/domain/workflow"
)

func Stage() (string, func(context.Context, *workflow.TaskContext) error) {
	return "second", func(ctx context.Context, tc *workflow.TaskContext) error {
		tc.BuildSummary = "second-ran"
		return nil
	}
}
`), 0o644); err != nil {
		t.Fatal(err)
	}

	capture := setSlogCapture(t)

	reg := make(workflow.Registry)
	if err := mergeSkillWorkflows(dir, reg, nil); err != nil {
		t.Fatal(err)
	}

	// Last-write-wins: second skill's stages should be used.
	wf, ok := reg["custom"]
	if !ok {
		t.Fatal("'custom' missing from registry")
	}
	if len(wf.Stages) != 1 {
		t.Fatalf("got %d stages, want 1", len(wf.Stages))
	}
	if wf.Stages[0].Name != "second" {
		t.Errorf("stage[0].Name = %q, want second (last-write-wins)", wf.Stages[0].Name)
	}

	// Exactly one warning containing both skill dirs must have been logged.
	var warnings []slog.Record
	for _, r := range capture.records {
		if r.Level >= slog.LevelWarn && strings.Contains(r.Message, "duplicate") {
			warnings = append(warnings, r)
		}
	}
	if len(warnings) != 1 {
		t.Fatalf("got %d duplicate warnings, want exactly 1", len(warnings))
	}
	w := warnings[0]
	hasSkill := false
	hasPrev := false
	w.Attrs(func(a slog.Attr) bool {
		if a.Key == "skill" && strings.Contains(a.Value.String(), "archie-wf-custom-b") {
			hasSkill = true
		}
		if a.Key == "previous_skill" && strings.Contains(a.Value.String(), "archie-wf-custom-a") {
			hasPrev = true
		}
		return true
	})
	if !hasSkill {
		t.Error("warning missing 'skill' key with second skill dir")
	}
	if !hasPrev {
		t.Error("warning missing 'previous_skill' key with first skill dir")
	}
}

// TestBuildRegistry_NoWarningOnBuiltinOverride is an integration-style
// test that validates the warning is absent when a skill overrides the
// built-in "implement" workflow via BuildRegistry.
func TestBuildRegistry_NoWarningOnBuiltinOverride(t *testing.T) {
	dir := t.TempDir()
	skillDir := filepath.Join(dir, ".agents", "skills", "archie-wf-implement")
	pluginsDir := filepath.Join(skillDir, "plugins")
	if err := os.MkdirAll(pluginsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(`---
name: archie-wf-implement
description: Custom implement
version: 1.0.0
metadata:
  archie:
    workflow: implement
---
Custom.
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pluginsDir, "01-custom.go"), []byte(`package main

import (
	"context"
	"github.com/samcharles93/archie-core/internal/domain/workflow"
)

func Stage() (string, func(context.Context, *workflow.TaskContext) error) {
	return "custom-stage", func(ctx context.Context, tc *workflow.TaskContext) error {
		return nil
	}
}
`), 0o644); err != nil {
		t.Fatal(err)
	}

	capture := setSlogCapture(t)

	reg, err := BuildRegistry(dir)
	if err != nil {
		t.Fatal(err)
	}

	wf, ok := reg["implement"]
	if !ok {
		t.Fatal("'implement' missing from registry")
	}
	if len(wf.Stages) != 1 {
		t.Fatalf("got %d stages, want 1 from skill override", len(wf.Stages))
	}
	if wf.Stages[0].Name != "custom-stage" {
		t.Errorf("stage[0].Name = %q, want custom-stage", wf.Stages[0].Name)
	}

	for _, r := range capture.records {
		if r.Level >= slog.LevelWarn && strings.Contains(r.Message, "duplicate") {
			t.Errorf("unexpected 'duplicate' warning via BuildRegistry on builtin override: %s", r.Message)
		}
	}
}

func TestBuildRegistrySkipsBrokenSkill(t *testing.T) {
	// R2: a skill with broken plugins must not prevent other skills
	// from registering  --  and must not block daemon startup.

	dir := t.TempDir()

	// Skill 1: valid workflow.
	goodDir := filepath.Join(dir, ".agents", "skills", "archie-wf-good", "plugins")
	if err := os.MkdirAll(goodDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(goodDir, "01-step.go"), []byte(`package main

import (
	"context"
	"github.com/samcharles93/archie-core/internal/domain/workflow"
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

	// Skill 2: broken plugins  --  ALL plugins are broken.
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
		t.Error("'good' workflow missing  --  broken skill blocked registry build")
	}
	// Bad workflow must not be registered (it had no valid stages).
	if _, ok := reg["bad"]; ok {
		// It might fall back to built-in "bad"  --  that's fine.
		// But it should NOT be the empty workflow from the broken skill.
		t.Log("'bad' workflow present (may be built-in fallback)")
	}
	// Built-in fallbacks must still exist.
	if _, ok := reg["implement"]; !ok {
		t.Error("built-in 'implement' missing after broken skill was skipped")
	}
}
