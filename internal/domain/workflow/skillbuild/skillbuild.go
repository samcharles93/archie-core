// Package skillbuild constructs Workflows from skill catalog entries by
// loading Yaegi-interpreted stage plugins from the skill's plugins/ directory.
package skillbuild

import (
	"context"
	"fmt"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/traefik/yaegi/interp"

	"github.com/samcharles93/archie-core/internal/skill"
	"github.com/samcharles93/archie-core/internal/domain/workflow"
	"github.com/samcharles93/archie-core/internal/domain/workflow/wfextract"
	"github.com/samcharles93/archie-core/internal/yaegiutil"
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
	reg := builtins()
	if err := mergeSkillWorkflows(worktree, reg); err != nil {
		return nil, err
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
	// Copy base  --  never mutate the caller's registry.
	reg := make(workflow.Registry, len(base))
	maps.Copy(reg, base)
	if err := mergeSkillWorkflows(worktree, reg); err != nil {
		return nil, err
	}
	return reg, nil
}

// mergeSkillWorkflows scans worktree for skill catalog entries that
// declare a workflow, builds each from its stage plugins, and merges the
// result into reg in place. A skill whose workflow has no stages yet
// (declared intent, no plugins/ directory) leaves reg's existing entry
// (built-in or base) untouched.
func mergeSkillWorkflows(worktree string, reg workflow.Registry) error {
	catalog, err := skill.Catalog(worktree)
	if err != nil {
		return fmt.Errorf("skill catalog: %w", err)
	}

	seenFromSkill := make(map[string]string) // workflow -> skill dir

	for _, entry := range catalog {
		if entry.Workflow == "" {
			continue
		}
		sw := SkillWorkflow{Workflow: entry.Workflow, Dir: entry.Dir}
		wf, err := BuildWorkflow(worktree, sw)
		if err != nil {
			slog.Default().Warn("skipping broken skill workflow",
				"workflow", entry.Workflow,
				"skill", entry.Dir,
				"err", err,
			)
			continue
		}
		if len(wf.Stages) > 0 {
			// Only warn when TWO SKILLS declare the same workflow name.
			// Overriding a built-in name is routine  --  don't log.
			if prevSkill, dup := seenFromSkill[entry.Workflow]; dup {
				slog.Default().Warn("duplicate workflow name across skills  --  overriding",
					"workflow", entry.Workflow,
					"skill", entry.Dir,
					"previous_skill", prevSkill,
				)
			}
			seenFromSkill[entry.Workflow] = entry.Dir
			reg[entry.Workflow] = wf
		}
	}

	return nil
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

	// Each plugin file is package main defining Stage()  --  they must be
	// evaluated in separate interpreters to avoid symbol collisions.
	stages := make([]workflow.Stage, 0, len(names))
	for _, name := range names {
		path := filepath.Join(pluginsDir, name)
		src, err := os.ReadFile(path)
		if err != nil {
			slog.Default().Warn("skipping unreadable stage plugin", "plugin", name, "err", err)
			continue
		}
		stage, err := loadStage(name, string(src))
		if err != nil {
			slog.Default().Warn("skipping broken stage plugin", "plugin", name, "err", err)
			continue
		}
		stages = append(stages, stage)
	}

	return workflow.Workflow{Name: entry.Workflow, Stages: stages}, nil
}

// loadStage interprets a single .go plugin source via Yaegi and calls its
// exported Stage(). filename is used only for interpreter panic messages.
func loadStage(filename, src string) (workflow.Stage, error) {
	return yaegiutil.Safe(filename, func() (workflow.Stage, error) {
		i, err := yaegiutil.New(interp.Options{}, wfextract.Symbols)
		if err != nil {
			return workflow.Stage{}, err
		}
		if _, err := i.Eval(src); err != nil {
			return workflow.Stage{}, fmt.Errorf("evaluate: %w", err)
		}
		v, err := i.Eval("main.Stage")
		if err != nil {
			return workflow.Stage{}, fmt.Errorf("does not export main.Stage: %w", err)
		}
		if fn, ok := v.Interface().(func() workflow.Stage); ok {
			return fn(), nil
		}
		// Fallback for test plugins: func() (string, func(...) error).
		// NOTE: this constructs Stage with positional fields. If Stage
		// gains new fields in the future, this path will silently zero-
		// value them. Prefer func() workflow.Stage in production plugins.
		if rawFn, ok := v.Interface().(func() (string, func(context.Context, *workflow.TaskContext) error)); ok {
			name, run := rawFn()
			return workflow.Stage{Name: name, Run: run}, nil
		}
		return workflow.Stage{}, fmt.Errorf("main.Stage is %T, want func() workflow.Stage", v.Interface())
	})
}
