// Package wfeval discovers and interprets a repo's custom workflow
// stages: .archie/stages/*.go files, each exporting a Stage() function
// that returns a workflow.Stage. Split from internal/workflow (and its
// generated symbol table in wfextract) to avoid the import cycle that
// would result from workflow depending on its own extracted symbols.
package wfeval

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/traefik/yaegi/interp"
	"github.com/traefik/yaegi/stdlib"

	"github.com/samcharles93/archie-core/internal/workflow"
	"github.com/samcharles93/archie-core/internal/workflow/wfextract"
)

// stagesDir is where a repo's custom stage scripts live, relative to
// the worktree root.
const stagesDir = ".archie/stages"

// Discover loads every .archie/stages/*.go file in dir and returns the
// workflow.Stage each exports, in filename order. A missing directory is
// not an error — it returns (nil, nil), meaning no custom stages are
// configured for this repo.
func Discover(dir string) ([]workflow.Stage, error) {
	entries, err := os.ReadDir(filepath.Join(dir, stagesDir))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read %s: %w", stagesDir, err)
	}

	var names []string
	for _, e := range entries {
		if !e.IsDir() && filepath.Ext(e.Name()) == ".go" {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)

	stages := make([]workflow.Stage, 0, len(names))
	for _, name := range names {
		stage, err := loadStage(filepath.Join(dir, stagesDir, name))
		if err != nil {
			return nil, fmt.Errorf("%s: %w", filepath.Join(stagesDir, name), err)
		}
		stages = append(stages, stage)
	}
	return stages, nil
}

// loadStage interprets one custom stage script and calls its exported
// Stage() function. The script runs in-process (interpreted, not
// sandboxed), so a panic inside it is recovered and returned as an error
// rather than taking down the daemon.
func loadStage(path string) (stage workflow.Stage, err error) {
	src, err := os.ReadFile(path)
	if err != nil {
		return workflow.Stage{}, err
	}

	defer func() {
		if r := recover(); r != nil {
			stage, err = workflow.Stage{}, fmt.Errorf("panicked: %v", r)
		}
	}()

	i := interp.New(interp.Options{})
	if err := i.Use(stdlib.Symbols); err != nil {
		return workflow.Stage{}, fmt.Errorf("yaegi: load stdlib symbols: %w", err)
	}
	if err := i.Use(wfextract.Symbols); err != nil {
		return workflow.Stage{}, fmt.Errorf("yaegi: load workflow symbols: %w", err)
	}

	if _, err := i.Eval(string(src)); err != nil {
		return workflow.Stage{}, fmt.Errorf("yaegi: evaluate: %w", err)
	}

	v, err := i.Eval("stages.Stage")
	if err != nil {
		return workflow.Stage{}, fmt.Errorf("yaegi: does not export func Stage() workflow.Stage: %w", err)
	}
	factory, ok := v.Interface().(func() workflow.Stage)
	if !ok {
		return workflow.Stage{}, fmt.Errorf("yaegi: Stage has the wrong signature (want func() workflow.Stage)")
	}

	return factory(), nil
}
