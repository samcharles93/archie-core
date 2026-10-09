package egress

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/docker/sandbox-kit-spec/v3/spec"

	"github.com/samcharles93/archie-core/internal/domain/harnesssecret"
)

type phaseOAuthStore struct{}

func (phaseOAuthStore) GetHarnessSecret(context.Context, string, string) (harnesssecret.Secret, error) {
	return harnesssecret.Secret{AccessToken: "real-oauth"}, nil
}
func (phaseOAuthStore) PutHarnessSecret(context.Context, harnesssecret.Secret) error { return nil }

func TestCredentialPhaseEnforcement(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, phase      string
		install, runtime bool
		revoked          bool
	}{
		{"install scalar", `"install"`, true, false, false},
		{"runtime scalar", `"runtime"`, false, true, false},
		{"both", `["install","runtime"]`, true, true, false},
		{"revoked", `["install","runtime"]`, false, false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var phases spec.Phases
			if err := json.Unmarshal([]byte(tc.phase), &phases); err != nil {
				t.Fatal(err)
			}
			creds := []spec.CredentialCapability{
				{Service: "key", Phase: phases, APIKey: &spec.APIKey{Inject: []spec.Inject{{Domain: "api.example", Header: "X-Key"}}}},
				{Service: "oauth", Phase: phases, OAuth: &spec.OAuth{TokenEndpoint: &spec.TokenEndpoint{Host: "token.example", Path: "/token"}, ResourceHosts: []string{"api.example"}, Sentinels: &spec.Sentinels{AccessToken: "sentinel"}}},
			}
			proxy := NewProxy(nil, ProxyOptions{Resolver: ResolverFunc(func(context.Context, string, string) (string, error) {
				if tc.revoked {
					return "", ErrUnbound
				}
				return "real-key", nil
			}), OAuthStore: phaseOAuthStore{}})
			session := &Session{token: "run", org: "org", injections: compileInjections(creds, nil), oauth: compileOAuthRules(creds, nil)}
			for _, runtime := range []bool{false, true} {
				session.atRun.Store(runtime)
				want := tc.install
				if runtime {
					want = tc.runtime
				}
				req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "http://api.example/data", nil)
				req.Header.Set("Authorization", "Bearer sentinel")
				if err := proxy.inject(context.Background(), session, req, "api.example", 80); err != nil {
					t.Fatal(err)
				}
				if err := proxy.injectOAuth(context.Background(), session, req, "api.example", 80); err != nil {
					t.Fatal(err)
				}
				if got := req.Header.Get("X-Key") == "real-key"; got != want {
					t.Errorf("runtime=%v API key injected=%v, want %v", runtime, got, want)
				}
				if got := req.Header.Get("Authorization") == "Bearer real-oauth"; got != want {
					t.Errorf("runtime=%v OAuth injected=%v, want %v", runtime, got, want)
				}
			}
		})
	}
}
