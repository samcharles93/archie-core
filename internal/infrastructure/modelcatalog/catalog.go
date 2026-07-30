// Adapted from /work/apps/tau/internal/providers/snapshot/gen/main.go at
// b41dda42ef869cc919044ff4f23d7b568531c3f5.
//
// Deliberate mutations:
//   - runtime startup fetch and disk fallback replace build-time generation;
//   - every models.dev provider is eligible rather than a Tau-owned allowlist;
//   - usable providers are derived from live environment and Archie overrides.
package modelcatalog

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/samcharles93/ai-sdk/runtime"

	"github.com/samcharles93/archie-core/internal/config"
)

type Options struct {
	URL        string
	CachePath  string
	Getenv     func(string) string
	Configured map[string]config.Provider
	HTTPClient *http.Client
}

type Snapshot struct {
	Providers []Provider
}

type Provider struct {
	ID        string
	Name      string
	Class     string
	APIKeyEnv string
	BaseURL   string
	Models    []Model
}

type Model struct {
	ID              string
	Name            string
	ContextWindow   int
	MaxOutputTokens int
	Reasoning       bool
	Attachment      bool
	Structured      bool
	InputModalities []string
}

type catalogProvider struct {
	ID     string                          `json:"id"`
	Name   string                          `json:"name"`
	NPM    string                          `json:"npm"`
	API    string                          `json:"api"`
	Env    []string                        `json:"env"`
	Models map[string]runtime.CatalogModel `json:"models"`
}

func Load(ctx context.Context, opts Options) (Snapshot, error) {
	if opts.Getenv == nil {
		opts.Getenv = os.Getenv
	}
	raw, fetchErr := fetch(ctx, opts)
	if fetchErr == nil {
		snapshot, parseErr := parse(raw, opts)
		if parseErr == nil {
			if opts.CachePath != "" {
				// The downloaded catalog is already usable. A cache persistence
				// failure must not prevent startup with valid in-memory data.
				_ = writeCache(opts.CachePath, raw)
			}
			return snapshot, nil
		}
		fetchErr = parseErr
	}
	cached, cacheErr := os.ReadFile(opts.CachePath)
	if cacheErr != nil {
		return Snapshot{}, errors.Join(fetchErr, fmt.Errorf("read model catalog cache: %w", cacheErr))
	}
	snapshot, cacheErr := parse(cached, opts)
	if cacheErr != nil {
		return Snapshot{}, errors.Join(fetchErr, fmt.Errorf("parse cached model catalog: %w", cacheErr))
	}
	return snapshot, nil
}

func fetch(ctx context.Context, opts Options) ([]byte, error) {
	url := strings.TrimSpace(opts.URL)
	if url == "" {
		url = runtime.DefaultCatalogURL
	}
	client := opts.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("build model catalog request: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch model catalog: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch model catalog: status %d", resp.StatusCode)
	}
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read model catalog response: %w", err)
	}
	return raw, nil
}

func writeCache(path string, raw []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".models-*.json")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(raw); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

func parse(raw []byte, opts Options) (Snapshot, error) {
	var catalog map[string]catalogProvider
	if err := json.Unmarshal(raw, &catalog); err != nil {
		return Snapshot{}, fmt.Errorf("parse model catalog: %w", err)
	}
	providers := make([]Provider, 0, len(catalog))
	for key, source := range catalog {
		if provider, ok := usableProvider(key, source, opts); ok {
			providers = append(providers, provider)
		}
	}
	slices.SortFunc(providers, func(a, b Provider) int {
		return strings.Compare(a.Name, b.Name)
	})
	return Snapshot{Providers: providers}, nil
}

func usableProvider(key string, source catalogProvider, opts Options) (Provider, bool) {
	id := strings.TrimSpace(source.ID)
	if id == "" {
		id = key
	}
	override, configured := opts.Configured[id]
	envVar := firstSetEnv(source.Env, opts.Getenv)
	if configured && override.APIKeyEnv != "" {
		envVar = override.APIKeyEnv
	}
	if (!configured && envVar == "") ||
		(configured && override.APIKeyEnv != "" && opts.Getenv(override.APIKeyEnv) == "") {
		return Provider{}, false
	}
	models := toolModels(source.Models)
	if len(models) == 0 {
		return Provider{}, false
	}
	class, baseURL := providerRuntimeDefaults(source)
	if configured {
		if override.Class != "" {
			class = override.Class
		}
		if override.BaseURL != "" {
			baseURL = override.BaseURL
		}
	}
	name := strings.TrimSpace(source.Name)
	if name == "" {
		name = displayName(id)
	}
	return Provider{
		ID: id, Name: name, Class: class, APIKeyEnv: envVar,
		BaseURL: baseURL, Models: models,
	}, true
}

func providerRuntimeDefaults(source catalogProvider) (class, baseURL string) {
	class = "openai-compatible"
	if mapped, ok := runtime.NPMClassMapping[source.NPM]; ok {
		class = mapped
	}
	return class, source.API
}

func firstSetEnv(names []string, getenv func(string) string) string {
	for _, name := range names {
		if name = strings.TrimSpace(name); name != "" && getenv(name) != "" {
			return name
		}
	}
	return ""
}

func toolModels(source map[string]runtime.CatalogModel) []Model {
	models := make([]Model, 0, len(source))
	for key, model := range source {
		if !model.ToolCall {
			continue
		}
		id := strings.TrimSpace(model.ID)
		if id == "" {
			id = key
		}
		models = append(models, Model{
			ID: id, Name: model.Name,
			ContextWindow: model.ContextWindow(), MaxOutputTokens: model.MaxOutputTokens(),
			Reasoning: model.Reasoning, Attachment: model.Attachment, Structured: model.Structured,
			InputModalities: slices.Clone(model.Modalities.Input),
		})
	}
	slices.SortFunc(models, func(a, b Model) int { return strings.Compare(a.ID, b.ID) })
	return models
}

func displayName(id string) string {
	words := strings.FieldsFunc(id, func(r rune) bool { return r == '-' || r == '_' })
	for i := range words {
		if words[i] != "" {
			words[i] = strings.ToUpper(words[i][:1]) + words[i][1:]
		}
	}
	return strings.Join(words, " ")
}
