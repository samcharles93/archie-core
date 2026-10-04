package worktreerpc

import (
	"context"
	"errors"
	"testing"

	"github.com/samcharles93/archie-core/internal/domain/workflow"
)

type runsByToken map[string]*workflow.Task

func (r runsByToken) TaskForCredential(_ context.Context, token string) (*workflow.Task, error) {
	if task, ok := r[token]; ok {
		return task, nil
	}
	return nil, errors.New("unknown")
}

// TestPushNeedsTheRunsOwnCredential pins that a push is authorized only by a
// live run credential, only on the server of the run's identity, and only for
// that run's branch.
func TestPushNeedsTheRunsOwnCredential(t *testing.T) {
	runs := runsByToken{
		"live": {ID: 1, Identity: "bot", Owner: "o", Repo: "r", IssueNumber: 3, Branch: "archie/3"},
	}
	tests := []struct {
		name, token, identity string
		allowed               bool
	}{
		{"own run on own server", "live", "bot", true},
		{"unknown credential", "gone", "bot", false},
		{"another identity's server", "live", "other", false},
		{"no credential", "", "bot", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			task, err := publishable(context.Background(), runs, tt.token, tt.identity)
			if (err == nil) != tt.allowed {
				t.Fatalf("err %v, want allowed %v", err, tt.allowed)
			}
			if tt.allowed && task.Branch != "archie/3" {
				t.Fatalf("pushed branch %q", task.Branch)
			}
		})
	}
}
