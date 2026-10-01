package egress

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/docker/sandbox-kit-spec/v3/spec"

	"github.com/samcharles93/archie-core/internal/domain/harnesssecret"
	"github.com/samcharles93/archie-core/internal/domain/storecontract"
)

type fakeOAuthStore struct {
	mu      sync.Mutex
	secrets map[string]harnesssecret.Secret
}

func newFakeOAuthStore() *fakeOAuthStore {
	return &fakeOAuthStore{secrets: map[string]harnesssecret.Secret{}}
}

func (f *fakeOAuthStore) GetHarnessSecret(_ context.Context, org, service string) (harnesssecret.Secret, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s, ok := f.secrets[org+"/"+service]
	if !ok {
		return harnesssecret.Secret{}, storecontract.ErrHarnessSecretNotFound
	}
	return s, nil
}

func (f *fakeOAuthStore) PutHarnessSecret(_ context.Context, s harnesssecret.Secret) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.secrets[s.Org+"/"+s.Service] = s
	return nil
}

// tokenEndpoint is the fake provider token endpoint: it records the body the
// proxy sent upstream and answers with a configured response.
type tokenEndpoint struct {
	mu       sync.Mutex
	lastBody []byte
	hits     int
	response string
}

func (e *tokenEndpoint) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	e.mu.Lock()
	defer e.mu.Unlock()
	e.lastBody, e.hits = body, e.hits+1
	w.Header().Set("Content-Type", "application/json")
	_, _ = io.WriteString(w, e.response)
}

func (e *tokenEndpoint) seen() (string, int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return string(e.lastBody), e.hits
}

const (
	sentinelAccess  = "sentinel-access"
	sentinelRefresh = "sentinel-refresh"
)

func oauthCred(required bool) spec.CredentialCapability {
	return spec.CredentialCapability{
		Required: required,
		Service:  "claude-code", Phase: "runtime",
		OAuth: &spec.OAuth{
			TokenEndpoint: &spec.TokenEndpoint{Host: "oauth.example.com", Path: "/token"},
			ResourceHosts: []string{"api.example.com"},
			Sentinels:     &spec.Sentinels{AccessToken: sentinelAccess, RefreshToken: sentinelRefresh},
		},
	}
}

// oauthSession registers run-7 in org-1 with one OAuth credential, granted
// to the run when grant is set.
func (h *harness) oauthSession(t *testing.T, required, grant bool) *Session {
	t.Helper()
	if grant {
		h.bind("run-7/claude-code", "")
	}
	s, err := h.proxy.Register(SessionOptions{
		Run: "run-7", Org: "org-1",
		Network:     &spec.PhasedNetwork{Runtime: &spec.NetworkRules{Allow: []string{"oauth.example.com:443", "api.example.com:443"}}},
		Credentials: []spec.CredentialCapability{oauthCred(required)},
		Bound:       map[string]CredentialKind{"claude-code": CredentialOAuth},
	})
	if err != nil {
		t.Fatal(err)
	}
	s.EnterRuntime()
	return s
}

func postToken(t *testing.T, h *harness, s *Session, form string) (int, map[string]any) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, "https://oauth.example.com/token", strings.NewReader(form))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := h.client(s.Token()).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	var out map[string]any
	_ = json.Unmarshal(body, &out)
	return resp.StatusCode, out
}

