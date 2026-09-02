// Package archieplaybooks is the application composition for the standalone
// playbook-validation CLI (cmd/archie-playbooks). It owns the lint mode's
// orchestration: collect the configured directories, run the domain
// validator, and shape the outcome for human consumption. All validation
// rules live in internal/domain/workflow (LoadKindWorkflowsYAML,
// LoadLabelWorkflowsYAML, LoadPlaybookDirs) -- this package only calls them
// and formats results; there is deliberately no second validation path
// here (per docs/prds/eda-playbook-engine.md and t2db.12).
package archieplaybooks

import (
	"fmt"
	"io"

	"github.com/samcharles93/archie-core/internal/domain/workflow"
)

// Result is the outcome of one lint run: the exit code for the process and
// the human-readable findings.
type Result struct {
	ExitCode int
	Findings []string
}

// Lint validates one or more playbook directories against the domain
// loaders and reports every collision / malformed file / invalid binding as
// a finding. It returns exit code 0 on a clean set, 1 when any finding is
// reported. Directories and single-file fields share one validation source:
//
//	workflow.LoadPlaybookDirs
//
// which already applies the same rules LoadKindWorkflowsYAML and
// LoadLabelWorkflowsYAML do, per file and across files -- so the linter can
// never disagree with the daemon's startup validation.
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
