package egress

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/docker/sandbox-kit-spec/v3/spec"
)

// harness wires a proxy to local upstreams: the proxy's dialer maps the
// public names a policy uses onto httptest listeners, so the tests exercise
// the real CONNECT, TLS termination and forwarding paths hermetically.
type harness struct {
	proxy    *Proxy
	server   *httptest.Server
	upstream *httptest.Server
	plain    *httptest.Server
	hits     atomic.Int32
	lastAuth atomic.Value
	caPool   *x509.CertPool
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	h := &harness{}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.hits.Add(1)
		h.lastAuth.Store(r.Header.Get("Authorization"))
		_, _ = io.WriteString(w, "upstream:"+r.Host+r.URL.Path)
	})
	h.upstream = httptest.NewTLSServer(handler)
	h.plain = httptest.NewServer(handler)
	t.Cleanup(h.upstream.Close)
	t.Cleanup(h.plain.Close)

	ca, err := LoadOrCreateCA(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	upstreamRoots := x509.NewCertPool()
	upstreamRoots.AddCert(h.upstream.Certificate())
	routes := map[string]string{
		"api.example.com:443":     h.upstream.Listener.Addr().String(),
		"install.example.com:443": h.upstream.Listener.Addr().String(),
		"denied.example.com:443":  h.upstream.Listener.Addr().String(),
		"plain.example.com:80":    h.plain.Listener.Addr().String(),
	}
	h.proxy = NewProxy(ca, ProxyOptions{
		UpstreamRoots: upstreamRoots,
		Dial: func(ctx context.Context, network, addr string) (net.Conn, error) {
			target, ok := routes[addr]
			if !ok {
				t.Errorf("proxy dialled unexpected upstream %s", addr)
				return nil, &net.OpError{Op: "dial", Net: network, Err: net.UnknownNetworkError("unrouted")}
			}
			return (&net.Dialer{}).DialContext(ctx, network, target)
		},
	})
	h.server = httptest.NewServer(h.proxy)
	t.Cleanup(h.server.Close)
	h.caPool = x509.NewCertPool()
	h.caPool.AppendCertsFromPEM(ca.CertPEM())
	return h
}

