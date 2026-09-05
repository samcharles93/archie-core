// Package embedding implements the internal/domain/embedding.Client
// contract over the ai-sdk provider SDKs already vendored for chat, so
// this capability is config-driven the same way chat model roles already
// are (a providers table + a role in [models]).
package embedding

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	sdkembed "github.com/samcharles93/ai-sdk/embed"
	"github.com/samcharles93/ai-sdk/provider/cohere"
	"github.com/samcharles93/ai-sdk/provider/gemini"
	"github.com/samcharles93/ai-sdk/provider/mistral"
	"github.com/samcharles93/ai-sdk/provider/ollama"
	"github.com/samcharles93/ai-sdk/provider/openai"

	"github.com/samcharles93/archie-core/internal/config"
	domainembedding "github.com/samcharles93/archie-core/internal/domain/embedding"
)

// Role is the [models] key embedding consumers use, matching the
// "builder"/"planner"/"triage" role convention chat models already use
// (internal/domain/workflow/agent.go).
const Role = "embedding"

// DefaultTimeout bounds one embedding request when Options.Timeout is
// unset.
const DefaultTimeout = 30 * time.Second

// Options configures New.
type Options struct {
	// Getenv resolves a provider's APIKeyEnv to its credential. Defaults to
	// os.Getenv; overridable for tests.
	Getenv func(string) string
	// Timeout bounds one embedding request. Defaults to DefaultTimeout.
	Timeout time.Duration
}

// New builds an embedding client from cfg.Models[Role] and the matching
// cfg.Providers entry. It reports (nil, false) rather than an error when
// the capability is not usable right now -- no role configured, an unknown
// provider, an unsupported provider class, or a missing/blank credential
// -- per the credential-missing-degrades-not-fatal rule (AGENTS.md):
// callers must skip the capability, never fail daemon startup or an
// unrelated call, over this.
//
// cfg.Providers is expected to already have secret refs resolved to
// APIKeyEnv (see internal/app/archied/provider_secrets.go's
// resolveProviderSecrets), the same precondition agentexec.NewRuntime
// relies on for chat models.
func New(cfg config.Config, opts Options) (domainembedding.Client, bool) {
	getenv := opts.Getenv
	if getenv == nil {
		getenv = os.Getenv
	}
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}

	providerID, modelID, ok := parseModelRef(cfg.Models[Role])
	if !ok {
		return nil, false
	}
	provider, ok := cfg.Providers[providerID]
	if !ok {
		return nil, false
	}

	apiKey := ""
	if provider.APIKeyEnv != "" {
		apiKey = strings.TrimSpace(getenv(provider.APIKeyEnv))
		if apiKey == "" {
			return nil, false
		}
	}

	sdkProvider, err := newSDKProvider(provider.Class, apiKey, provider.BaseURL, &http.Client{Timeout: timeout})
	if err != nil {
		return nil, false
	}
	return &client{provider: sdkembed.NewClient(sdkProvider), model: modelID}, true
}

// parseModelRef splits a "provider/model" role value the same way
// ai-sdk/runtime.Runtime.ParseModelRef does, without depending on a
// Runtime instance.
func parseModelRef(ref string) (providerID, modelID string, ok bool) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return "", "", false
	}
	providerID, modelID, hasSlash := strings.Cut(ref, "/")
	providerID, modelID = strings.TrimSpace(providerID), strings.TrimSpace(modelID)
	if !hasSlash || providerID == "" || modelID == "" {
		return "", "", false
	}
	return providerID, modelID, true
}

// newSDKProvider constructs the ai-sdk embed.Provider for a config class.
// Only classes matching config.Provider's shape (APIKey/BaseURL, no
// provider-specific extra fields) are supported -- azure's Deployment
// requirement, for example, has no field to source it from yet, so an
// "azure" class degrades like any other unsupported class rather than
// gaining a special-cased partial wiring.
func newSDKProvider(class, apiKey, baseURL string, httpClient *http.Client) (sdkembed.Provider, error) {
	switch class {
	case "openai", "openai-compatible":
		return openai.New(openai.Config{APIKey: apiKey, BaseURL: baseURL, HTTPClient: httpClient})
	case "gemini":
		return gemini.New(gemini.Config{APIKey: apiKey, BaseURL: baseURL, HTTPClient: httpClient})
	case "ollama":
		return ollama.New(ollama.Config{BaseURL: baseURL, HTTPClient: httpClient}), nil
	case "cohere":
		return cohere.New(cohere.Config{APIKey: apiKey, BaseURL: baseURL, HTTPClient: httpClient})
	case "mistral":
		return mistral.New(mistral.Config{APIKey: apiKey, BaseURL: baseURL, HTTPClient: httpClient})
	default:
		return nil, fmt.Errorf("embedding: provider class %q does not support embeddings", class)
	}
}

// client adapts an ai-sdk embed.Client to domainembedding.Client.
type client struct {
	provider *sdkembed.Client
	model    string
}

func (c *client) Embed(ctx context.Context, texts []string) ([]domainembedding.Vector, error) {
	if len(texts) == 0 {
		return nil, domainembedding.ErrEmptyInput
	}
	resp, err := c.provider.Embed(ctx, sdkembed.Request{Model: c.model, Inputs: texts})
	if err != nil {
		return nil, fmt.Errorf("embedding: %w", err)
	}
	vectors := make([]domainembedding.Vector, len(texts))
	for _, e := range resp.Embeddings {
		if e.Index < 0 || e.Index >= len(vectors) {
			continue
		}
		vectors[e.Index] = domainembedding.Vector(e.Vector)
	}
	return vectors, nil
}
