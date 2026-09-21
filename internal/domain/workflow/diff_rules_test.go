package workflow

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/worktree"
)

// errDiffUnavailable is what a Trees implementation reports when the committed
// change could not be read.
var errDiffUnavailable = errors.New("diff unavailable")

// diffTwoFiles is a committed change whose added lines carry every position a
// rule's file/line attribution can get wrong: two hunks in one Go file, a match
// on an added line in each, a removed line that matches (and must not be
// reported, because a removed line has no new-file position), a CONTEXT line
// that matches (and must not be reported, because the rules read what the
// change adds), and a second file whose added line matches.
const diffTwoFiles = `diff --git a/main.go b/main.go
index 1111111..2222222 100644
--- a/main.go
+++ b/main.go
@@ -1,4 +1,5 @@
 package main
-func greet() { println("hi") }
+func greet() { println("hi") }
+func boom() { panic("boom") }
 // panic( mentioned in a kept comment
 func main() { greet() }
@@ -20,3 +22,3 @@ func tail() {
 	keep(t)
-	panic("old")
+	panic("later")
 }
diff --git a/notes.md b/notes.md
index 3333333..4444444 100644
--- a/notes.md
+++ b/notes.md
@@ -1,1 +1,2 @@
 # notes
+panic( in a doc line
`

// diffTestFile is a change whose only matching added line is in a _test.go
// file, so a rule's exclude_path is the difference between parking and not.
const diffTestFile = `diff --git a/main_test.go b/main_test.go
index 5555555..6666666 100644
--- a/main_test.go
+++ b/main_test.go
@@ -1,1 +1,2 @@
 package main
+func TestBoom(t *testing.T) { panic("boom") }
`

// stepSettingsNode decodes a step's settings the way ParseDefinition hands them
// to a factory. An empty src is the zero Node a step written without a settings
// key carries, which is not the same value as an empty mapping.
func stepSettingsNode(t *testing.T, src string) yaml.Node {
	t.Helper()
	if src == "" {
		return yaml.Node{}
	}
	var document yaml.Node
	if err := yaml.Unmarshal([]byte(src), &document); err != nil {
		t.Fatalf("parse settings YAML: %v", err)
	}
	if len(document.Content) != 1 {
		t.Fatalf("settings YAML decoded to %d nodes, want one", len(document.Content))
	}
	return *document.Content[0]
}

// diffRulesStage builds the stage through the registered step type's factory,
// so no test can drive a stage the YAML route could not compile.
func diffRulesStage(t *testing.T, settingsYAML string) (Stage, error) {
	t.Helper()
	return DiffRulesStepType().Factory(stepSettingsNode(t, settingsYAML))
}

// diffRulesTaskContext is the minimum a diff-rules stage runs against: a
// prepared worktree, a base branch to diff against, and a log to read back.
func diffRulesTaskContext(diff string, log *bytes.Buffer) *TaskContext {
	return &TaskContext{
		Task:  &Task{ID: 1, Owner: "acme", Repo: "todo", IssueNumber: 42},
		Repo:  config.Repo{Owner: "acme", Name: "todo", Base: "main"},
		Trees: &fakeTrees{diff: diff},
		Dir:   "/tmp/diff-rules-worktree",
		Log:   slog.New(slog.NewTextHandler(log, nil)),
	}
}

// TestDiffRulesStepTypeIsRegisteredAndNamesItself pins the identity the
// vocabulary is keyed on: the word an operator writes in YAML, and the stage
// name that appears in the run's events -- they are the same name, so a
// workflow definition and the timeline agree about which step ran.
func TestDiffRulesStepTypeIsRegisteredAndNamesItself(t *testing.T) {
	t.Parallel()

	stepType := DiffRulesStepType()
	if stepType.Name != DiffRulesStepName {
		t.Errorf("step type name = %q, want %q", stepType.Name, DiffRulesStepName)
	}
	if DiffRulesStepName != "gate.diff-rules" {
		t.Errorf("DiffRulesStepName = %q, want the registered vocabulary word %q", DiffRulesStepName, "gate.diff-rules")
	}
	stage, err := diffRulesStage(t, "rules:\n  - level: error\n    pattern: 'panic\\('\n    message: no new panic\n")
	if err != nil {
		t.Fatalf("factory: %v", err)
	}
	if stage.Name != stepType.Name {
		t.Errorf("compiled stage = %q, want the step type's own name %q", stage.Name, stepType.Name)
	}
	if stage.Run == nil {
		t.Error("compiled stage has no Run: it would silently do nothing")
	}
}

