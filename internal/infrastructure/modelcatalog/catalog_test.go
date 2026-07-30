package modelcatalog

import (
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/samcharles93/archie-core/internal/config"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func catalogClient(body string) *http.Client {
	return &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(body)),
			Header:     make(http.Header),
		}, nil
	})}
}

func TestLoadDownloadsFiltersAndDiscoversUsableProviders(t *testing.T) {
	const body = `{
		"openai": {
			"id": "openai",
			"name": "OpenAI",
			"npm": "@ai-sdk/openai",
			"api": "https://api.openai.test/v1",
			"env": ["OPENAI_API_KEY"],
			"models": {
				"tool-model": {"id":"tool-model","name":"Tool Model","tool_call":true,"limit":{"context":1000,"output":100}},
				"text-model": {"id":"text-model","name":"Text Model","tool_call":false}
			}
		},
		"anthropic": {
			"id": "anthropic",
			"npm": "@ai-sdk/anthropic",
			"env": ["ANTHROPIC_API_KEY"],
			"models": {"claude": {"id":"claude","tool_call":true}}
		}
	}`
	cachePath := filepath.Join(t.TempDir(), "models.json")
	got, err := Load(context.Background(), Options{
		URL:        "https://models.test/catalog.json",
		CachePath:  cachePath,
		HTTPClient: catalogClient(body),
		Getenv: func(name string) string {
			if name == "OPENAI_API_KEY" {
				return "present"
			}
			return ""
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Providers) != 1 {
		t.Fatalf("providers = %#v, want one usable provider", got.Providers)
	}
	provider := got.Providers[0]
	if provider.ID != "openai" || provider.Class != "openai" || provider.APIKeyEnv != "OPENAI_API_KEY" {
		t.Fatalf("provider = %#v", provider)
	}
	if len(provider.Models) != 1 || provider.Models[0].ID != "tool-model" {
		t.Fatalf("models = %#v, want only tool-capable model", provider.Models)
	}
	if provider.Models[0].ContextWindow != 1000 || provider.Models[0].MaxOutputTokens != 100 {
		t.Fatalf("model limits = %#v", provider.Models[0])
	}
	if _, err := os.Stat(cachePath); err != nil {
		t.Fatalf("cache was not written: %v", err)
	}
}

func TestLoadFallsBackToCache(t *testing.T) {
	cachePath := filepath.Join(t.TempDir(), "models.json")
	if err := os.WriteFile(cachePath, []byte(`{
		"openai": {
			"id":"openai",
			"npm":"@ai-sdk/openai",
			"env":["OPENAI_API_KEY"],
			"models":{"cached":{"id":"cached","tool_call":true}}
		}
	}`), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := Load(context.Background(), Options{
		URL:       "http://127.0.0.1:1",
		CachePath: cachePath,
		Getenv:    func(string) string { return "present" },
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Providers) != 1 || got.Providers[0].Models[0].ID != "cached" {
		t.Fatalf("snapshot = %#v", got)
	}
}

func TestLoadPreservesAndUsesCacheWhenDownloadIsInvalid(t *testing.T) {
	cachePath := filepath.Join(t.TempDir(), "models.json")
	cached := []byte(`{
		"openai": {
			"id":"openai",
			"npm":"@ai-sdk/openai",
			"env":["OPENAI_API_KEY"],
			"models":{"cached":{"id":"cached","tool_call":true}}
		}
	}`)
	if err := os.WriteFile(cachePath, cached, 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := Load(context.Background(), Options{
		URL:        "https://models.test/catalog.json",
		CachePath:  cachePath,
		HTTPClient: catalogClient(`{"broken":`),
		Getenv:     func(string) string { return "present" },
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Providers) != 1 || got.Providers[0].Models[0].ID != "cached" {
		t.Fatalf("snapshot = %#v, want preserved cached catalog", got)
	}
	after, err := os.ReadFile(cachePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(cached) {
		t.Fatalf("invalid download replaced cache:\n%s", after)
	}
}

func TestLoadExplicitProviderOverridesCatalogDefaults(t *testing.T) {
	const body = `{
		"openai": {
			"id":"openai",
			"npm":"@ai-sdk/openai",
			"api":"https://catalog.invalid/v1",
			"env":["OPENAI_API_KEY"],
			"models":{"tool":{"id":"tool","tool_call":true}}
		}
	}`
	got, err := Load(context.Background(), Options{
		URL:        "https://models.test/catalog.json",
		CachePath:  filepath.Join(t.TempDir(), "models.json"),
		HTTPClient: catalogClient(body),
		Getenv:     func(string) string { return "present" },
		Configured: map[string]config.Provider{
			"openai": {
				Class:     "openai-compatible",
				APIKeyEnv: "CUSTOM_KEY",
				BaseURL:   "https://custom.test/v1",
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	provider := got.Providers[0]
	if provider.Class != "openai-compatible" || provider.APIKeyEnv != "CUSTOM_KEY" ||
		provider.BaseURL != "https://custom.test/v1" {
		t.Fatalf("configured override lost: %#v", provider)
	}
}

func TestLoadMapsGoogleCatalogProviderToSDKGeminiClass(t *testing.T) {
	const body = `{
		"google": {
			"id":"google",
			"npm":"@ai-sdk/google",
			"env":["GOOGLE_API_KEY","GEMINI_API_KEY"],
			"models":{"gemini":{"id":"gemini","tool_call":true}}
		}
	}`
	got, err := Load(context.Background(), Options{
		URL:        "https://models.test/catalog.json",
		CachePath:  filepath.Join(t.TempDir(), "models.json"),
		HTTPClient: catalogClient(body),
		Getenv: func(name string) string {
			if name == "GEMINI_API_KEY" {
				return "present"
			}
			return ""
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Providers) != 1 {
		t.Fatalf("providers = %#v, want Google provider", got.Providers)
	}
	if got.Providers[0].Class != "gemini" {
		t.Fatalf("Google class = %q, want ai-sdk native gemini class", got.Providers[0].Class)
	}
	if got.Providers[0].APIKeyEnv != "GEMINI_API_KEY" {
		t.Fatalf("Google key env = %q, want present catalog env", got.Providers[0].APIKeyEnv)
	}
}

func TestLoadExcludesProviderWithoutSupportedSDKClass(t *testing.T) {
	const body = `{
		"native-only": {
			"id":"native-only",
			"npm":"@ai-sdk/native-only",
			"env":["NATIVE_KEY"],
			"models":{"tool":{"id":"tool","tool_call":true}}
		}
	}`
	got, err := Load(context.Background(), Options{
		URL: "https://models.test/catalog.json", CachePath: filepath.Join(t.TempDir(), "models.json"),
		HTTPClient: catalogClient(body), Getenv: func(string) string { return "present" },
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Providers) != 0 {
		t.Fatalf("providers = %#v, want unsupported provider excluded", got.Providers)
	}
}
