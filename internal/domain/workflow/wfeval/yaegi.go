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

	"github.com/samcharles93/archie-core/internal/domain/workflow"
	"github.com/samcharles93/archie-core/internal/domain/workflow/wfextract"
	"github.com/samcharles93/archie-core/internal/yaegiutil"
)

// stagesDir is where a repo's custom stage scripts live, relative to
// the worktree root.
const stagesDir = ".archie/stages"

// Discover loads every .archie/stages/*.go file in dir and returns the
// workflow.Stage each exports, in filename order. A missing directory is
// not an error  --  it returns (nil, nil), meaning no custom stages are
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
func loadStage(path string) (workflow.Stage, error) {
	src, err := os.ReadFile(path)
	if err != nil {
		return workflow.Stage{}, err
	}

	return yaegiutil.Safe(path, func() (workflow.Stage, error) {
		i, err := yaegiutil.New(interp.Options{}, wfextract.Symbols)
		if err != nil {
			return workflow.Stage{}, err
		}
		factory, err := yaegiutil.Resolve[func() workflow.Stage](i, string(src), "stages.Stage")
		if err != nil {
			return workflow.Stage{}, fmt.Errorf("yaegi: %w", err)
		}
		return factory(), nil
	})
}