// TestDiffRulesStepRefusesSettingsItCannotApply is the validation contract, and
// it is where invalid state has to be refused: a stored definition that reaches
// a run is compiled on the executing side, so a setting this step cannot apply
// must fail at the factory -- during validation and compile -- rather than be
// quietly dropped when the stage runs.
func TestDiffRulesStepRefusesSettingsItCannotApply(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		settings string
		want     string
	}{
		{
			name: "no settings at all",
			want: "requires settings",
		},
		{
			name:     "no rules",
			settings: "rules: []",
			want:     "at least one rule",
		},
		{
			name:     "rules key with no rules in it",
			settings: "rules:",
			want:     "at least one rule",
		},
		{
			name:     "settings that are not a mapping",
			settings: "- rules",
			want:     DiffRulesStepName + " settings",
		},
		{
			name:     "settings that are a scalar",
			settings: "just text",
			want:     DiffRulesStepName + " settings",
		},
		{
			name:     "unknown settings key",
			settings: "rulez:\n  - level: error\n    pattern: 'x'\n    message: m\n",
			want:     "rulez",
		},
		{
			name:     "rule with no level",
			settings: "rules:\n  - pattern: 'x'\n    message: m\n",
			want:     "level is required",
		},
		{
			name:     "rule with an unknown level",
			settings: "rules:\n  - level: fatal\n    pattern: 'x'\n    message: m\n",
			want:     `unknown level "fatal"`,
		},
		{
			name:     "rule with an unknown key",
			settings: "rules:\n  - level: error\n    pattern: 'x'\n    message: m\n    severity: high\n",
			want:     "severity",
		},
		{
			name:     "rule with no pattern",
			settings: "rules:\n  - level: error\n    message: m\n",
			want:     "pattern is required",
		},
		{
			name:     "rule with a pattern that is not a regex",
			settings: "rules:\n  - level: error\n    pattern: 'panic('\n    message: m\n",
			want:     "pattern",
		},
		{
			name:     "rule with no message",
			settings: "rules:\n  - level: error\n    pattern: 'x'\n",
			want:     "message is required",
		},
		{
			name:     "rule with a path that is not a regex",
			settings: "rules:\n  - level: error\n    pattern: 'x'\n    message: m\n    path: '('\n",
			want:     "path",
		},
		{
			name:     "rule with an exclude_path that is not a regex",
			settings: "rules:\n  - level: error\n    pattern: 'x'\n    message: m\n    exclude_path: '('\n",
			want:     "exclude_path",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			stage, err := diffRulesStage(t, test.settings)
			if err == nil {
				t.Fatalf("factory accepted unusable settings and built %q; want a refusal naming %q", stage.Name, test.want)
			}
			if !strings.Contains(err.Error(), test.want) {
				t.Errorf("factory error = %v, want it to name %q", err, test.want)
			}
		})
	}
}