func (h *harness) register(t *testing.T) *Session {
	t.Helper()
	s, err := h.proxy.Register(&spec.PhasedNetwork{
		Install: &spec.NetworkRules{Allow: []string{"install.example.com"}},
		Runtime: &spec.NetworkRules{Allow: []string{"api.example.com:443", "plain.example.com:80"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func (h *harness) client(token string) *http.Client {
	proxyURL, _ := url.Parse(h.server.URL)
	if token != "" {
		proxyURL.User = url.UserPassword(proxyUser, token)
	}
	return &http.Client{Transport: &http.Transport{
		Proxy:           http.ProxyURL(proxyURL),
		TLSClientConfig: &tls.Config{RootCAs: h.caPool},
	}}
}

func get(t *testing.T, c *http.Client, target string) (int, string, error) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, target, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := c.Do(req)
	if err != nil {
		return 0, "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(body), nil
}

func TestProxyForwardsAllowedHTTPS(t *testing.T) {
	h := newHarness(t)
	s := h.register(t)
	s.EnterRuntime()
	status, body, err := get(t, h.client(s.Token()), "https://api.example.com/v1/messages")
	if err != nil {
		t.Fatalf("request through proxy: %v", err)
	}
	if status != http.StatusOK || body != "upstream:api.example.com/v1/messages" {
		t.Fatalf("got %d %q", status, body)
	}
}

func TestProxyRefusesWithoutAValidToken(t *testing.T) {
	h := newHarness(t)
	s := h.register(t)
	s.EnterRuntime()
	revoked := h.register(t)
	revoked.EnterRuntime()
	h.proxy.Revoke(revoked.Token())
	for name, token := range map[string]string{"no token": "", "unknown token": "not-a-token", "revoked token": revoked.Token()} {
		t.Run(name, func(t *testing.T) {
			if _, _, err := get(t, h.client(token), "https://api.example.com/"); err == nil {
				t.Fatal("request succeeded without a valid session")
			}
			status, _, err := get(t, h.client(token), "http://plain.example.com/")
			if err != nil {
				t.Fatal(err)
			}
			if status != http.StatusProxyAuthRequired {
				t.Fatalf("plain request got %d, want 407", status)
			}
		})
	}
	if h.hits.Load() != 0 {
		t.Fatalf("upstream saw %d requests from unauthenticated clients", h.hits.Load())
	}
}

func TestProxyEnforcesThePhasePolicy(t *testing.T) {
	h := newHarness(t)
	s := h.register(t)
	c := h.client(s.Token())

	if status, _, err := get(t, c, "https://install.example.com/"); err != nil || status != http.StatusOK {
		t.Fatalf("install-phase host during install: %d %v", status, err)
	}
	if _, _, err := get(t, c, "https://api.example.com/"); err == nil {
		t.Fatal("runtime-only host reachable during install")
	}
	s.EnterRuntime()
	if _, _, err := get(t, h.client(s.Token()), "https://install.example.com/"); err == nil {
		t.Fatal("install-phase host still reachable at runtime")
	}
	if _, _, err := get(t, h.client(s.Token()), "https://denied.example.com/"); err == nil {
		t.Fatal("host outside every allow list reachable")
	}
}

func TestProxyForwardsAllowedPlainHTTP(t *testing.T) {
	h := newHarness(t)
	s := h.register(t)
	s.EnterRuntime()
	status, body, err := get(t, h.client(s.Token()), "http://plain.example.com/x")
	if err != nil || status != http.StatusOK || body != "upstream:plain.example.com/x" {
		t.Fatalf("got %d %q %v", status, body, err)
	}
	status, _, err = get(t, h.client(s.Token()), "http://denied.example.com/")
	if err != nil || status != http.StatusForbidden {
		t.Fatalf("denied plain request got %d %v, want 403", status, err)
	}
}

func TestProxyRefusesAHostHeaderThatLeavesTheTunnel(t *testing.T) {
	h := newHarness(t)
	s := h.register(t)
	s.EnterRuntime()
	c := h.client(s.Token())
	req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, "https://api.example.com/", nil)
	req.Host = "denied.example.com"
	resp, err := c.Do(req)
	if err == nil {
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusMisdirectedRequest {
			t.Fatalf("smuggled Host got %d, want 421", resp.StatusCode)
		}
	}
	if h.hits.Load() != 0 {
		t.Fatal("a request whose Host differs from its tunnel reached upstream")
	}
}

func TestProxyRefusesMismatchedSNI(t *testing.T) {
	h := newHarness(t)
	s := h.register(t)
	s.EnterRuntime()
	proxyURL, _ := url.Parse(h.server.URL)
	proxyURL.User = url.UserPassword(proxyUser, s.Token())
	tr := &http.Transport{
		Proxy:           http.ProxyURL(proxyURL),
		TLSClientConfig: &tls.Config{RootCAs: h.caPool, ServerName: "denied.example.com"},
	}
	req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, "https://api.example.com/", nil)
	resp, err := (&http.Client{Transport: tr}).Do(req)
	if err == nil {
		_ = resp.Body.Close()
		t.Fatal("a TLS handshake naming another host completed inside the tunnel")
	}
	if h.hits.Load() != 0 {
		t.Fatal("mismatched SNI reached upstream")
	}
}

func TestProxyAppliesTheRewriteHook(t *testing.T) {
	h := newHarness(t)
	var seen atomic.Pointer[Session]
	h.proxy.rewrite = func(s *Session, r *http.Request) error {
		seen.Store(s)
		r.Header.Set("Authorization", "Bearer injected")
		return nil
	}
	s := h.register(t)
	s.EnterRuntime()
	if _, _, err := get(t, h.client(s.Token()), "https://api.example.com/"); err != nil {
		t.Fatal(err)
	}
	if got := h.lastAuth.Load(); got != "Bearer injected" {
		t.Fatalf("upstream Authorization %v, want the rewritten value", got)
	}
	if seen.Load() != s {
		t.Fatal("rewrite hook did not receive the request's session")
	}
}

func TestUpstreamGuard(t *testing.T) {
	tests := []struct {
		ip      string
		literal bool
		want    bool
	}{
		{"93.184.216.34", false, true},
		{"127.0.0.1", false, false},
		{"127.0.0.1", true, false},
		{"::1", false, false},
		{"0.0.0.0", true, false},
		{"10.1.2.3", false, false},
		{"10.1.2.3", true, true},
		{"172.17.0.1", false, false},
		{"192.168.1.10", false, false},
		{"169.254.169.254", false, false},
		{"fd00::1", false, false},
		{"fe80::1", false, false},
	}
	for _, tt := range tests {
		err := guardUpstream(net.ParseIP(tt.ip), tt.literal)
		if got := err == nil; got != tt.want {
			t.Errorf("guardUpstream(%s, literal=%v) allowed=%v, want %v (%v)", tt.ip, tt.literal, got, tt.want, err)
		}
	}
	if err := guardUpstream(net.ParseIP("127.0.0.1"), false); err == nil || !strings.Contains(err.Error(), "127.0.0.1") {
		t.Fatalf("guard error %v does not name the refused address", err)
	}
}
