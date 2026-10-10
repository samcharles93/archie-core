package forgerpc

import (
	"context"
	"errors"
	"testing"

	"github.com/samcharles93/archie-core/internal/domain/storepkg"
	"github.com/samcharles93/archie-core/internal/domain/workflow"
)

type acceptedByPackage map[string]storepkg.Authority

func (a acceptedByPackage) AcceptedAuthority(_ context.Context, name string) (storepkg.Authority, error) {
	if authority, ok := a[name]; ok {
		return authority, nil
	}
	return storepkg.Authority{}, errors.New("not installed")
}

type runsByToken map[string]*workflow.Task

func (r runsByToken) TaskForCredential(_ context.Context, token string) (*workflow.Task, error) {
	if task, ok := r[token]; ok {
		return task, nil
	}
	return nil, errors.New("unknown")
}

// TestForgeCallStaysInItsRunsAuthority pins that a forge call is authorized
// only by a live run credential, on its identity's server, for its own
// repository, and for a package's workflow only within the forge permissions
// accepted for that package.
func TestForgeCallStaysInItsRunsAuthority(t *testing.T) {
	packaged := "package: triage\nid: triage\n"
	s := &Server{
		Runs: runsByToken{
			"live":    {Identity: "bot", Owner: "acme", Repo: "app"},
			"pkg":     {Identity: "bot", Owner: "acme", Repo: "app", WorkflowDefinitionYAML: packaged},
			"removed": {Identity: "bot", Owner: "acme", Repo: "app", WorkflowDefinitionYAML: "package: gone\nid: gone\n"},
		},
		Authorities: acceptedByPackage{"triage": {ForgePermissions: []string{"comment"}}},
	}
	own := func(credential string) Target { return Target{Credential: credential, Owner: "acme", Repo: "app"} }
	tests := []struct {
		name       string
		identity   string
		target     Target
		permission string
		allowed    bool
	}{
		{"own repository", "bot", own("live"), "comment", true},
		{"another repository", "bot", Target{Credential: "live", Owner: "acme", Repo: "other"}, "comment", false},
		{"another identity's server", "other", own("live"), "comment", false},
		{"unknown credential", "bot", own("gone"), "comment", false},
		{"no credential", "bot", Target{Owner: "acme", Repo: "app"}, "comment", false},
		{"operator workflow is not bounded by a package", "bot", own("live"), "open_pr", true},
		{"package workflow within its accepted permissions", "bot", own("pkg"), "comment", true},
		{"package workflow beyond its accepted permissions", "bot", own("pkg"), "open_pr", false},
		{"package workflow whose package is gone", "bot", own("removed"), "comment", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := s.authorize(tt.identity, tt.target, tt.permission); (err == nil) != tt.allowed {
				t.Fatalf("err %v, want allowed %v", err, tt.allowed)
			}
		})
	}
}
