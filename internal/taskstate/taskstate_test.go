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
		{status: Queued, decline: true, actions: []Action{ActionCancel}},
		{status: Running, decline: true, actions: []Action{ActionStop}},
		{status: WaitingHuman, approve: true, decline: true, actions: []Action{ActionApprove, ActionReject}},
		{status: PROpen, actions: []Action{ActionOpenPR, ActionOpenIssue}},
		{status: Parked, retry: true, decline: true, actions: []Action{ActionRetry, ActionAbandon}},
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

func TestActionsReturnsIndependentSlices(t *testing.T) {
	first := Actions(WaitingHuman)
	first[0] = ActionArchive
	if got := Actions(WaitingHuman); !slices.Equal(got, []Action{ActionApprove, ActionReject}) {
		t.Fatalf("mutating returned actions changed lifecycle table: %v", got)
	}
}
