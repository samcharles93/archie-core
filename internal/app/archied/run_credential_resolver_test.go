package archied

import (
	"context"
	"errors"
	"testing"

	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/domain/workflow"
	"github.com/samcharles93/archie-core/internal/infrastructure/egress"
)

type runsByToken map[string]*workflow.Task

func (r runsByToken) TaskForCredential(_ context.Context, token string) (*workflow.Task, error) {
	if task, ok := r[token]; ok {
		return task, nil
	}
	return nil, errors.New("unknown")
}

type secretValues map[string]string

func (s secretValues) Resolve(ref config.SecretRef) (string, error) { return s[ref.Key], nil }

// TestEgressResolvesOnlyGrantedCredentials pins that the proxy hands a run
// only a secret its identity is granted and bound for in its own org.
func TestEgressResolvesOnlyGrantedCredentials(t *testing.T) {
	own := []string{"anthropic"}
	cfg := config.Config{
		GrantedCredentials: []string{"github"},
		Providers:          map[string]config.Provider{"openai": {Class: "openai"}, "deepseek": {Class: "deepseek"}},
		Identities:         []config.IdentityConfig{{Name: "acme-bot", Org: "acme", GrantedCredentials: &own}},
		Containers: config.ContainerConfig{Credentials: []config.CredentialBinding{
			{Service: "github", Secret: config.SecretRef{Engine: "env", Key: "GH"}},
			{Service: "anthropic", Secret: config.SecretRef{Engine: "env", Key: "ROOT_AI"}},
			{Service: "anthropic", Org: "acme", Secret: config.SecretRef{Engine: "env", Key: "ACME_AI"}},
			{Service: "openai", Secret: config.SecretRef{Engine: "env", Key: "ROOT_OPENAI"}},
			{Service: "openai", Org: "acme", Secret: config.SecretRef{Engine: "env", Key: "ACME_OPENAI"}},
			{Service: "deepseek", Secret: config.SecretRef{Engine: "env", Key: "ROOT_DEEPSEEK"}},
		}},
	}
	r := runCredentialResolver{
		runs:    runsByToken{"root-run": {ID: 1}, "acme-run": {ID: 2, Identity: "acme-bot"}},
		config:  config.NewHolder(cfg),
		secrets: secretValues{"GH": "gh-secret", "ROOT_AI": "root-ai", "ACME_AI": "acme-ai", "ROOT_OPENAI": "root-openai", "ACME_OPENAI": "acme-openai", "ROOT_DEEPSEEK": "root-deepseek"},
	}
	tests := []struct {
		name, credential, service, want string
	}{
		{"root run, granted", "root-run", "github", "gh-secret"},
		{"root run, not granted", "root-run", "anthropic", ""},
		{"identity run gets its own org's binding", "acme-run", "anthropic", "acme-ai"},
		{"identity grants replace the root's", "acme-run", "github", ""},
		{"unknown credential", "gone", "github", ""},
		{"a model provider key needs no grant", "root-run", "openai", "root-openai"},
		{"a model provider key is the run's own org's", "acme-run", "openai", "acme-openai"},
		{"another org's model provider key is unbound", "acme-run", "deepseek", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := r.Resolve(context.Background(), tt.credential, tt.service)
			if tt.want == "" {
				if !errors.Is(err, egress.ErrUnbound) {
					t.Fatalf("got %q, %v; want ErrUnbound", got, err)
				}
				return
			}
			if err != nil || got != tt.want {
				t.Fatalf("got %q, %v; want %q", got, err, tt.want)
			}
		})
	}
}

// TestEgressResolvesASessionGrant pins that a setup session's token, which
// names no task, resolves only the granted services of its own org. A grant
// that widened to another org's binding would hand the container a credential
// the operator's update-on-secret authorisation never covered.
func TestEgressResolvesASessionGrant(t *testing.T) {
	cfg := config.Config{Containers: config.ContainerConfig{Credentials: []config.CredentialBinding{
		{Service: "github", Org: "acme", Secret: config.SecretRef{Engine: "env", Key: "ACME_GH"}},
		{Service: "linear", Org: "acme"},
	}}}
	sessions := &sessionGrants{}
	sessions.Grant("acme-session", "acme", []string{"github", "linear"})
	sessions.Grant("other-session", "other", []string{"github"})
	r := runCredentialResolver{
		runs:     runsByToken{"acme-run": {ID: 1, Identity: "acme-bot"}},
		sessions: sessions,
		config:   config.NewHolder(cfg),
		secrets:  secretValues{"ACME_GH": "acme-gh"},
	}
	tests := []struct {
		name, credential, service, want string
		bound                           bool
	}{
		{name: "a bound api key resolves", credential: "acme-session", service: "github", want: "acme-gh", bound: true},
		{name: "a bound oauth service resolves to the proxy's own token set", credential: "acme-session", service: "linear", bound: true},
		{name: "a service outside the grant is unbound", credential: "acme-session", service: "nope"},
		{name: "another org's binding is unbound", credential: "other-session", service: "github"},
		{name: "an unknown token falls through to the task path", credential: "acme-run", service: "github"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := r.Resolve(context.Background(), tt.credential, tt.service)
			if !tt.bound {
				if !errors.Is(err, egress.ErrUnbound) {
					t.Fatalf("got %q, %v; want ErrUnbound", got, err)
				}
				return
			}
			if err != nil || got != tt.want {
				t.Fatalf("got %q, %v; want %q", got, err, tt.want)
			}
		})
	}
}
