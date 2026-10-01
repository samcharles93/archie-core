// Package transcription implements the internal/domain/messaging.Transcriber
// contract over the ai-sdk provider SDKs already vendored for chat, so voice
// notes are transcribed through the same providers table and model-role
// convention chat models and the embedding capability already use.
package transcription

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/samcharles93/ai-sdk/provider/groq"
	"github.com/samcharles93/ai-sdk/provider/openai"
	sdktranscribe "github.com/samcharles93/ai-sdk/transcribe"

	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/domain/messaging"
)

// Role is the [models] key transcription consumers use, matching the
// "builder"/"planner"/"triage" role convention chat models and the
// embedding capability already use.
const Role = "transcription"

// DefaultTimeout bounds one transcription request when Options.Timeout is
// unset. It is generous relative to a chat request because transcription
// uploads the whole audio file before the provider answers.
const DefaultTimeout = 60 * time.Second

// Options configures New.
type Options struct {
	// Getenv resolves a provider's APIKeyEnv to its credential. Defaults to
	// os.Getenv; overridable for tests.
	Getenv func(string) string
	// ResolveSecret resolves a provider's api_key secret reference. Nil means
	// secret references cannot be resolved, so such a provider degrades.
	ResolveSecret func(config.SecretRef) (string, error)
	// Timeout bounds one transcription request. Defaults to DefaultTimeout.
	Timeout time.Duration
}

// New builds a transcription client from models[Role] and the matching
// providers entry. It reports (nil, false) rather than an error when the
// capability is not usable right now -- no role configured, an unknown
// provider, an unsupported provider class, or a missing/blank credential --
// per the credential-missing-degrades-not-fatal rule (AGENTS.md): callers
// keep the media note, never fail the turn or daemon startup, over this.
func New(models map[string]string, providers map[string]config.Provider, opts Options) (messaging.Transcriber, bool) {
	getenv := opts.Getenv
	if getenv == nil {
		getenv = os.Getenv
	}
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}

	providerID, modelID, ok := parseModelRef(models[Role])
	if !ok {
		return nil, false
	}
	provider, ok := providers[providerID]
	if !ok {
		return nil, false
	}
	apiKey, ok := resolveCredential(provider, getenv, opts.ResolveSecret)
	if !ok {
		return nil, false
	}
	sdkProvider, err := newSDKProvider(provider.Class, apiKey, provider.BaseURL, &http.Client{Timeout: timeout})
	if err != nil {
		return nil, false
	}
	return &client{client: sdktranscribe.NewClient(sdkProvider), model: modelID}, true
}

// resolveCredential returns the provider's key and whether the provider may
// be used. A provider that names neither an env var nor a secret reference is
// permitted with an empty key: a self-hosted OpenAI-compatible backend is
// unauthenticated, and the provider constructor rejects an empty key for the
// hosted classes that require one.
func resolveCredential(provider config.Provider, getenv func(string) string, resolveSecret func(config.SecretRef) (string, error)) (string, bool) {
	switch {
	case provider.APIKeyEnv != "":
		key := strings.TrimSpace(getenv(provider.APIKeyEnv))
		return key, key != ""
	case provider.APIKey != (config.SecretRef{}):
		if resolveSecret == nil {
			return "", false
		}
		key, err := resolveSecret(provider.APIKey)
		if err != nil {
			return "", false
		}
		key = strings.TrimSpace(key)
		return key, key != ""
	default:
		return "", true
	}
}

// parseModelRef splits a "provider/model" role value without depending on an
// ai-sdk Runtime instance (mirrors internal/infrastructure/embedding).
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

// newSDKProvider constructs the ai-sdk transcribe.Provider for a config class.
// Only classes that speak the Whisper-compatible /audio/transcriptions
// endpoint are supported; any other class degrades like a missing role rather
// than gaining a special-cased partial wiring.
func newSDKProvider(class, apiKey, baseURL string, httpClient *http.Client) (sdktranscribe.Provider, error) {
	switch class {
	case "openai", "openai-compatible":
		return openai.New(openai.Config{APIKey: apiKey, BaseURL: baseURL, HTTPClient: httpClient})
	case "groq":
		return groq.New(groq.Config{APIKey: apiKey, BaseURL: baseURL, HTTPClient: httpClient})
	default:
		return nil, fmt.Errorf("transcription: provider class %q does not support transcription", class)
	}
}

// client adapts an ai-sdk transcribe.Client to messaging.Transcriber.
type client struct {
	client *sdktranscribe.Client
	model  string
}

func (c *client) Transcribe(ctx context.Context, audio []byte) (string, error) {
	if len(audio) == 0 {
		return "", fmt.Errorf("%w: no audio to transcribe", messaging.ErrTranscriptionUnavailable)
	}
	resp, err := c.client.Transcribe(ctx, sdktranscribe.TranscribeRequest{Model: c.model, Audio: audio})
	if err != nil {
		return "", fmt.Errorf("%w: %w", messaging.ErrTranscriptionUnavailable, err)
	}
	return strings.TrimSpace(resp.Text), nil
}
