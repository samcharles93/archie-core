package workflow

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/samcharles93/archie-core/internal/taskstate"
)

// outcomeSite is one place in this package that ends a workflow by assigning
// tc.Outcome: the file it lives in, the status identifier it assigns, and what
// the site decides.
type outcomeSite struct {
	file string
	// ident is the package status identifier the site assigns, spelled the way
	// the source spells it -- that is the string this test matches source
	// against.
	ident string
	// why names the decision, so a reader adjudicating a diff here knows which
	// site moved without opening the file.
	why string
}

// outcomeSites is every tc.Outcome assignment in this package's non-test
// sources. Workflow.Run persists any of them as a single transition out of
// StatusRunning (finish, workflow.go), so each status below has to be a row
// internal/taskstate's table allows from Running -- the two no-change paths
// that set StatusMerged while running are exactly what that table caught.
//
// The list is checked against the source in both directions by
// TestOutcomeSitesAreTransitionsFromRunning: a site added, removed or moved to
// another status fails there and has to be adjudicated against the table
// rather than discovered on a live run.
var outcomeSites = []outcomeSite{
	{"agent_run.go", "StatusCompleted", "a chat task's agent run produced its answer: nothing to open a PR for"},
	{"diff_rules.go", "StatusParked", "the diff breaks one of the repo's blocking rules"},
	{"feasibility.go", "StatusClosedWontDo", "feasibility decided this work should not be done"},
	{"feasibility.go", "StatusWaitingHuman", "the PRD is delivered and the run waits for a go/no-go"},
	{"remediate.go", "StatusParked", "the remediation round cap is spent"},
	{"remediate.go", "StatusPROpen", "a remediation round pushed the task back to its open PR"},
	{"review.go", "StatusParked", "review is enabled but no reviewer is wired"},
	{"review.go", "StatusParked", "review confirmed an error-level finding"},
	{"steps.go", "StatusCompleted", "a no-change build closed the forge issue"},
	{"steps.go", "StatusCompleted", "a no-change build on a task with no forge issue"},
	{"steps.go", "StatusPROpen", "the PR opened"},
	{"steps.go", "StatusParked", "the diff exceeds diff_cap_lines"},
	{"triage.go", "StatusCompleted", "triage found no code change is needed"},
	{"triage.go", "StatusQueued", "triage routed the task to another workflow"},
}

// outcomeStatusIdentifiers resolves the identifiers above to the stored
// strings they alias (task_aliases.go re-exports them from the task package,
// which re-exports them from internal/taskstate), so the table can name a site
// the way the source does and still ask the transition table about it.
var outcomeStatusIdentifiers = map[string]string{
	"StatusQueued":       StatusQueued,
	"StatusRunning":      StatusRunning,
	"StatusWaitingHuman": StatusWaitingHuman,
	"StatusPROpen":       StatusPROpen,
	"StatusMerged":       StatusMerged,
	"StatusParked":       StatusParked,
	"StatusDead":         StatusDead,
	"StatusRejected":     StatusRejected,
	"StatusClosedWontDo": StatusClosedWontDo,
	"StatusCompleted":    StatusCompleted,
}

// TestOutcomeSitesAreTransitionsFromRunning holds the producer half of the
// transition contract: every status this package finishes a run with is one
// internal/taskstate lets a running task move to. It fails for an unadjudicated
// site (the table and the source disagree) and for a status the table forbids
// (the run would have produced a transition the State Store must refuse).
func TestOutcomeSitesAreTransitionsFromRunning(t *testing.T) {
	t.Parallel()

	want := make([]string, 0, len(outcomeSites))
	for _, site := range outcomeSites {
		want = append(want, site.file+" "+site.ident)
	}
	slices.Sort(want)

	found := outcomeSitesInSource(t)
	if !slices.Equal(found, want) {
		t.Errorf("the outcome sites in this package are %v, but the table here lists %v: add, remove or correct the entry (and adjudicate the status against internal/taskstate)", found, want)
	}

	for _, site := range outcomeSites {
		status, known := outcomeStatusIdentifiers[site.ident]
		if !known {
			t.Errorf("%s names %s, which is not a task status identifier; the table here cannot be asked about it", site.file, site.ident)
			continue
		}
		if !taskstate.CanTransition(taskstate.Running, status) {
			t.Errorf("%s finishes the run as %s (%q), which internal/taskstate does not allow from %s: the task would end in a transition the State Store has to refuse", site.file, site.ident, status, taskstate.Running)
		}
	}
}

// outcomeSitesInSource returns every tc.Outcome assignment in this package's
// non-test sources as "<file> <status identifier>", sorted, duplicates kept: a
// file with two distinct outcome decisions has to be two entries here.
func outcomeSitesInSource(t *testing.T) []string {
	t.Helper()

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read the package directory: %v", err)
	}
	found := make([]string, 0, len(outcomeSites))
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(token.NewFileSet(), name, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		ast.Inspect(file, func(node ast.Node) bool {
			literal, ok := node.(*ast.CompositeLit)
			if !ok {
				return true
			}
			kind, ok := literal.Type.(*ast.Ident)
			if !ok || kind.Name != "Outcome" {
				return true
			}
			for _, element := range literal.Elts {
				field, ok := element.(*ast.KeyValueExpr)
				if !ok {
					continue
				}
				key, ok := field.Key.(*ast.Ident)
				if !ok || key.Name != "Status" {
					continue
				}
				status, ok := field.Value.(*ast.Ident)
				if !ok {
					// An expression this test cannot read by name is still a
					// site that has to be adjudicated, so it lands in the
					// comparison as a mismatch rather than being skipped.
					found = append(found, name+" <expression>")
					continue
				}
				found = append(found, name+" "+status.Name)
			}
			return true
		})
	}
	slices.Sort(found)
	return found
}
