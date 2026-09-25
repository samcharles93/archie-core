package taskstate

import (
	"slices"
	"testing"
)

// The legal pairs are written out here rather than read from the map under
// test. A table-driven test that derived its expectation from the table would
// hold for any table, including an empty one.
var legalExecutionTransitions = []struct{ from, to string }{
	{Queued, Running},
	{Queued, Declined},
	{Running, Queued},
	{Running, WaitingHuman},
	{Running, PROpen},
	{Running, Completed},
	{Running, Parked},
	{Running, Declined},
	{WaitingHuman, Queued},
	{WaitingHuman, Declined},
	{Parked, Queued},
	{Parked, Dead},
	{Parked, Declined},
	{PROpen, Merged},
	{PROpen, Rejected},
	{PROpen, Queued},
	{PROpen, Declined},
}

var legalStepTransitions = []struct{ from, to StepStatus }{
	{StepPending, StepRunning},
	{StepPending, StepCancelled},
	{StepRunning, StepSucceeded},
	{StepRunning, StepFailed},
	{StepRunning, StepCancelled},
	{StepRunning, StepInterrupted},
}

func statusIDs() []string {
	ids := make([]string, 0, len(Statuses()))
	for _, meta := range Statuses() {
		ids = append(ids, meta.ID)
	}
	return ids
}

func execPair(from, to string) string { return from + " -> " + to }

// The whole cross product, not a sample: every pair the vocabulary can
// express is checked against the legal set, so an edge nobody meant to add
// fails here rather than at the store.
func TestExecutionTransitionTableIsExactlyThePRDTable(t *testing.T) {
	known := statusIDs()
	legal := map[string]bool{}
	for _, p := range legalExecutionTransitions {
		if !slices.Contains(known, p.from) || !slices.Contains(known, p.to) {
			t.Fatalf("legal pair %s names a status the catalog does not present", execPair(p.from, p.to))
		}
		if legal[execPair(p.from, p.to)] {
			t.Fatalf("duplicate legal pair %s", execPair(p.from, p.to))
		}
		legal[execPair(p.from, p.to)] = true
	}

	for _, from := range known {
		for _, to := range known {
			want := legal[execPair(from, to)]
			if got := CanTransition(from, to); got != want {
				t.Errorf("CanTransition(%s) = %v, want %v", execPair(from, to), got, want)
			}
		}
	}
}

// The PRD names one write the store accepts today and the table must refuse:
// a queued task cannot be born merged. It is the pair a table that only
// checked "is this a known status" would still let through.
func TestQueuedCannotReachMerged(t *testing.T) {
	if CanTransition(Queued, Merged) {
		t.Error("CanTransition(queued, merged) = true; the transition tables section names this pair as illegal")
	}
}

// The other pair written before the table existed, by every fixture that
// needed a finished task: a merge is observed on an open pull request, never
// straight from running. Named here so the next fixture that reaches for it is
// a failure in the table's own test rather than a surprise at the store.
func TestRunningCannotReachMerged(t *testing.T) {
	if CanTransition(Running, Merged) {
		t.Error("CanTransition(running, merged) = true; a merge is only observed on an open pull request")
	}
}

// An unknown status must be illegal in both positions. A status written by a
// newer version, or a typo, must not be able to start or end a transition.
func TestUnknownStatusIsNeverATransition(t *testing.T) {
	const unknown = "quantum_superposition"
	for _, status := range statusIDs() {
		if CanTransition(unknown, status) {
			t.Errorf("CanTransition(%s -> %s) = true", unknown, status)
		}
		if CanTransition(status, unknown) {
			t.Errorf("CanTransition(%s -> %s) = true", status, unknown)
		}
	}
	if CanTransition(unknown, unknown) {
		t.Errorf("CanTransition(%s -> %s) = true", unknown, unknown)
	}
	if CanStepTransition(StepStatus(unknown), StepPending) {
		t.Errorf("CanStepTransition(%s -> %s) = true", unknown, StepPending)
	}
	if CanStepTransition(StepRunning, StepStatus(unknown)) {
		t.Errorf("CanStepTransition(%s -> %s) = true", StepRunning, unknown)
	}
}

// Rows must cover the vocabulary, and only the vocabulary. A status added to
// the presentation catalog without a row would be unroutable -- no writer
// could move a task into or out of it -- and the failure would surface as a
// task stuck in a state, long after the edit.
func TestTransitionRowsCoverTheStatusVocabulary(t *testing.T) {
	known := map[string]bool{}
	for _, id := range statusIDs() {
		known[id] = true
		if _, ok := executionTransitions[id]; !ok {
			t.Errorf("status %q has no row in executionTransitions", id)
		}
	}
	for from := range executionTransitions {
		if !known[from] {
			t.Errorf("executionTransitions has a row for %q, which the catalog does not present", from)
		}
		for _, to := range executionTransitions[from] {
			if !known[to] {
				t.Errorf("executionTransitions row %q names %q, which the catalog does not present", from, to)
			}
		}
	}
}

