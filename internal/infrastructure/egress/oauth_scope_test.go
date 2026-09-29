package egress

import (
	"net/http"
	"slices"
	"testing"

	"github.com/samcharles93/archie-core/internal/domain/harnesssecret"
)

// The provider's granted scopes are captured with the token set (RFC 6749
// section 5.1 spells them as one space-delimited string), so a Kit's
// credential file can render them.
func TestOAuthCaptureRecordsScopes(t *testing.T) {
	h := newHarness(t)
	h.token.response = `{"access_token":"new-access","refresh_token":"new-refresh","expires_in":3600,"scope":"user:inference user:profile"}`
	_ = h.oauth.PutHarnessSecret(t.Context(), harnesssecret.Secret{
		Org: "org-1", Service: "claude-code",
		AccessToken: "old-access", RefreshToken: "real-refresh",
		Scopes: []string{"user:inference"},
	})
	s := h.oauthSession(t, true, true)

	status, _ := postToken(t, h, s, "grant_type=refresh_token&refresh_token="+sentinelRefresh)

	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200", status)
	}
	stored, err := h.oauth.GetHarnessSecret(t.Context(), "org-1", "claude-code")
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"user:inference", "user:profile"}; !slices.Equal(stored.Scopes, want) {
		t.Fatalf("stored scopes = %v, want the granted set %v", stored.Scopes, want)
	}
}

// A refresh that does not repeat the scope keeps the set the login captured:
// a provider may return scopes only on the initial exchange.
func TestOAuthCaptureKeepsScopesWhenTheResponseOmitsThem(t *testing.T) {
	h := newHarness(t)
	h.token.response = `{"access_token":"new-access","refresh_token":"new-refresh","expires_in":3600}`
	want := []string{"user:inference", "user:profile"}
	_ = h.oauth.PutHarnessSecret(t.Context(), harnesssecret.Secret{
		Org: "org-1", Service: "claude-code",
		AccessToken: "old-access", RefreshToken: "real-refresh",
		Scopes: want,
	})
	s := h.oauthSession(t, true, true)

	status, _ := postToken(t, h, s, "grant_type=refresh_token&refresh_token="+sentinelRefresh)

	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200", status)
	}
	stored, err := h.oauth.GetHarnessSecret(t.Context(), "org-1", "claude-code")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(stored.Scopes, want) {
		t.Fatalf("stored scopes = %v, want the set the login captured %v", stored.Scopes, want)
	}
}