// TestDiffRulesStepAppliesRulesToTheCommittedDiff is the behaviour contract: the
// rules read the diff against the base branch, report the added lines they
// match with their new-file line numbers, park the run on an error-level
// finding, and leave a warn-level one advisory.
func TestDiffRulesStepAppliesRulesToTheCommittedDiff(t *testing.T) {
	t.Parallel()

	panicRule := "rules:\n" +
		"  - id: no-new-panic\n" +
		"    level: error\n" +
		"    pattern: 'panic\\('\n" +
		"    message: new panic() call  --  use error returns instead\n"

	tests := []struct {
		name       string
		settings   string
		diff       string
		wantParked bool
		wantDetail []string
		omitDetail []string
		wantLog    []string
		omitLog    []string
	}{
		{
			name:       "an error rule reports every added line it matches",
			settings:   panicRule,
			diff:       diffTwoFiles,
			wantParked: true,
			wantDetail: []string{
				"main.go:3: new panic() call",
				"main.go:23: new panic() call",
				"notes.md:2: new panic() call",
				"(no-new-panic)",
			},
			// The removed line is at old-file line 21 and the kept comment is at
			// new-file line 4: neither is something this change added.
			omitDetail: []string{"main.go:21", "main.go:4:", "notes.md:1"},
			// added_lines=4 is the run's evidence that the rules read the change
			// (two added lines in the first hunk, one in the second, one in the
			// other file). A step that read nothing must not leave the same trace.
			wantLog: []string{"rule_level=error", "file=main.go line=3", "rule=no-new-panic", "added_lines=4"},
		},
		{
			name:       "a warn rule is advisory",
			settings:   "rules:\n  - id: advisory\n    level: warn\n    pattern: 'panic\\('\n    message: consider error returns\n",
			diff:       diffTwoFiles,
			wantParked: false,
			wantLog:    []string{"rule_level=warn", "file=main.go line=3", "consider error returns"},
		},
		{
			name:       "a rule that matches nothing is a no-op",
			settings:   "rules:\n  - level: error\n    pattern: 'this appears nowhere'\n    message: unreachable\n",
			diff:       diffTwoFiles,
			wantParked: false,
			omitLog:    []string{"unreachable"},
		},
		{
			name:       "context lines are not added lines",
			settings:   "rules:\n  - level: error\n    pattern: 'mentioned in a kept comment'\n    message: kept comment matched\n",
			diff:       diffTwoFiles,
			wantParked: false,
			omitDetail: []string{"main.go:4"},
			omitLog:    []string{"kept comment matched"},
		},
		{
			name:       "a path scope keeps the rule off other files",
			settings:   "rules:\n  - level: error\n    pattern: 'panic\\('\n    path: '\\.go$'\n    message: go only\n",
			diff:       diffTwoFiles,
			wantParked: true,
			wantDetail: []string{"main.go:3", "go only"},
			omitDetail: []string{"notes.md:2"},
		},
		{
			name:       "an exclude_path spares the files it names",
			settings:   "rules:\n  - level: error\n    pattern: 'panic\\('\n    path: '\\.go$'\n    exclude_path: '_test\\.go$'\n    message: production code only\n",
			diff:       diffTestFile,
			wantParked: false,
			omitLog:    []string{"production code only"},
		},
		{
			name:       "the same rule without the exclude_path matches the test file",
			settings:   "rules:\n  - level: error\n    pattern: 'panic\\('\n    path: '\\.go$'\n    message: any go file\n",
			diff:       diffTestFile,
			wantParked: true,
			wantDetail: []string{"main_test.go:2", "any go file"},
		},
		{
			name:       "no committed change is a no-op",
			settings:   panicRule,
			diff:       "",
			wantParked: false,
			omitLog:    []string{"no-new-panic"},
			wantLog:    []string{"added_lines=0"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			stage, err := diffRulesStage(t, test.settings)
			if err != nil {
				t.Fatalf("factory: %v", err)
			}
			var log bytes.Buffer
			tc := diffRulesTaskContext(test.diff, &log)

			if err := stage.Run(context.Background(), tc); err != nil {
				t.Fatalf("stage.Run() = %v, want nil: a finding is a verdict about the task, not a stage failure", err)
			}

			detail := tc.Outcome.Detail
			if test.wantParked {
				if tc.Outcome.Status != StatusParked {
					t.Fatalf("Outcome = %+v, want a parked outcome", tc.Outcome)
				}
			} else if tc.Outcome.Status != "" {
				t.Errorf("Outcome = %+v, want no outcome: nothing the change did violated the rules", tc.Outcome)
			}
			for _, want := range test.wantDetail {
				if !strings.Contains(detail, want) {
					t.Errorf("park Detail = %q, want it to contain %q", detail, want)
				}
			}
			for _, omit := range test.omitDetail {
				if strings.Contains(detail, omit) {
					t.Errorf("park Detail = %q, want it NOT to contain %q", detail, omit)
				}
			}
			for _, want := range test.wantLog {
				if !strings.Contains(log.String(), want) {
					t.Errorf("log = %q, want it to contain %q", log.String(), want)
				}
			}
			for _, omit := range test.omitLog {
				if strings.Contains(log.String(), omit) {
					t.Errorf("log = %q, want it NOT to contain %q", log.String(), omit)
				}
			}
		})
	}
}

