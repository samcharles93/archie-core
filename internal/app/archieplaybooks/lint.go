// Package archieplaybooks implements the archie-playbooks CLI, using the
// daemon's own playbook loaders.
package archieplaybooks

import (
	"fmt"
	"io"

	"github.com/samcharles93/archie-core/internal/domain/eda/module"
	"github.com/samcharles93/archie-core/internal/domain/eda/playbook"
	"github.com/samcharles93/archie-core/internal/domain/workflow"
)

// Result is the outcome of one lint run: the exit code for the process and
// the human-readable findings.
type Result struct {
	ExitCode int
	Findings []string
}

// Lint validates playbook directories with workflow.LoadPlaybookDirs and
// reports every finding. It returns 0 when clean, 1 otherwise.
func Lint(dirs []string, stderr io.Writer) Result {
	var findings []string

	_, _, err := workflow.LoadPlaybookDirs(dirs)
	if err != nil {
		findings = append(findings, err.Error())
	}

	if len(findings) > 0 {
		for _, f := range findings {
			fmt.Fprintln(stderr, f)
		}
		return Result{ExitCode: 1, Findings: findings}
	}
	return Result{ExitCode: 0, Findings: nil}
}

// LintEDA validates an EDA playbook directory (the daemon's eda_playbook_dir)
// with playbook.Load, the loader the daemon runs at startup.
func LintEDA(dir string, stderr io.Writer) Result {
	if _, err := playbook.Load(dir, module.New()); err != nil {
		fmt.Fprintln(stderr, err)
		return Result{ExitCode: 1, Findings: []string{err.Error()}}
	}
	return Result{}
}
