package oidc

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/samcharles93/archie-core/internal/domain/identity"
)

// TestProviderOutageIsUnavailableThenRecovers pins that a provider down at
// startup neither stops the dashboard nor refuses credentials as invalid, and
// that sign-in works once it returns.
func TestProviderOutageIsUnavailableThenRecovers(t *testing.T) {
	var up atomic.Bool
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !up.Load() {
			http.Error(w, "down", http.StatusServiceUnavailable)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{
			"issuer": server.URL, "authorization_endpoint": server.URL + "/auth",
			"token_endpoint": server.URL + "/token", "jwks_uri": server.URL + "/keys",
		})
	}))
	defer server.Close()

	provider, err := NewProvider(Config{Issuer: server.URL, Audience: "archie"})
	if err != nil {
		t.Fatalf("a down provider must not fail construction: %v", err)
	}
	verifier := NewVerifier(provider)
	ctx := context.Background()

	tests := []struct {
		name            string
		up              bool
		wantUnavailable bool
	}{
		{"down is unavailable, not rejected", false, true},
		{"back up is a normal verification", true, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			up.Store(tt.up)
			_, err := verifier.Verify(ctx, "not-a-jwt")
			if err == nil || errors.Is(err, identity.ErrProviderUnavailable) != tt.wantUnavailable {
				t.Fatalf("verify err %v, want unavailable %v", err, tt.wantUnavailable)
			}
			if checkErr := provider.Check(ctx); (checkErr != nil) != tt.wantUnavailable {
				t.Fatalf("check err %v, want failure %v", checkErr, tt.wantUnavailable)
			}
		})
	}
}
