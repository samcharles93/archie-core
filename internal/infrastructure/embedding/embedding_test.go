package embedding

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/samcharles93/archie-core/internal/config"
	domainembedding "github.com/samcharles93/archie-core/internal/domain/embedding"
)

func TestNewDegradesWhenNoRoleConfigured(t *testing.T) {
	t.Parallel()
	cfg := config.Config{}
	if _, ok := New(cfg, Options{}); ok {
		t.Fatal("New() ok = true, want false when models[embedding] is unset")
	}
}

func TestNewDegradesWhenProviderUnknown(t *testing.T) {
	t.Parallel()
	cfg := config.Config{
		Models: map[string]string{Role: "openai/text-embedding-3-small"},
	}
	if _, ok := New(cfg, Options{}); ok {
		t.Fatal("New() ok = true, want false when the provider is not configured")
	}
}

func TestNewDegradesWhenRefHasNoProviderPrefix(t *testing.T) {
	t.Parallel()
	cfg := config.Config{
		Models:    map[string]string{Role: "text-embedding-3-small"},
		Providers: map[string]config.Provider{"openai": {Class: "openai", APIKeyEnv: "TEST_KEY"}},
	}
	if _, ok := New(cfg, Options{Getenv: constEnv("secret")}); ok {
		t.Fatal("New() ok = true, want false for a model ref with no provider/ prefix")
	}
}

func TestNewDegradesWhenCredentialMissing(t *testing.T) {
	t.Parallel()
	cfg := config.Config{
		Models:    map[string]string{Role: "openai/text-embedding-3-small"},
		Providers: map[string]config.Provider{"openai": {Class: "openai", APIKeyEnv: "TEST_MISSING_KEY"}},
	}
	if _, ok := New(cfg, Options{Getenv: constEnv("")}); ok {
		t.Fatal("New() ok = true, want false when the configured credential env var is empty")
	}
}

func TestNewDegradesForUnsupportedProviderClass(t *testing.T) {
	t.Parallel()
	cfg := config.Config{
		Models:    map[string]string{Role: "custom/some-model"},
		Providers: map[string]config.Provider{"custom": {Class: "azure", APIKeyEnv: "TEST_KEY"}},
	}
	if _, ok := New(cfg, Options{Getenv: constEnv("secret")}); ok {
		t.Fatal("New() ok = true, want false for a provider class embeddings doesn't support")
	}
}

func TestNewSucceedsWhenNoCredentialRequired(t *testing.T) {
	t.Parallel()
	cfg := config.Config{
		Models:    map[string]string{Role: "local/nomic-embed-text"},
		Providers: map[string]config.Provider{"local": {Class: "ollama"}},
	}
	if _, ok := New(cfg, Options{}); !ok {
		t.Fatal("New() ok = false, want true for a credential-free provider class (ollama)")
	}
}

func TestClientEmbedReturnsVectorsInInputOrder(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/embeddings" {
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
		var body struct {
			Model string   `json:"model"`
			Input []string `json:"input"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if body.Model != "text-embedding-3-small" {
			t.Fatalf("model = %q, want %q", body.Model, "text-embedding-3-small")
		}
		resp := map[string]any{
			"object": "list",
			"model":  body.Model,
			"data": []map[string]any{
				{"object": "embedding", "index": 1, "embedding": []float32{0.4, 0.5}},
				{"object": "embedding", "index": 0, "embedding": []float32{0.1, 0.2}},
			},
			"usage": map[string]any{"prompt_tokens": 3, "total_tokens": 3},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	t.Cleanup(srv.Close)

	cfg := config.Config{
		Models: map[string]string{Role: "openai/text-embedding-3-small"},
		Providers: map[string]config.Provider{
			"openai": {Class: "openai", APIKeyEnv: "TEST_OPENAI_KEY", BaseURL: srv.URL},
		},
	}
	c, ok := New(cfg, Options{Getenv: constEnv("secret")})
	if !ok {
		t.Fatal("New() ok = false, want true")
	}

	vectors, err := c.Embed(context.Background(), []string{"hello", "world"})
	if err != nil {
		t.Fatalf("Embed() error = %v", err)
	}
	if len(vectors) != 2 {
		t.Fatalf("len(vectors) = %d, want 2", len(vectors))
	}
	if got := vectors[0]; len(got) != 2 || got[0] != 0.1 || got[1] != 0.2 {
		t.Fatalf("vectors[0] = %v, want [0.1 0.2]", got)
	}
	if got := vectors[1]; len(got) != 2 || got[0] != 0.4 || got[1] != 0.5 {
		t.Fatalf("vectors[1] = %v, want [0.4 0.5]", got)
	}
}

func TestClientEmbedSurfacesProviderFailureAsError(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"invalid api key"}`))
	}))
	t.Cleanup(srv.Close)

	cfg := config.Config{
		Models: map[string]string{Role: "openai/text-embedding-3-small"},
		Providers: map[string]config.Provider{
			"openai": {Class: "openai", APIKeyEnv: "TEST_OPENAI_KEY", BaseURL: srv.URL},
		},
	}
	c, ok := New(cfg, Options{Getenv: constEnv("bad-key")})
	if !ok {
		t.Fatal("New() ok = false, want true")
	}

	if _, err := c.Embed(context.Background(), []string{"hello"}); err == nil {
		t.Fatal("Embed() error = nil, want a reported error for the 401 response")
	}
}

func TestClientEmbedRespectsTimeout(t *testing.T) {
	t.Parallel()

	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release
	}))
	// Cleanups run LIFO: srv.Close (registered first) waits for the
	// in-flight handler to return, so release must close before it does --
	// hence registering it second, not for statement-order readability.
	t.Cleanup(srv.Close)
	t.Cleanup(func() { close(release) })

	cfg := config.Config{
		Models: map[string]string{Role: "openai/text-embedding-3-small"},
		Providers: map[string]config.Provider{
			"openai": {Class: "openai", APIKeyEnv: "TEST_OPENAI_KEY", BaseURL: srv.URL},
		},
	}
	c, ok := New(cfg, Options{Getenv: constEnv("secret"), Timeout: 50 * time.Millisecond})
	if !ok {
		t.Fatal("New() ok = false, want true")
	}

	start := time.Now()
	_, err := c.Embed(context.Background(), []string{"hello"})
	if err == nil {
		t.Fatal("Embed() error = nil, want a timeout error")
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("Embed() took %v, want it bounded by the configured timeout", elapsed)
	}
}

func TestClientEmbedRejectsEmptyInput(t *testing.T) {
	t.Parallel()
	cfg := config.Config{
		Models:    map[string]string{Role: "local/nomic-embed-text"},
		Providers: map[string]config.Provider{"local": {Class: "ollama"}},
	}
	c, ok := New(cfg, Options{})
	if !ok {
		t.Fatal("New() ok = false, want true")
	}
	if _, err := c.Embed(context.Background(), nil); !errors.Is(err, domainembedding.ErrEmptyInput) {
		t.Fatalf("Embed() error = %v, want %v", err, domainembedding.ErrEmptyInput)
	}
}

func constEnv(value string) func(string) string {
	return func(string) string { return value }
}
