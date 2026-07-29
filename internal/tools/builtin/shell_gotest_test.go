package builtin

import (
	"strings"
	"testing"
)

const verboseGoTestOutput = `=== RUN   TestAlpha
--- PASS: TestAlpha (0.00s)
=== RUN   TestBeta
    beta_test.go:12: some chatty log line
--- PASS: TestBeta (0.01s)
PASS
ok  	github.com/example/pkg	0.014s
?   	github.com/example/pkg/empty	[no test files]
ok  	github.com/example/other	(cached)
`

func TestCollapseGoTest_KeepsOnlyPackageSummaries(t *testing.T) {
	got, collapsed := collapseGoTestOutput("go test ./... -v", verboseGoTestOutput, true)
	if !collapsed {
		t.Fatal("expected a passing go test run to be collapsed")
	}
	for _, want := range []string{
		"ok  \tgithub.com/example/pkg\t0.014s",
		"?   \tgithub.com/example/pkg/empty\t[no test files]",
		"ok  \tgithub.com/example/other\t(cached)",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("summary line missing: %q\ngot:\n%s", want, got)
		}
	}
	for _, unwanted := range []string{"=== RUN", "--- PASS", "chatty log line"} {
		if strings.Contains(got, unwanted) {
			t.Errorf("per-test noise %q survived:\n%s", unwanted, got)
		}
	}
	if !strings.Contains(got, "collapsed") {
		t.Errorf("expected a notice explaining the collapse, got:\n%s", got)
	}
}

// A failing run is the case where the detail is the whole point.
func TestCollapseGoTest_LeavesFailingRunsAlone(t *testing.T) {
	failing := verboseGoTestOutput + "--- FAIL: TestGamma (0.00s)\nFAIL\n"
	got, collapsed := collapseGoTestOutput("go test ./...", failing, false)
	if collapsed {
		t.Fatal("a failing run must not be collapsed")
	}
	if got != failing {
		t.Fatal("a failing run's output must be returned verbatim")
	}
}

func TestCollapseGoTest_IgnoresOtherCommands(t *testing.T) {
	for _, cmd := range []string{"git status", "go build ./...", "gotestsum --format short"} {
		if _, collapsed := collapseGoTestOutput(cmd, verboseGoTestOutput, true); collapsed {
			t.Errorf("%q must not be treated as a go test invocation", cmd)
		}
	}
}

// Nothing to gain from collapsing output that is already just summaries, and
// doing so would append a confusing notice.
func TestCollapseGoTest_SkipsAlreadyTerseOutput(t *testing.T) {
	terse := "ok  \tgithub.com/example/pkg\t0.014s\n"
	if _, collapsed := collapseGoTestOutput("go test ./...", terse, true); collapsed {
		t.Error("already-terse output should be left alone")
	}
}

// Guard against collapsing a run that produced no recognisable summary at all,
// which would leave the model with nothing.
func TestCollapseGoTest_SkipsWhenNoSummaryLines(t *testing.T) {
	odd := "=== RUN   TestAlpha\n--- PASS: TestAlpha (0.00s)\nPASS\n"
	if _, collapsed := collapseGoTestOutput("go test ./...", odd, true); collapsed {
		t.Error("output with no package summary lines should be left alone")
	}
}
