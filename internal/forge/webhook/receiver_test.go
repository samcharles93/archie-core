package webhook

import (
	"testing"

	"github.com/samcharles93/archie-core/internal/forge"
)

// An issue leaves dispatch when it is closed or loses what made it work; an
// unlabel or unassign that still leaves it eligible withdraws nothing.
func TestWithdrawal(t *testing.T) {
	r := New("", nil, "either", "archie", "bot", nil, nil, nil, nil)
	tests := []struct {
		name  string
		event forge.IssueEvent
		want  bool
	}{
		{"closed", forge.IssueEvent{Action: "closed", State: "closed"}, true},
		{"label removed, still assigned", forge.IssueEvent{Action: "unlabeled", Assignees: []string{"bot"}}, false},
		{"label removed, nothing left", forge.IssueEvent{Action: "unlabeled"}, true},
		{"unassigned, still labelled", forge.IssueEvent{Action: "unassigned", Labels: []string{"archie"}}, false},
		{"unassigned, nothing left", forge.IssueEvent{Action: "unassigned"}, true},
		{"edited", forge.IssueEvent{Action: "edited"}, false},
		{"a pull request closing", forge.IssueEvent{Action: "closed", PullRequest: true}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.event.Owner, tt.event.Repo = "acme", "widget"
			if _, got := r.withdrawal(&tt.event); got != tt.want {
				t.Fatalf("withdrawal = %v, want %v", got, tt.want)
			}
		})
	}
}