// TestDiffRulesStepRequiresAPreparedWorktree pins the wiring failure a
// mis-ordered definition produces: a step placed before the workflow prepared a
// worktree has nothing to diff, and must say so instead of handing git an empty
// directory and parking with a git error.
func TestDiffRulesStepRequiresAPreparedWorktree(t *testing.T) {
	t.Parallel()

	stage, err := diffRulesStage(t, "rules:\n  - level: error\n    pattern: 'x'\n    message: m\n")
	if err != nil {
		t.Fatalf("factory: %v", err)
	}
	var log bytes.Buffer
	tc := diffRulesTaskContext("", &log)
	tc.Dir = ""

	err = stage.Run(context.Background(), tc)
	if err == nil {
		t.Fatal("stage.Run() = nil with no worktree, want the wiring failure")
	}
	if !strings.Contains(err.Error(), "worktree") {
		t.Errorf("stage.Run() error = %v, want it to name the missing worktree", err)
	}
}

// TestDiffRulesStepPropagatesTheDiffFailure pins that a diff this stage could
// not read is an execution failure, not a clean change: reporting "no findings"
// because the read failed would pass a gate that never ran.
func TestDiffRulesStepPropagatesTheDiffFailure(t *testing.T) {
	t.Parallel()

	stage, err := diffRulesStage(t, "rules:\n  - level: error\n    pattern: 'x'\n    message: m\n")
	if err != nil {
		t.Fatalf("factory: %v", err)
	}
	var log bytes.Buffer
	tc := diffRulesTaskContext("", &log)
	tc.Trees = &fakeTrees{diffErr: errDiffUnavailable}

	err = stage.Run(context.Background(), tc)
	if err == nil {
		t.Fatal("stage.Run() = nil when the diff could not be read, want the read failure")
	}
	if !strings.Contains(err.Error(), "diff") {
		t.Errorf("stage.Run() error = %v, want it to name the diff it could not read", err)
	}
	if tc.Outcome.Status != "" {
		t.Errorf("Outcome = %+v, want none: a read failure is not a verdict", tc.Outcome)
	}
}

