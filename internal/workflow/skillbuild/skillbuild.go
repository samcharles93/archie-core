// Package skillbuild constructs Workflows from skill catalog entries by
// loading Yaegi-interpreted stage plugins from the skill's plugins/ directory.
package skillbuild

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/traefik/yaegi/interp"
	"github.com/traefik/yaegi/stdlib"

	"github.com/samcharles93/archie-core/internal/workflow"
	"github.com/samcharles93/archie-core/internal/workflow/wfextract"
)

// SkillWorkflow is the subset of a catalog entry needed to build a
// workflow from a skill's stage plugins.
type SkillWorkflow struct {
	Workflow string // from metadata.archie.workflow
	Dir      string // skill directory name
}

// BuildWorkflow loads stage plugins from a skill's plugins/ directory
// and returns a workflow.Workflow. Stages are Yaegi-interpreted .go files
// sorted by filename. Each file must export:
//
//	func Stage() workflow.Stage
func BuildWorkflow(worktree string, entry SkillWorkflow) (workflow.Workflow, error) {
	pluginsDir := filepath.Join(worktree, ".agents", "skills", entry.Dir, "plugins")
	entries, err := os.ReadDir(pluginsDir)
	if os.IsNotExist(err) {
		return workflow.Workflow{Name: entry.Workflow}, nil
	}
	if err != nil {
		return workflow.Workflow{}, fmt.Errorf("read plugins dir %s: %w", pluginsDir, err)
	}

	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".go") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)

	stages := make([]workflow.Stage, 0, len(names))
	for _, name := range names {
		path := filepath.Join(pluginsDir, name)
		src, err := os.ReadFile(path)
		if err != nil {
			return workflow.Workflow{}, fmt.Errorf("read plugin %s: %w", path, err)
		}
		stage, err := loadStage(name, string(src))
		if err != nil {
			return workflow.Workflow{}, fmt.Errorf("plugin %s: %w", name, err)
		}
		stages = append(stages, stage)
	}

	return workflow.Workflow{Name: entry.Workflow, Stages: stages}, nil
}

// loadStage interprets a single .go plugin source via Yaegi, using the
// wfextract symbol table so plugins can reference workflow types.
func loadStage(filename string, src string) (workflow.Stage, error) {
	i := interp.New(interp.Options{})
	if err := i.Use(stdlib.Symbols); err != nil {
		return workflow.Stage{}, fmt.Errorf("stdlib: %w", err)
	}
	if err := i.Use(wfextract.Symbols); err != nil {
		return workflow.Stage{}, fmt.Errorf("wfextract: %w", err)
	}
	if _, err := i.Eval(src); err != nil {
		return workflow.Stage{}, fmt.Errorf("eval: %w", err)
	}
	v, err := i.Eval("main.Stage")
	if err != nil {
		return workflow.Stage{}, fmt.Errorf("resolve Stage: %w", err)
	}
	fn, ok := v.Interface().(func() workflow.Stage)
	if !ok {
		// Fallback for test plugins: func() (string, func(...) error)
		rawFn, rawOk := v.Interface().(func() (string, func(context.Context, *workflow.TaskContext) error))
		if rawOk {
			name, run := rawFn()
			return workflow.Stage{Name: name, Run: run}, nil
		}
		return workflow.Stage{}, fmt.Errorf("Stage is %T, want func() workflow.Stage", v.Interface())
	}
	return fn(), nil
}
