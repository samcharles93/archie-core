package taskstate

import "testing"

// The rules must be the same wherever an operator acts. These cases are the
// contract both the dashboard and chat are held to; a divergence between them
// is what this package exists to prevent.
func TestActionRules(t *testing.T) {
	tests := []struct {
		status                  string
		approve, retry, decline bool
		terminal                bool
	}{
		{status: Queued, decline: true},
		{status: Running, decline: true},
		{status: WaitingHuman, approve: true, decline: true},
		{status: PROpen, decline: true},
		{status: Parked, retry: true},
		{status: Merged, terminal: true},
		{status: Rejected, terminal: true},
		{status: Dead, terminal: true},
		{status: Declined, terminal: true},
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
	// Declining is deliberately permissive: refusing work archie does not
	// recognise is safer than being unable to stop it.
	if err := CheckDecline(unknown); err != nil {
		t.Errorf("CheckDecline refused an unknown status: %v", err)
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