// TestAddedDiffLinesAttributesNewFilePositions drives the walker on its own,
// because the line a finding carries is the difference between an operator
// being able to find the violation and not.
func TestAddedDiffLinesAttributesNewFilePositions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		diff string
		want []diffLine
	}{
		{
			name: "one hunk",
			diff: "diff --git a/a.go b/a.go\n--- a/a.go\n+++ b/a.go\n@@ -1,2 +1,3 @@\n package a\n+var b = 1\n var c = 2\n",
			want: []diffLine{{Path: "a.go", Line: 2, Text: "var b = 1"}},
		},
		{
			name: "a second hunk keeps counting from its own header",
			diff: "diff --git a/a.go b/a.go\n--- a/a.go\n+++ b/a.go\n@@ -1,1 +1,2 @@\n package a\n+var b = 1\n@@ -40,1 +41,2 @@\n func f() {}\n+func g() {}\n",
			want: []diffLine{{Path: "a.go", Line: 2, Text: "var b = 1"}, {Path: "a.go", Line: 42, Text: "func g() {}"}},
		},
		{
			name: "a removed line advances no new-file position",
			diff: "diff --git a/a.go b/a.go\n--- a/a.go\n+++ b/a.go\n@@ -1,3 +1,2 @@\n package a\n-var gone = 1\n+var kept = 2\n var tail = 3\n",
			want: []diffLine{{Path: "a.go", Line: 2, Text: "var kept = 2"}},
		},
		{
			name: "a header for the next file moves the path, not the position",
			diff: "diff --git a/a.go b/a.go\n--- a/a.go\n+++ b/a.go\n@@ -1,1 +1,2 @@\n package a\n+var b = 1\ndiff --git a/b.go b/b.go\n--- a/b.go\n+++ b/b.go\n@@ -1,1 +1,2 @@\n package b\n+var c = 2\n",
			want: []diffLine{{Path: "a.go", Line: 2, Text: "var b = 1"}, {Path: "b.go", Line: 2, Text: "var c = 2"}},
		},
		{
			// A body line whose own text begins with the file-header marker is
			// still a body line: inside a hunk, `+++ x` is the added line `++ x`.
			name: "an added line that looks like a file header is body text",
			diff: "diff --git a/a.md b/a.md\n--- a/a.md\n+++ b/a.md\n@@ -1,1 +1,2 @@\n # a\n+++ title\n",
			want: []diffLine{{Path: "a.md", Line: 2, Text: "++ title"}},
		},
		{
			name: "no newline marker is not a line",
			diff: "diff --git a/a.go b/a.go\n--- a/a.go\n+++ b/a.go\n@@ -1,1 +1,2 @@\n package a\n+var b = 1\n\\ No newline at end of file\n",
			want: []diffLine{{Path: "a.go", Line: 2, Text: "var b = 1"}},
		},
		{
			name: "an empty diff adds nothing",
			diff: "",
			want: nil,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got := addedDiffLines(test.diff)
			if len(got) != len(test.want) {
				t.Fatalf("addedDiffLines() = %+v, want %+v", got, test.want)
			}
			for i := range got {
				if got[i] != test.want[i] {
					t.Errorf("addedDiffLines()[%d] = %+v, want %+v", i, got[i], test.want[i])
				}
			}
		})
	}
}

// diffRulesTestTaskContext is the same context a diff-rules stage runs against,
// for a test that drives a real worktree instead of a fake one.
func diffRulesTestTaskContext(t *testing.T, dir string, log *bytes.Buffer) *TaskContext {
	t.Helper()
	tc := diffRulesTaskContext("", log)
	tc.Dir = dir
	return tc
}

// TestDiffRulesStepReadsARealDiff drives the step against the production Trees
// implementation rather than a fixture: every other test here feeds a
// hand-written unified diff, and the positions a rule reports have to survive
// what a real worktree actually emits -- headers, index lines and all.
func TestDiffRulesStepReadsARealDiff(t *testing.T) {
	t.Parallel()

	dir := gitRepoWithOriginRef(t, "main")
	added := "package main\n\nvar a = 1\n\nfunc boom() { panic(\"boom\") }\n"
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(added), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-m", "add panic")

	stage, err := diffRulesStage(t, "rules:\n  - id: no-new-panic\n    level: error\n    pattern: 'panic\\('\n    message: new panic() call\n")
	if err != nil {
		t.Fatalf("factory: %v", err)
	}
	var log bytes.Buffer
	tc := diffRulesTestTaskContext(t, dir, &log)
	tc.Trees = &worktree.Manager{WorkDir: t.TempDir()}

	if err := stage.Run(context.Background(), tc); err != nil {
		t.Fatalf("stage.Run(): %v", err)
	}
	if tc.Outcome.Status != StatusParked {
		t.Fatalf("Outcome = %+v, want a parked outcome for the added panic", tc.Outcome)
	}
	// The panic is on line 5 of the file the change produces. A walker that
	// counted the diff's own offsets would report a different number.
	if !strings.Contains(tc.Outcome.Detail, "main.go:5: new panic() call") {
		t.Errorf("park Detail = %q, want it to locate the added line as main.go:5", tc.Outcome.Detail)
	}
}

