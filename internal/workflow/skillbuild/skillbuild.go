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

	"github.com/samcharles93/archie-core/internal/skill"
	"github.com/samcharles93/archie-core/internal/workflow"
	"github.com/samcharles93/archie-core/internal/workflow/wfextract"
)

// builtins returns the hardcoded fallback workflows. These are used when
// no skill declares a given workflow name in its metadata.archie.workflow.
func builtins() workflow.Registry {
	return workflow.Registry{
		"bootstrap":   workflow.Bootstrap(),
		"implement":   workflow.Implement(),
		"tdd":         workflow.TDD(),
		"feasibility": workflow.Feasibility(),
		"default":     workflow.Implement(),
	}
}

// BuildRegistry scans .agents/skills/ for catalog entries that declare a
// workflow in metadata.archie.workflow, builds each from its stage plugins,
// and returns a complete workflow.Registry. Workflow names not covered by
// any skill fall back to the built-in Go definitions.
//
// Skills define workflows. Plugins define stages. The daemon composes them.
func BuildRegistry(worktree string) (workflow.Registry, error) {
	catalog, err := skill.Catalog(worktree)
	if err != nil {
		return nil, fmt.Errorf("skill catalog: %w", err)
	}

	reg := builtins()

	for _, entry := range catalog {
		if entry.Workflow == "" {
			continue
		}
		sw := SkillWorkflow{Workflow: entry.Workflow, Dir: entry.Dir}
		wf, err := BuildWorkflow(worktree, sw)
		if err != nil {
			return nil, fmt.Errorf("build workflow %s from skill %s: %w", entry.Workflow, entry.Dir, err)
		}
		// Only override the built-in when the skill actually has stages —
		// an empty workflow means the skill declared intent but has no
		// plugins yet, so keep the built-in.
		if len(wf.Stages) > 0 {
			reg[entry.Workflow] = wf
		}
	}

	return reg, nil
}

// SkillWorkflow is the subset of a catalog entry needed to build a
// workflow from a skill's stage plugins.
type SkillWorkflow struct {
	Workflow string // from metadata.archie.workflow
	Dir      string // skill directory name
}

// AugmentRegistry scans a worktree directory for skills that declare
// workflows, builds each from its stage plugins, and returns a new
// registry with worktree workflows merged over the base. The base
// registry is not mutated.
//
// This is the per-task complement to BuildRegistry: BuildRegistry
// runs once at startup against a shared skills directory (or the
// workdir root); AugmentRegistry runs at process time against a
// specific cloned worktree so that per-repo skills are discovered
// without restarting the daemon.
func AugmentRegistry(worktree string, base workflow.Registry) (workflow.Registry, error) {
	catalog, err := skill.Catalog(worktree)
	if err != nil {
		return nil, fmt.Errorf("skill catalog: %w", err)
	}

	// Copy base — never mutate the caller's registry.
	reg := make(workflow.Registry, len(base)+len(catalog))
	for k, v := range base {
		reg[k] = v
	}

	for _, entry := range catalog {
		if entry.Workflow == "" {
			continue
		}
		sw := SkillWorkflow{Workflow: entry.Workflow, Dir: entry.Dir}
		wf, err := BuildWorkflow(worktree, sw)
		if err != nil {
			return nil, fmt.Errorf("build workflow %s from skill %s: %w", entry.Workflow, entry.Dir, err)
		}
		// Only override the base entry when the skill actually has stages.
		// An empty workflow means the skill declared intent but has no
		// plugins yet — keep the base (or built-in) definition.
		if len(wf.Stages) > 0 {
			reg[entry.Workflow] = wf
		}
	}

	return reg, nil
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