func TestOAuthRefreshUsesTheStoredTokenAndReturnsOnlySentinels(t *testing.T) {
	tests := []struct {
		name        string
		response    string
		wantRefresh string
	}{
		{
			name:        "rotating provider",
			response:    `{"access_token":"new-access","refresh_token":"new-refresh","expires_in":3600,"token_type":"Bearer"}`,
			wantRefresh: "new-refresh",
		},
		{
			name:        "provider keeps the refresh token",
			response:    `{"access_token":"new-access","expires_in":3600}`,
			wantRefresh: "real-refresh",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t)
			h.token.response = tt.response
			_ = h.oauth.PutHarnessSecret(t.Context(), harnesssecret.Secret{Org: "org-1", Service: "claude-code", AccessToken: "old-access", RefreshToken: "real-refresh"})
			s := h.oauthSession(t, true, true)

			status, out := postToken(t, h, s, "grant_type=refresh_token&refresh_token="+sentinelRefresh)

			if status != http.StatusOK {
				t.Fatalf("status = %d, want 200", status)
			}
			if sent, _ := h.token.seen(); sent != "grant_type=refresh_token&refresh_token=real-refresh" {
				t.Fatalf("upstream body = %q, want the stored refresh token in place of the sentinel", sent)
			}
			if out["access_token"] != sentinelAccess {
				t.Fatalf("container got access_token %v, want the sentinel", out["access_token"])
			}
			if _, rotated := out["refresh_token"]; rotated && out["refresh_token"] != sentinelRefresh {
				t.Fatalf("container got refresh_token %v, want the sentinel", out["refresh_token"])
			}
			stored, _ := h.oauth.GetHarnessSecret(t.Context(), "org-1", "claude-code")
			if stored.AccessToken != "new-access" || stored.RefreshToken != tt.wantRefresh || stored.ExpiresAt.IsZero() {
				t.Fatalf("stored = %+v, want access new-access, refresh %s and an expiry", stored, tt.wantRefresh)
			}
		})
	}
}

func TestOAuthLoginExchangeCapturesWithNoStoredToken(t *testing.T) {
	h := newHarness(t)
	h.token.response = `{"access_token":"first-access","refresh_token":"first-refresh"}`
	s := h.oauthSession(t, true, true)

	status, out := postToken(t, h, s, "grant_type=authorization_code&code=abc")

	if status != http.StatusOK || out["refresh_token"] != sentinelRefresh {
		t.Fatalf("status %d, body %v; want 200 with sentinels", status, out)
	}
	stored, err := h.oauth.GetHarnessSecret(t.Context(), "org-1", "claude-code")
	if err != nil || stored.RefreshToken != "first-refresh" {
		t.Fatalf("stored = %+v, %v; want the captured login", stored, err)
	}
}

func TestOAuthRefreshWithNoStoredTokenNeverReachesUpstream(t *testing.T) {
	h := newHarness(t)
	s := h.oauthSession(t, true, true)

	status, _ := postToken(t, h, s, "grant_type=refresh_token&refresh_token="+sentinelRefresh)

	if status != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", status)
	}
	if _, hits := h.token.seen(); hits != 0 {
		t.Fatalf("upstream hit %d times, want 0", hits)
	}
}

// An org's stored token is read and written only for a run whose credential
// carries the service; declaring it in the Kit is not enough.
func TestOAuthUngrantedRunNeitherReadsNorWritesTheOrgToken(t *testing.T) {
	tests := []struct {
		name       string
		required   bool
		wantStatus int
		wantHits   int
	}{
		{name: "required", required: true, wantStatus: http.StatusBadGateway, wantHits: 0},
		{name: "optional passes through untouched", required: false, wantStatus: http.StatusOK, wantHits: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t)
			h.token.response = `{"access_token":"attacker-access","refresh_token":"attacker-refresh"}`
			seed := harnesssecret.Secret{Org: "org-1", Service: "claude-code", AccessToken: "real-access", RefreshToken: "real-refresh"}
			_ = h.oauth.PutHarnessSecret(t.Context(), seed)
			s := h.oauthSession(t, tt.required, false)

			status, _ := postToken(t, h, s, "grant_type=refresh_token&refresh_token="+sentinelRefresh)

			sent, hits := h.token.seen()
			if status != tt.wantStatus || hits != tt.wantHits {
				t.Fatalf("status %d with %d upstream hits, want %d with %d", status, hits, tt.wantStatus, tt.wantHits)
			}
			if strings.Contains(sent, "real-refresh") {
				t.Fatalf("upstream saw the org's refresh token: %q", sent)
			}
			stored, _ := h.oauth.GetHarnessSecret(t.Context(), "org-1", "claude-code")
			if stored.Org != seed.Org || stored.Service != seed.Service ||
				stored.AccessToken != seed.AccessToken || stored.RefreshToken != seed.RefreshToken ||
				stored.TokenType != seed.TokenType || !stored.ExpiresAt.Equal(seed.ExpiresAt) ||
				!slices.Equal(stored.Scopes, seed.Scopes) {
				t.Fatalf("stored = %+v, want it unchanged", stored)
			}
		})
	}
}