// A terminal status has no way out and a live one must have at least one way
// forward: a live status with no edges strands every task that reaches it.
func TestTerminalAndRoutableRowsAgree(t *testing.T) {
	for _, id := range statusIDs() {
		if got, want := len(executionTransitions[id]) == 0, Terminal(id); got != want {
			t.Errorf("status %q: row is empty = %v, Terminal = %v", id, got, want)
		}
	}
	for _, status := range stepVocabulary() {
		if got, want := len(stepTransitions[status]) == 0, StepTerminal(status); got != want {
			t.Errorf("step %q: row is empty = %v, StepTerminal = %v", status, got, want)
		}
	}
}

func stepVocabulary() []StepStatus {
	return []StepStatus{StepPending, StepRunning, StepSucceeded, StepFailed, StepCancelled, StepInterrupted}
}

func TestStepTransitionTableIsExactlyThePRDTable(t *testing.T) {
	known := stepVocabulary()
	legal := map[StepStatus]bool{}
	for _, p := range legalStepTransitions {
		if !slices.Contains(known, p.from) || !slices.Contains(known, p.to) {
			t.Fatalf("legal pair %s -> %s names a step status not in the vocabulary", p.from, p.to)
		}
		legal[p.from+"->"+p.to] = true
	}

	for _, from := range known {
		for _, to := range known {
			want := legal[from+"->"+to]
			if got := CanStepTransition(from, to); got != want {
				t.Errorf("CanStepTransition(%s -> %s) = %v, want %v", from, to, got, want)
			}
		}
	}
}

func TestStepTransitionRowsCoverTheStepVocabulary(t *testing.T) {
	known := map[StepStatus]bool{}
	for _, status := range stepVocabulary() {
		known[status] = true
		if _, ok := stepTransitions[status]; !ok {
			t.Errorf("step status %q has no row in stepTransitions", status)
		}
	}
	for from := range stepTransitions {
		if !known[from] {
			t.Errorf("stepTransitions has a row for unknown step status %q", from)
		}
		for _, to := range stepTransitions[from] {
			if !known[to] {
				t.Errorf("stepTransitions row %q names unknown step status %q", from, to)
			}
		}
	}
}

// Two ways for a step to stop early, and they must not be confused:
// cancelled is someone asking, interrupted is the process dying.
func TestStepTerminal(t *testing.T) {
	terminals := []StepStatus{StepSucceeded, StepFailed, StepCancelled, StepInterrupted}
	for _, status := range stepVocabulary() {
		want := slices.Contains(terminals, status)
		if got := StepTerminal(status); got != want {
			t.Errorf("StepTerminal(%s) = %v, want %v", status, got, want)
		}
	}
	if StepTerminal(StepStatus("half_written")) {
		t.Error("StepTerminal(unknown) = true")
	}
}

// The step vocabulary is separate and stays out of the execution catalog: a
// step status listed in Statuses() would show the dashboard a task status no
// task can have.
func TestStepVocabularyStaysOutOfTheExecutionCatalog(t *testing.T) {
	ids := statusIDs()
	for _, status := range stepVocabulary() {
		if string(status) == Running {
			// The two vocabularies deliberately share this word.
			continue
		}
		if slices.Contains(ids, string(status)) {
			t.Errorf("step status %q is presented as an execution status", status)
		}
	}
}

// A step may only enter running while its execution is running, and never
// under a parent that is already terminal: a finished parent's children were
// cancelled or interrupted when it finished.
func TestCheckStepStart(t *testing.T) {
	tests := []struct {
		name           string
		execution      string
		parentTerminal bool
		wantErr        bool
	}{
		{name: "running execution, live parent", execution: Running},
		{name: "running execution, terminal parent", execution: Running, parentTerminal: true, wantErr: true},
		{name: "queued execution", execution: Queued, wantErr: true},
		{name: "execution waiting on a human", execution: WaitingHuman, wantErr: true},
		{name: "execution parked", execution: Parked, wantErr: true},
		{name: "execution with a pull request open", execution: PROpen, wantErr: true},
		{name: "terminal execution", execution: Merged, wantErr: true},
		{name: "unknown execution", execution: "quantum_superposition", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := CheckStepStart(tt.execution, tt.parentTerminal)
			if got := err != nil; got != tt.wantErr {
				t.Errorf("CheckStepStart(%q, %v) error = %v, want error = %v", tt.execution, tt.parentTerminal, err, tt.wantErr)
			}
		})
	}

	for _, id := range statusIDs() {
		if err := CheckStepStart(id, false); (err != nil) != (id != Running) {
			t.Errorf("CheckStepStart(%q, false) = %v, want error exactly when %q is not running", id, err, id)
		}
	}
}
