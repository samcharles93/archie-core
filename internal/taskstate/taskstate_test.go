package taskstate

import (
	"slices"
	"testing"
)

// The rules must be the same wherever an operator acts. These cases are the
// contract both the dashboard and chat are held to; a divergence between them
// is what this package exists to prevent.
func TestActionRules(t *testing.T) {
	tests := []struct {
		status                  string
		approve, retry, decline bool
		terminal                bool
		actions                 []Action
	}{
		{status: Queued, decline: true, actions: []Action{ActionCancel, ActionReject}},
		{status: Running, decline: true, actions: []Action{ActionStop, ActionReject}},
		{status: WaitingHuman, approve: true, decline: true, actions: []Action{ActionApprove, ActionReject}},
		{status: PROpen, decline: true, actions: []Action{ActionOpenPR, ActionOpenIssue, ActionReject}},
		{status: Parked, retry: true, decline: true, actions: []Action{ActionRetry, ActionAbandon, ActionReject}},
		{status: Merged, terminal: true, actions: []Action{ActionArchive}},
		{status: Rejected, terminal: true, actions: []Action{ActionArchive}},
		{status: Dead, terminal: true, actions: []Action{ActionArchive}},
		{status: Declined, terminal: true, actions: []Action{ActionArchive}},
	}

	for _, tc := range tests {
		t.Run(tc.status, func(t *testing.T) {
			if got := CheckApprove(tc.status) == nil; got != tc.approve {
				t.Errorf("CheckApprove allowed = %v, want %v", got, tc.approve)
			}
			if got := CheckRetry(tc.status) == nil; got != tc.retry {
				t.Errorf("CheckRetry allowed = %v, want %v", got, tc.retry)
			}
			if got := CheckDecline(tc.status) == nil; got != tc.decline {
				t.Errorf("CheckDecline allowed = %v, want %v", got, tc.decline)
			}
			if got := Terminal(tc.status); got != tc.terminal {
				t.Errorf("Terminal = %v, want %v", got, tc.terminal)
			}
			if got := Actions(tc.status); !slices.Equal(got, tc.actions) {
				t.Errorf("Actions = %v, want %v", got, tc.actions)
			}
			for _, action := range tc.actions {
				if err := CheckAction(tc.status, action); err != nil {
					t.Errorf("CheckAction(%q) = %v", action, err)
				}
			}
		})
	}
}

// An unknown status must not be silently actionable: a typo or a status from
// a newer version should refuse rather than approve.
func TestUnknownStatusIsNotApprovable(t *testing.T) {
	const unknown = "quantum_superposition"
	if err := CheckApprove(unknown); err == nil {
		t.Error("CheckApprove allowed an unknown status")
	}
	if err := CheckRetry(unknown); err == nil {
		t.Error("CheckRetry allowed an unknown status")
	}
	if err := CheckDecline(unknown); err == nil {
		t.Error("CheckDecline allowed an unknown status")
	}
}

// Rejected and Declined must stay distinct. Collapsing them would lose the
// difference between "the forge closed the PR" and "a human said no", which
// is the drift this package was created to end.
func TestRejectedAndDeclinedAreDistinct(t *testing.T) {
	if Rejected == Declined {
		t.Fatal("Rejected and Declined are the same string")
	}
	if !Terminal(Rejected) || !Terminal(Declined) {
		t.Error("both must be terminal")
	}
}

// The presentation catalog must cover every status and action the lifecycle
// exposes, so the frontend can render anything the rules allow without holding
// its own copy of the vocabulary.
func TestPresentationCatalog(t *testing.T) {
	allowedKinds := map[string]bool{"idle": true, "info": true, "ok": true, "warn": true, "danger": true}

	statuses := Statuses()
	if len(statuses) == 0 {
		t.Fatal("Statuses() returned no entries")
	}
	seen := map[string]bool{}
	for _, meta := range statuses {
		if meta.ID == "" || meta.Label == "" || !allowedKinds[meta.Kind] {
			t.Errorf("status %q: id=%q label=%q kind=%q", meta.ID, meta.ID, meta.Label, meta.Kind)
		}
		if seen[meta.ID] {
			t.Errorf("duplicate status id %q", meta.ID)
		}
		seen[meta.ID] = true
		if meta.NeedsYou && meta.Kind == "idle" {
			t.Errorf("status %q is marked needs_you but has idle severity", meta.ID)
		}
	}
	// Every lifecycle status must have presentation metadata.
	for _, status := range []string{Queued, Running, WaitingHuman, PROpen, Merged, Parked, Dead, Rejected, Declined} {
		if !seen[status] {
			t.Errorf("status %q missing from Statuses()", status)
		}
	}

	actions := ActionCatalog()
	if len(actions) == 0 {
		t.Fatal("ActionCatalog() returned no entries")
	}
	actionSeen := map[string]bool{}
	for _, meta := range actions {
		if meta.ID == "" || meta.Label == "" {
			t.Errorf("action %q: id=%q label=%q", meta.ID, meta.ID, meta.Label)
		}
		if actionSeen[meta.ID] {
			t.Errorf("duplicate action id %q", meta.ID)
		}
		actionSeen[meta.ID] = true
		// link actions navigate; the rest must carry a button kind.
		if meta.Kind != "link" && meta.Kind != "primary" && meta.Kind != "quiet" && meta.Kind != "danger" {
			t.Errorf("action %q has unknown kind %q", meta.ID, meta.Kind)
		}
	}

	// Every action the lifecycle table can emit must be describable, otherwise
	// the frontend has no way to present it.
	byID := func(status string) map[Action]bool {
		out := map[Action]bool{}
		for _, a := range Actions(status) {
			out[a] = true
		}
		return out
	}
	for _, status := range []string{Queued, Running, WaitingHuman, PROpen, Parked, Merged, Rejected, Dead, Declined} {
		for a := range byID(status) {
			if !actionSeen[string(a)] {
				t.Errorf("action %q from status %q has no ActionCatalog entry", a, status)
			}
		}
	}
}

func TestActionsReturnsIndependentSlices(t *testing.T) {
	first := Actions(WaitingHuman)
	first[0] = ActionArchive
	if got := Actions(WaitingHuman); !slices.Equal(got, []Action{ActionApprove, ActionReject}) {
		t.Fatalf("mutating returned actions changed lifecycle table: %v", got)
	}
}