func TestOAuthResourceRequestCarriesTheStoredAccessToken(t *testing.T) {
	h := newHarness(t)
	_ = h.oauth.PutHarnessSecret(t.Context(), harnesssecret.Secret{Org: "org-1", Service: "claude-code", AccessToken: "real-access"})
	s := h.oauthSession(t, true, true)

	req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, "https://api.example.com/v1/messages", nil)
	req.Header.Set("Authorization", "Bearer "+sentinelAccess)
	resp, err := h.client(s.Token()).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()

	if got := h.lastAuth.Load(); got != "Bearer real-access" {
		t.Fatalf("upstream Authorization = %v, want the stored access token", got)
	}
}

// dualCred declares apiKey and oauth on one service, the spec's shape for
// "whichever the host has bound" (docker/claude-code-kit does, for
// anthropic).
func dualCred(required bool) spec.CredentialCapability {
	c := oauthCred(required)
	c.APIKey = &spec.APIKey{Name: "EXAMPLE_KEY", ProxyManaged: true, Inject: []spec.Inject{
		{Domain: "api.example.com", Header: "Authorization", Format: "Bearer %s"},
	}}
	return c
}

// TestAnAPIKeyBoundServiceDoesNotInterceptTheTokenEndpoint is the mirror of
// TestAnOAuthBoundServiceInjectsNoAPIKey: when the org's binding names a
// secret for a service whose Kit also declares OAuth, this run has no token
// set to exchange, so its token endpoint is an ordinary allowed host.
// Intercepting it could only fail on the missing token set, or capture a
// login the org never bound.
func TestAnAPIKeyBoundServiceDoesNotInterceptTheTokenEndpoint(t *testing.T) {
	h := newHarness(t)
	h.bind("run-7/claude-code", "sk-real-key")
	s, err := h.proxy.Register(SessionOptions{
		Run: "run-7", Org: "org-1",
		Network:     &spec.PhasedNetwork{Runtime: &spec.NetworkRules{Allow: []string{"oauth.example.com:443"}}},
		Credentials: []spec.CredentialCapability{dualCred(false)},
		Bound:       map[string]CredentialKind{"claude-code": CredentialAPIKey},
	})
	if err != nil {
		t.Fatal(err)
	}
	s.EnterRuntime()
	status, _ := postToken(t, h, s, "grant_type=refresh_token&refresh_token="+sentinelRefresh)
	if status != http.StatusOK {
		t.Fatalf("token endpoint status %d, want 200: an API-key-bound run has no token set to intercept", status)
	}
}

// TestRegisterAllowsARequiredAPIKeyBoundCredentialWithNoStore: a Kit may
// declare apiKey and oauth on one service as required (the spec's
// "whichever the host has bound"). An org that bound the API key has no use
// for a token store, so the launch must not be refused for the missing one.
func TestRegisterAllowsARequiredAPIKeyBoundCredentialWithNoStore(t *testing.T) {
	ca, err := LoadOrCreateCA(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	p := NewProxy(ca, ProxyOptions{})
	if _, err := p.Register(SessionOptions{
		Run: "run-7", Org: "org-1",
		Credentials: []spec.CredentialCapability{dualCred(true)},
		Bound:       map[string]CredentialKind{"claude-code": CredentialAPIKey},
	}); err != nil {
		t.Fatalf("Register() = %v, want an API-key-bound run to need no token store", err)
	}
}

func TestRegisterRefusesARequiredOAuthCredentialWithNoStore(t *testing.T) {
	ca, err := LoadOrCreateCA(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	p := NewProxy(ca, ProxyOptions{})
	if _, err := p.Register(SessionOptions{Run: "run-7", Org: "org-1", Credentials: []spec.CredentialCapability{oauthCred(true)}, Bound: map[string]CredentialKind{"claude-code": CredentialOAuth}}); err == nil {
		t.Fatal("Register() succeeded with a required OAuth credential and no store")
	}
}

func newTokenEndpoint(t *testing.T) (*tokenEndpoint, *httptest.Server) {
	t.Helper()
	e := &tokenEndpoint{}
	srv := httptest.NewTLSServer(e)
	t.Cleanup(srv.Close)
	return e, srv
}
