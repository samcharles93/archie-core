package egress

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"slices"
	"testing"

	"github.com/docker/sandbox-kit-spec/v3/spec"
)

func apiKey(service, phase string, required bool, name string, inject ...spec.Inject) spec.CredentialCapability {
	return spec.CredentialCapability{
		Service: service, Phase: phase, APIKey: &spec.APIKey{Name: name, ProxyManaged: true, Inject: inject},
		Required: required,
	}
}

func (h *harness) registerWith(t *testing.T, creds ...spec.CredentialCapability) *Session {
	t.Helper()
	s, err := h.proxy.Register(SessionOptions{
		Run: "run-7",
		Network: &spec.PhasedNetwork{
			Install: &spec.NetworkRules{Allow: []string{"install.example.com"}},
			Runtime: &spec.NetworkRules{Allow: []string{"api.example.com:443", "plain.example.com:80"}},
		},
		Credentials: creds,
	})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestInjectsAnAPIKeyHeader(t *testing.T) {
	h := newHarness(t)
	h.bind("run-7/example", "real-secret")
	s := h.registerWith(t, apiKey("example", "runtime", true, "EXAMPLE_KEY",
		spec.Inject{Domain: "api.example.com", Header: "Authorization", Format: "Bearer %s"}))
	s.EnterRuntime()
	req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, "https://api.example.com/", nil)
	req.Header.Set("Authorization", "Bearer "+Sentinel)
	resp, err := h.client(s.Token()).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if got := h.lastAuth.Load(); got != "Bearer real-secret" {
		t.Fatalf("upstream Authorization %v, want the real key in place of the sentinel", got)
	}
}

func TestInjectsBasicAuth(t *testing.T) {
	h := newHarness(t)
	h.bind("run-7/example", "real-secret")
	s := h.registerWith(t, apiKey("example", "runtime", true, "",
		spec.Inject{Domain: "api.example.com", Scheme: "basic", Username: "x-access-token"}))
	s.EnterRuntime()
	if _, _, err := get(t, h.client(s.Token()), "https://api.example.com/"); err != nil {
		t.Fatal(err)
	}
	want := "Basic " + base64.StdEncoding.EncodeToString([]byte("x-access-token:real-secret"))
	if got := h.lastAuth.Load(); got != want {
		t.Fatalf("upstream Authorization %v, want %q", got, want)
	}
}

func TestInjectionIsScopedToItsDomainAndPhase(t *testing.T) {
	h := newHarness(t)
	h.bind("run-7/example", "real-secret")
	h.bind("run-7/installer", "install-secret")
	s := h.registerWith(t,
		apiKey("example", "runtime", true, "EXAMPLE_KEY", spec.Inject{Domain: "api.example.com", Header: "Authorization", Format: "Bearer %s"}),
		apiKey("installer", "install", true, "", spec.Inject{Domain: "plain.example.com", Header: "X-Install", Format: "%s"}),
	)
	s.EnterRuntime()
	if _, _, err := get(t, h.client(s.Token()), "http://plain.example.com/"); err != nil {
		t.Fatal(err)
	}
	if got := h.lastAuth.Load(); got != "" {
		t.Fatalf("a key for api.example.com was sent to plain.example.com: %v", got)
	}
	if h.lastInstall.Load() != "" {
		t.Fatal("an install-phase credential was injected at runtime")
	}
}

func TestAnUnboundCredential(t *testing.T) {
	t.Run("required stops the request before it reaches upstream", func(t *testing.T) {
		h := newHarness(t)
		s := h.registerWith(t, apiKey("example", "runtime", true, "EXAMPLE_KEY",
			spec.Inject{Domain: "api.example.com", Header: "Authorization", Format: "Bearer %s"}))
		s.EnterRuntime()
		status, _, err := get(t, h.client(s.Token()), "https://api.example.com/")
		if err != nil {
			t.Fatal(err)
		}
		if status != http.StatusBadGateway || h.hits.Load() != 0 {
			t.Fatalf("status %d with %d upstream hits; want 502 and none", status, h.hits.Load())
		}
	})
	t.Run("optional passes without one", func(t *testing.T) {
		h := newHarness(t)
		s := h.registerWith(t, apiKey("example", "runtime", false, "EXAMPLE_KEY",
			spec.Inject{Domain: "api.example.com", Header: "Authorization", Format: "Bearer %s"}))
		s.EnterRuntime()
		status, _, err := get(t, h.client(s.Token()), "https://api.example.com/")
		if err != nil || status != http.StatusOK {
			t.Fatalf("got %d %v", status, err)
		}
		if got := h.lastAuth.Load(); got != "" {
			t.Fatalf("upstream saw Authorization %v with no binding", got)
		}
	})
	t.Run("a resolver failure is never treated as unbound", func(t *testing.T) {
		h := newHarness(t)
		h.failResolves(errors.New("state store unreachable"))
		s := h.registerWith(t, apiKey("example", "runtime", false, "EXAMPLE_KEY",
			spec.Inject{Domain: "api.example.com", Header: "Authorization", Format: "Bearer %s"}))
		s.EnterRuntime()
		status, _, err := get(t, h.client(s.Token()), "https://api.example.com/")
		if err != nil {
			t.Fatal(err)
		}
		if status != http.StatusBadGateway || h.hits.Load() != 0 {
			t.Fatalf("status %d with %d upstream hits; want 502 and none", status, h.hits.Load())
		}
	})
}

func TestTheResolverSeesTheRunAndService(t *testing.T) {
	h := newHarness(t)
	h.bind("run-7/example", "real-secret")
	s := h.registerWith(t, apiKey("example", "runtime", true, "EXAMPLE_KEY",
		spec.Inject{Domain: "api.example.com", Header: "Authorization", Format: "Bearer %s"}))
	s.EnterRuntime()
	if _, _, err := get(t, h.client(s.Token()), "https://api.example.com/"); err != nil {
		t.Fatal(err)
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if !slices.Contains(h.resolved, "run-7/example") {
		t.Fatalf("resolver calls %v, want run-7/example", h.resolved)
	}
}

func TestSentinelEnv(t *testing.T) {
	env := SentinelEnv([]spec.CredentialCapability{
		apiKey("named", "runtime", true, "NAMED_KEY", spec.Inject{Domain: "a.example.com", Header: "X", Format: "%s"}),
		apiKey("injectonly", "runtime", true, "", spec.Inject{Domain: "b.example.com", Header: "X", Format: "%s"}),
	})
	if !slices.Equal(env, []string{"NAMED_KEY=" + Sentinel}) {
		t.Fatalf("SentinelEnv = %v; a named key gets the sentinel, an inject-only key gets nothing", env)
	}
}

// fakeResolver lets the harness stand in for the run credential's secrets.
func (h *harness) resolve(_ context.Context, run, service string) (string, error) {
	key := run + "/" + service
	h.mu.Lock()
	defer h.mu.Unlock()
	h.resolved = append(h.resolved, key)
	if h.resolveErr != nil {
		return "", h.resolveErr
	}
	v, ok := h.secrets[key]
	if !ok {
		return "", ErrUnbound
	}
	return v, nil
}

func (h *harness) bind(key, value string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.secrets[key] = value
}

func (h *harness) failResolves(err error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.resolveErr = err
}