// bareTrees is a workflow.Trees implementation that cannot report uncommitted
// work: it exposes the interface by delegation, so no optional capability rides
// along with it.
type bareTrees struct{ Trees }

// errUncommittedUnavailable is what a Trees implementation reports when it could
// not read whether the worktree holds uncommitted work.
var errUncommittedUnavailable = errors.New("status unavailable")

// TestDiffRulesStepRefusesToCheckAChangeThatIsNotCommitted is the placement
// contract. The step reads what the branch has COMMITTED, and only a worktree
// with uncommitted work left in it can tell "this change is not committed yet"
// apart from "there is no change" -- so a step placed before the step that
// commits must fail loudly rather than report no findings and let the run
// proceed, which is a gate that never looked.
func TestDiffRulesStepRefusesToCheckAChangeThatIsNotCommitted(t *testing.T) {
	t.Parallel()

	const rule = "rules:\n  - id: no-new-panic\n    level: error\n    pattern: 'panic\\('\n    message: new panic() call\n"

	tests := []struct {
		name           string
		diff           string
		uncommitted    bool
		uncommittedErr error
		// trees overrides the fake with a variant, e.g. one that hides the
		// optional capability.
		trees      func(*fakeTrees) Trees
		wantErr    []string
		wantParked bool
		wantLog    []string
	}{
		{
			name:        "uncommitted work with no committed change is refused",
			uncommitted: true,
			wantErr:     []string{"uncommitted", "must follow the step that commits"},
			wantLog:     []string{"added_lines=0"},
		},
		{
			name:    "a clean worktree with no committed change is a no-op",
			wantLog: []string{"added_lines=0"},
		},
		{
			name:    "a Trees that cannot report uncommitted work is refused, not passed",
			trees:   func(f *fakeTrees) Trees { return bareTrees{Trees: f} },
			wantErr: []string{"cannot report", "must follow the step that commits"},
		},
		{
			name:           "a failed uncommitted read is propagated",
			uncommittedErr: errUncommittedUnavailable,
			wantErr:        []string{"uncommitted"},
		},
		{
			name:        "a committed change is checked even when the worktree is dirty",
			diff:        diffTwoFiles,
			uncommitted: true,
			wantParked:  true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			stage, err := diffRulesStage(t, rule)
			if err != nil {
				t.Fatalf("factory: %v", err)
			}
			var log bytes.Buffer
			fake := &fakeTrees{diff: test.diff, uncommitted: test.uncommitted, uncommittedErr: test.uncommittedErr}
			tc := diffRulesTaskContext("", &log)
			tc.Trees = fake
			if test.trees != nil {
				tc.Trees = test.trees(fake)
			}

			err = stage.Run(context.Background(), tc)
			if len(test.wantErr) > 0 {
				requireRefusalNaming(t, err, test.wantErr, tc.Outcome)
			} else if err != nil {
				t.Fatalf("stage.Run() = %v, want nil", err)
			}
			if test.wantParked && tc.Outcome.Status != StatusParked {
				t.Errorf("Outcome = %+v, want a parked outcome for the committed violation", tc.Outcome)
			}
			for _, want := range test.wantLog {
				if !strings.Contains(log.String(), want) {
					t.Errorf("log = %q, want it to contain %q", log.String(), want)
				}
			}
		})
	}
}

// requireRefusalNaming checks the stage refused, that its error names every part
// the case expects, and that the refusal is not dressed up as a verdict: a step
// that could not check the change has no finding to park on.
func requireRefusalNaming(t *testing.T, err error, want []string, outcome Outcome) {
	t.Helper()
	if err == nil {
		t.Fatalf("stage.Run() = nil, want the refusal naming %v", want)
	}
	for _, fragment := range want {
		if !strings.Contains(err.Error(), fragment) {
			t.Errorf("stage.Run() error = %v, want it to name %q", err, fragment)
		}
	}
	if outcome.Status != "" {
		t.Errorf("Outcome = %+v, want none: a step that cannot check the change is a stage failure, not a verdict", outcome)
	}
}
