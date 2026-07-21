package wfeval

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/samcharles93/archie-core/internal/workflow"
)

func writeStageScript(t *testing.T, dir, name, src string) {
	t.Helper()
	stagesDir := filepath.Join(dir, ".archie", "stages")
	if err := os.MkdirAll(stagesDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stagesDir, name), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestDiscoverNoStagesDir(t *testing.T) {
	stages, err := Discover(t.TempDir())
	if err != nil {
		t.Fatalf("Discover() error = %v, want nil (no .archie/stages)", err)
	}
	if stages != nil {
		t.Fatalf("Discover() = %v, want nil", stages)
	}
}

func TestDiscoverRunsStagesInFilenameOrder(t *testing.T) {
	dir := t.TempDir()
	writeStageScript(t, dir, "b-second.go", `package stages

import (
	"context"

	"github.com/samcharles93/archie-core/internal/workflow"
)

func Stage() workflow.Stage {
	return workflow.Stage{Name: "second", Run: func(ctx context.Context, tc *workflow.TaskContext) error {
		return nil
	}}
}
`)
	writeStageScript(t, dir, "a-first.go", `package stages

import (
	"context"

	"github.com/samcharles93/archie-core/internal/workflow"
)

func Stage() workflow.Stage {
	return workflow.Stage{Name: "first", Run: func(ctx context.Context, tc *workflow.TaskContext) error {
		return nil
	}}
}
`)

	stages, err := Discover(dir)
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}
	if len(stages) != 2 {
		t.Fatalf("Discover() = %#v, want 2 stages", stages)
	}
	if stages[0].Name != "first" || stages[1].Name != "second" {
		t.Fatalf("Discover() order = [%s, %s], want [first, second]", stages[0].Name, stages[1].Name)
	}
}

func TestDiscoverStageRuns(t *testing.T) {
	dir := t.TempDir()
	writeStageScript(t, dir, "touch.go", `package stages

import (
	"context"
	"os"
	"path/filepath"

	"github.com/samcharles93/archie-core/internal/workflow"
)

func Stage() workflow.Stage {
	return workflow.Stage{Name: "touch", Run: func(ctx context.Context, tc *workflow.TaskContext) error {
		return os.WriteFile(filepath.Join(tc.Dir, "touched.txt"), []byte("hi\n"), 0o644)
	}}
}
`)
	stages, err := Discover(dir)
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}
	if len(stages) != 1 {
		t.Fatalf("Discover() = %#v, want 1 stage", stages)
	}

	tc := &workflow.TaskContext{Dir: dir}
	if err := stages[0].Run(context.Background(), tc); err != nil {
		t.Fatalf("stage.Run() error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "touched.txt")); err != nil {
		t.Fatalf("stage did not run: %v", err)
	}
}

func TestDiscoverBadSignature(t *testing.T) {
	dir := t.TempDir()
	writeStageScript(t, dir, "bad.go", `package stages

func Stage() string {
	return "not a workflow.Stage"
}
`)
	if _, err := Discover(dir); err == nil {
		t.Fatal("Discover() error = nil, want a signature mismatch error")
	}
}

func TestDiscoverPanicRecovered(t *testing.T) {
	dir := t.TempDir()
	writeStageScript(t, dir, "panics.go", `package stages

import "github.com/samcharles93/archie-core/internal/workflow"

func Stage() workflow.Stage {
	var files []string
	_ = files[5]
	return workflow.Stage{}
}
`)
	if _, err := Discover(dir); err == nil {
		t.Fatal("Discover() error = nil, want a recovered-panic error")
	}
}
