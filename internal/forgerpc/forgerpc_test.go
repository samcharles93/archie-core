package forgerpc

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

// TestForgeCallStaysInItsRunsRepository pins that a forge call is authorized
// only by a live run credential, on its identity's server, for its own
// repository.
func TestForgeCallStaysInItsRunsRepository(t *testing.T) {
	s := &Server{Runs: runsByToken{"live": {Identity: "bot", Owner: "acme", Repo: "app"}}}
	tests := []struct {
		name     string
		identity string
		target   Target
		allowed  bool
	}{
		{"own repository", "bot", Target{Credential: "live", Owner: "acme", Repo: "app"}, true},
		{"another repository", "bot", Target{Credential: "live", Owner: "acme", Repo: "other"}, false},
		{"another identity's server", "other", Target{Credential: "live", Owner: "acme", Repo: "app"}, false},
		{"unknown credential", "bot", Target{Credential: "gone", Owner: "acme", Repo: "app"}, false},
		{"no credential", "bot", Target{Owner: "acme", Repo: "app"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := s.authorize(tt.identity, tt.target); (err == nil) != tt.allowed {
				t.Fatalf("err %v, want allowed %v", err, tt.allowed)
			}
		})
	}
}
