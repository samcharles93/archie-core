// Package archieplaybooks implements the `archied playbooks` command, using the
// daemon's own playbook loaders.
package archieplaybooks

import (
	"fmt"
	"io"

	"github.com/samcharles93/archie-core/internal/domain/eda/module"
	"github.com/samcharles93/archie-core/internal/domain/eda/playbook"
)

// Result is the outcome of one lint run: the exit code for the process and
// the human-readable findings.
type Result struct {
	ExitCode int
	Findings []string
}

// LintEDA validates a directory of EDA playbook documents with the compiler
// the eda-playbooks resource validates against.
func LintEDA(dir string, stderr io.Writer) Result {
	if _, err := playbook.Load(dir, module.New()); err != nil {
		fmt.Fprintln(stderr, err)
		return Result{ExitCode: 1, Findings: []string{err.Error()}}
	}
	return Result{}
}
