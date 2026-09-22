package oidc

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
)

const testAudience = "archie-dashboard"

// provider is a minimal identity provider: a discovery document and a JWKS.
// It signs with a real RSA key so every refusal below is the library's own
// signature and claim checking, not a stub's behaviour.
type provider struct {
	server *httptest.Server
	key    *rsa.PrivateKey
	keyID  string
	// other is a second, unrelated key used to sign a token the provider
	// never published, which is what a forged or foreign token looks like.
	other *rsa.PrivateKey
	// pending holds the PKCE challenge each issued code must be redeemed with.
	pending sync.Map
}

func newProvider(t *testing.T) *provider {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	other, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate second key: %v", err)
	}
	p := &provider{key: key, other: other, keyID: "test-key"}
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer":                                p.server.URL,
			"jwks_uri":                              p.server.URL + "/keys",
			"authorization_endpoint":                p.server.URL + "/authorize",
			"token_endpoint":                        p.server.URL + "/token",
			"response_types_supported":              []string{"code"},
			"subject_types_supported":               []string{"public"},
			"id_token_signing_alg_values_supported": []string{"RS256"},
		})
	})
	mux.HandleFunc("/authorize", p.authorizeEndpoint(t))
	mux.HandleFunc("/token", p.tokenEndpoint(t))
	mux.HandleFunc("/keys", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{
			Key: key.Public(), KeyID: p.keyID, Algorithm: "RS256", Use: "sig",
		}}})
	})
	p.server = httptest.NewServer(mux)
	t.Cleanup(p.server.Close)
	return p
}

// testClaims is the claim set this provider emits. A machine token carries
// client_id and no sub; a person's token carries sub.
type testClaims struct {
	jwt.Claims
	ClientID string   `json:"client_id,omitempty"`
	Scopes   []string `json:"scp,omitempty"`
}

// token signs a token with the given key for the given claims.
func (p *provider) token(t *testing.T, signer *rsa.PrivateKey, mutate func(*testClaims)) string {
	t.Helper()
	claims := testClaims{
		Issuer:   p.server.URL,
		Subject:  "sam",
		Audience: jwt.Audience{testAudience},
		Expiry:   jwt.NewNumericDate(time.Now().Add(time.Hour)),
		IssuedAt: jwt.NewNumericDate(time.Now().Add(-time.Minute)),
	}
	if mutate != nil {
		mutate(&claims)
	}
	sig, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: signer},
		(&jose.SignerOptions{}).WithType("JWT").WithHeader("kid", p.keyID))
	if err != nil {
		t.Fatalf("new signer: %v", err)
	}
	raw, err := jwt.Signed(sig).Claims(claims).Serialize()
	if err != nil {
		t.Fatalf("serialize token: %v", err)
	}
	return raw
}

// personToken is what the token endpoint issues for a signed-in person: a
// subject, and the audience this dashboard checks.
func (p *provider) personToken(t *testing.T) string {
	t.Helper()
	return p.token(t, p.key, func(c *testClaims) {
		c.Subject = "a150ab4b-0000-0000-0000-000000000001"
		c.ClientID = ""
		c.Scopes = []string{"openid", "profile", "email"}
	})
}

func (p *provider) verifier(t *testing.T) *Verifier {
	t.Helper()
	v, err := New(context.Background(), Config{Issuer: p.server.URL, Audience: testAudience})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return v
}

func TestVerifyAcceptsAProviderIssuedToken(t *testing.T) {
	p := newProvider(t)
	credential, err := p.verifier(t).Verify(context.Background(), p.token(t, p.key, nil))
	if err != nil {
		t.Fatalf("Verify() error = %v", err)
	}
	if credential.Subject.Subject != "sam" {
		t.Fatalf("subject = %q, want %q", credential.Subject.Subject, "sam")
	}
	if credential.Subject.Issuer != p.server.URL {
		t.Fatalf("issuer = %q, want %q", credential.Subject.Issuer, p.server.URL)
	}
}

