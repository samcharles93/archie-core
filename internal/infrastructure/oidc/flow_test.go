package oidc

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"testing"

	"golang.org/x/oauth2"
)

// TestAuthCodeURLRequestsTheAudienceAtTheAuthorizationEndpoint pins the trap infra
// measured: a provider that is asked for the audience only at the token endpoint
// returns a token with an empty audience, and the audience check then refuses a
// perfectly valid token. The fix is this query parameter, so a test fails if it
// ever goes missing.
func TestAuthCodeURLRequestsTheAudienceAtTheAuthorizationEndpoint(t *testing.T) {
	p := newProvider(t)
	flow, err := NewFlow(context.Background(), Config{Issuer: p.server.URL, Audience: testAudience},
		"archie-web", "secret", p.server.URL+"/oauth2/callback")
	if err != nil {
		t.Fatalf("NewFlow() error = %v", err)
	}

	raw, verifier := flow.AuthCodeURL("state-1")
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("AuthCodeURL() returned an unparseable URL: %v", err)
	}
	query := parsed.Query()
	if got := query.Get("audience"); got != testAudience {
		t.Fatalf("audience = %q, want %q", got, testAudience)
	}
	if query.Get("code_challenge") == "" || query.Get("code_challenge_method") != "S256" {
		t.Fatalf("PKCE is not on the authorization request: %v", query)
	}
	if verifier == "" {
		t.Fatal("AuthCodeURL() returned no PKCE verifier for the callback")
	}
}

func TestNewFlowRefusesAnIncompleteConfiguration(t *testing.T) {
	p := newProvider(t)
	complete := Config{Issuer: p.server.URL, Audience: testAudience}
	tests := []struct {
		name        string
		cfg         Config
		clientID    string
		redirectURL string
	}{
		{"no client id", complete, "", p.server.URL + "/oauth2/callback"},
		{"no redirect URL", complete, "archie-web", ""},
		{"no audience", Config{Issuer: p.server.URL}, "archie-web", p.server.URL + "/oauth2/callback"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := NewFlow(context.Background(), tc.cfg, tc.clientID, "secret", tc.redirectURL); err == nil {
				t.Fatal("NewFlow() accepted a configuration it must refuse")
			}
		})
	}
}

// TestExchangeCompletesARealAuthorizationCodeFlow drives the flow against the test
// provider's own authorize and token endpoints, so the code exchange and PKCE are
// exercised rather than stubbed.
func TestExchangeCompletesARealAuthorizationCodeFlow(t *testing.T) {
	p := newProvider(t)
	redirect := "http://dashboard.example/oauth2/callback"
	flow, err := NewFlow(context.Background(), Config{Issuer: p.server.URL, Audience: testAudience},
		"archie-web", "secret", redirect)
	if err != nil {
		t.Fatalf("NewFlow() error = %v", err)
	}

	authURL, verifier := flow.AuthCodeURL("state-2")
	code := p.authorize(t, authURL, "state-2")

	session, err := flow.Exchange(context.Background(), code, verifier)
	if err != nil {
		t.Fatalf("Exchange() error = %v", err)
	}
	if session.Credential.Subject.Subject != "a150ab4b-0000-0000-0000-000000000001" {
		t.Fatalf("subject = %q, want the person's provider subject", session.Credential.Subject.Subject)
	}
	if session.Credential.Subject.Issuer != p.server.URL {
		t.Fatalf("issuer = %q, want %q", session.Credential.Subject.Issuer, p.server.URL)
	}
	if session.Token == "" {
		t.Fatal("Exchange() returned no token to present on later requests")
	}
}

// TestExchangeRefusesAWrongPKCEVerifier: the provider rejects the exchange, and
// archie must surface that as a refusal rather than a session.
func TestExchangeRefusesAWrongPKCEVerifier(t *testing.T) {
	p := newProvider(t)
	flow, err := NewFlow(context.Background(), Config{Issuer: p.server.URL, Audience: testAudience},
		"archie-web", "secret", "http://dashboard.example/oauth2/callback")
	if err != nil {
		t.Fatalf("NewFlow() error = %v", err)
	}
	authURL, _ := flow.AuthCodeURL("state-3")
	code := p.authorize(t, authURL, "state-3")

	if _, err := flow.Exchange(context.Background(), code, "not-the-verifier"); err == nil {
		t.Fatal("Exchange() accepted a wrong PKCE verifier")
	}
}

// authorize walks a browser's side of the flow: it asks the provider's
// authorization endpoint and returns the code the provider sends back.
func (p *provider) authorize(t *testing.T, authURL, wantState string) string {
	t.Helper()
	request, err := http.NewRequestWithContext(context.Background(), http.MethodGet, authURL, nil)
	if err != nil {
		t.Fatalf("build authorize request: %v", err)
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("authorize: %v", err)
	}
	defer func() { _ = response.Body.Close() }()
	location := response.Header.Get("Location")
	if location == "" {
		t.Fatalf("authorize returned %d with no redirect", response.StatusCode)
	}
	parsed, err := url.Parse(location)
	if err != nil {
		t.Fatalf("authorize redirect is unparseable: %v", err)
	}
	if got := parsed.Query().Get("state"); got != wantState {
		t.Fatalf("state = %q, want %q", got, wantState)
	}
	return parsed.Query().Get("code")
}

// authorizeEndpoint is a faithful authorization endpoint: it requires the
// audience and the PKCE challenge, and it only issues a code for a verifier it
// later receives.
func (p *provider) authorizeEndpoint(t *testing.T) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query()
		if query.Get("audience") == "" {
			http.Error(w, "audience is required at the authorization endpoint", http.StatusBadRequest)
			return
		}
		challenge := query.Get("code_challenge")
		if challenge == "" || query.Get("code_challenge_method") != "S256" {
			http.Error(w, "PKCE is required", http.StatusBadRequest)
			return
		}
		code := "code-" + query.Get("state")
		p.pending.Store(code, challenge)
		location := query.Get("redirect_uri") + "?code=" + url.QueryEscape(code) + "&state=" + url.QueryEscape(query.Get("state"))
		http.Redirect(w, r, location, http.StatusSeeOther)
	}
}

// tokenEndpoint trades a code for a signed access token, checking the PKCE
// verifier against the challenge the authorization endpoint recorded.
func (p *provider) tokenEndpoint(t *testing.T) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			http.Error(w, "bad form", http.StatusBadRequest)
			return
		}
		code := r.PostForm.Get("code")
		challenge, ok := p.pending.LoadAndDelete(code)
		if !ok {
			http.Error(w, "unknown code", http.StatusBadRequest)
			return
		}
		want, ok := challenge.(string)
		if !ok || pkceChallenge(r.PostForm.Get("code_verifier")) != want {
			http.Error(w, "PKCE verification failed", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": p.personToken(t),
			"token_type":   "Bearer",
			"expires_in":   3600,
			"scope":        "openid profile email",
		})
	}
}

// pkceChallenge computes the challenge the way the client library does, so the
// provider's check is the library's own transformation rather than archie's.
func pkceChallenge(verifier string) string {
	return oauth2.S256ChallengeFromVerifier(verifier)
}
