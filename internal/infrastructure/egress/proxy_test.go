package egress

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/samcharles93/archie-core/internal/domain/harnesssecret"
)

type stubOAuthStore struct{}

func (stubOAuthStore) GetHarnessSecret(context.Context, string, string) (harnesssecret.Secret, error) {
	return harnesssecret.Secret{}, nil
}

func (stubOAuthStore) PutHarnessSecret(context.Context, harnesssecret.Secret) error { return nil }

// A matched token endpoint must never be reverse-proxied when the run is not
// granted the service. Doing so would return the provider's real access and
// refresh tokens to the container un-sentinelized, so the proxy refuses.
func TestTokenEndpointRefusedWhenNotGranted(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		required bool
		status   int
	}{
		{name: "an optional ungranted credential is refused", status: http.StatusForbidden},
		{name: "a required ungranted credential is refused", required: true, status: http.StatusBadGateway},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var proxied atomic.Bool
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				proxied.Store(true)
				_, _ = w.Write([]byte(`{"access_token":"real-access","refresh_token":"real-refresh"}`))
			}))
			defer upstream.Close()

			proxy := NewProxy(nil, ProxyOptions{
				Resolver:   ResolverFunc(func(context.Context, string, string) (string, error) { return "", ErrUnbound }),
				OAuthStore: stubOAuthStore{},
				Dial: func(context.Context, string, string) (net.Conn, error) {
					return net.Dial("tcp", upstream.Listener.Addr().String())
				},
			})
			session := &Session{
				token: "run-token",
				org:   "org-sys",
				oauth: []oauthRule{{
					service: "linear", required: tc.required, runtime: true,
					tokenHost: compilePattern("token.example"), tokenPath: "/token",
				}},
			}
			session.atRun.Store(true)

			req := httptest.NewRequest(http.MethodPost, "http://token.example/token", strings.NewReader("grant_type=authorization_code"))
			rec := httptest.NewRecorder()
			proxy.forward(context.Background(), rec, req, session, "http", "token.example", 80)

			if rec.Code != tc.status {
				t.Fatalf("status = %d, want %d (body %q)", rec.Code, tc.status, rec.Body.String())
			}
			if proxied.Load() {
				t.Error("the token endpoint was proxied; the provider's real tokens reached the container")
			}
			if strings.Contains(rec.Body.String(), "real") {
				t.Fatalf("the response carried the provider's real tokens: %q", rec.Body.String())
			}
		})
	}
}