// TestVerifyDerivesTheSubjectTheWayTheProviderIssuesIt: a person is named by sub
// and a machine by client_id, because a machine token carries no sub at all.
// Getting this wrong does not fail loudly -- it binds every machine caller to one
// identity, or to none.
func TestVerifyDerivesTheSubjectTheWayTheProviderIssuesIt(t *testing.T) {
	p := newProvider(t)
	verifier := p.verifier(t)

	tests := []struct {
		name        string
		mutate      func(*testClaims)
		wantSubject string
		wantScopes  []string
	}{
		{
			name: "a person's token carries sub",
			mutate: func(c *testClaims) {
				c.Subject = "a150ab4b-0000-0000-0000-000000000001"
				c.Scopes = []string{"openid", "profile", "email", "groups"}
			},
			wantSubject: "a150ab4b-0000-0000-0000-000000000001",
			wantScopes:  []string{"openid", "profile", "email", "groups"},
		},
		{
			name: "a machine token carries client_id and no sub",
			mutate: func(c *testClaims) {
				c.Subject = ""
				c.ClientID = "archie"
				c.Scopes = []string{"authelia.bearer.authz"}
			},
			wantSubject: "archie",
			wantScopes:  []string{"authelia.bearer.authz"},
		},
		{
			name: "sub wins when a token carries both",
			mutate: func(c *testClaims) {
				c.Subject = "a150ab4b-0000-0000-0000-000000000002"
				c.ClientID = "archie-web"
			},
			wantSubject: "a150ab4b-0000-0000-0000-000000000002",
		},
		{
			name: "a space-delimited scope claim is read too",
			mutate: func(c *testClaims) {
				c.Subject = "a150ab4b-0000-0000-0000-000000000003"
				c.ClientID = ""
				c.Scopes = nil
			},
			wantSubject: "a150ab4b-0000-0000-0000-000000000003",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			credential, err := verifier.Verify(context.Background(), p.token(t, p.key, tc.mutate))
			if err != nil {
				t.Fatalf("Verify() error = %v", err)
			}
			if credential.Subject.Subject != tc.wantSubject {
				t.Fatalf("subject = %q, want %q", credential.Subject.Subject, tc.wantSubject)
			}
			if tc.wantScopes != nil && !slices.Equal(credential.Scopes, tc.wantScopes) {
				t.Fatalf("scopes = %v, want %v", credential.Scopes, tc.wantScopes)
			}
		})
	}
}

// TestVerifyRefusesATokenThatNamesNobody: a verified token asserting neither sub
// nor client_id is an anonymous caller, and accepting it would let any token the
// provider ever signed act as whoever the binding happened to hold.
func TestVerifyRefusesATokenThatNamesNobody(t *testing.T) {
	p := newProvider(t)
	token := p.token(t, p.key, func(c *testClaims) {
		c.Subject = ""
		c.ClientID = ""
	})
	if _, err := p.verifier(t).Verify(context.Background(), token); err == nil {
		t.Fatal("Verify() accepted a token that asserts no caller")
	}
}

func TestVerifyRefusesEveryUnacceptableToken(t *testing.T) {
	p := newProvider(t)
	verifier := p.verifier(t)

	tests := []struct {
		name  string
		token string
	}{
		{
			name:  "no token",
			token: "",
		},
		{
			name:  "expired",
			token: p.token(t, p.key, func(c *testClaims) { c.Expiry = jwt.NewNumericDate(time.Now().Add(-time.Hour)) }),
		},
		{
			name:  "signed by a key the provider never published",
			token: p.token(t, p.other, nil),
		},
		{
			name:  "issued for another audience",
			token: p.token(t, p.key, func(c *testClaims) { c.Audience = jwt.Audience{"another-service"} }),
		},
		{
			name:  "issued by another provider",
			token: p.token(t, p.key, func(c *testClaims) { c.Issuer = "https://someone-else.example" }),
		},
		{
			// The human path's failure mode: a token minted without the requested
			// audience arrives with an empty audience, and the check here refuses
			// it. The fix is requesting the audience at the authorisation
			// endpoint, not weakening this check.
			name:  "issued with no audience at all",
			token: p.token(t, p.key, func(c *testClaims) { c.Audience = nil }),
		},
		{
			name:  "not a token at all",
			token: "not-a-jwt",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := verifier.Verify(context.Background(), tc.token); err == nil {
				t.Fatal("Verify() accepted a token it must refuse")
			}
		})
	}
}

func TestNewRefusesAnIncompleteConfiguration(t *testing.T) {
	p := newProvider(t)
	tests := []struct {
		name string
		cfg  Config
	}{
		{"no issuer", Config{Audience: testAudience}},
		{"no audience", Config{Issuer: p.server.URL}},
		{"unreachable issuer", Config{Issuer: "http://127.0.0.1:1", Audience: testAudience}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := New(context.Background(), tc.cfg); err == nil {
				t.Fatal("New() accepted a configuration it must refuse")
			}
		})
	}
}
