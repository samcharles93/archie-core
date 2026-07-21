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

func TestBuildWorkflowNoStagesReturnsEmptyWorkflow(t *testing.T) {
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
