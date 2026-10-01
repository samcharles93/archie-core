package transcription

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/domain/messaging"
)

// TestNewDegradesRatherThanErroring pins the credential-missing-degrades
// contract: every reason the capability cannot serve right now yields
// (nil, false), so the channel keeps the media note instead of failing.
func TestNewDegradesRatherThanErroring(t *testing.T) {
	configured := config.Provider{Class: "openai", APIKeyEnv: "TRANSCRIPTION_TEST_KEY"}
	cases := []struct {
		name      string
		models    map[string]string
		providers map[string]config.Provider
		env       map[string]string
	}{
		{
			name:      "no role configured",
			providers: map[string]config.Provider{"openai": configured},
		},
		{
			name:   "role names a provider that is not configured",
			models: map[string]string{Role: "openai/whisper-1"},
		},
		{
			name:   "role is not a provider/model ref",
			models: map[string]string{Role: "whisper-1"},
		},
		{
			name:      "provider class does not support transcription",
			models:    map[string]string{Role: "anthropic/whisper-1"},
			providers: map[string]config.Provider{"anthropic": {Class: "anthropic", APIKeyEnv: "TRANSCRIPTION_TEST_KEY"}},
			env:       map[string]string{"TRANSCRIPTION_TEST_KEY": "secret"},
		},
		{
			name:      "credential env var is unset",
			models:    map[string]string{Role: "openai/whisper-1"},
			providers: map[string]config.Provider{"openai": configured},
		},
		{
			name:      "credential env var resolves empty",
			models:    map[string]string{Role: "openai/whisper-1"},
			providers: map[string]config.Provider{"openai": configured},
			env:       map[string]string{"TRANSCRIPTION_TEST_KEY": "   "},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client, ok := New(tc.models, tc.providers, Options{
				Getenv: func(k string) string { return tc.env[k] },
			})
			if ok || client != nil {
				t.Fatalf("New() = (%v, %v), want (nil, false)", client, ok)
			}
		})
	}
}

// TestNewResolvesSecretReference proves the api_key path a `[providers.*]`
// entry written with a secret engine takes is wired, not silently ignored
// in favour of api_key_env only.
func TestNewResolvesSecretReference(t *testing.T) {
	provider := config.Provider{Class: "openai", APIKey: config.SecretRef{Engine: "bws", Key: "OPENAI"}}
	client, ok := New(
		map[string]string{Role: "openai/whisper-1"},
		map[string]config.Provider{"openai": provider},
		Options{ResolveSecret: func(ref config.SecretRef) (string, error) {
			if ref.Key != "OPENAI" {
				t.Fatalf("ResolveSecret ref = %#v, want the provider's reference", ref)
			}
			return "from-engine", nil
		}},
	)
	if !ok || client == nil {
		t.Fatal("New() with a resolvable secret reference = (nil, false), want a client")
	}

	// An unresolvable reference degrades instead of returning a broken client.
	if c, ok := New(
		map[string]string{Role: "openai/whisper-1"},
		map[string]config.Provider{"openai": provider},
		Options{ResolveSecret: func(config.SecretRef) (string, error) { return "", errors.New("no secret") }},
	); ok || c != nil {
		t.Fatalf("New() with an unresolvable reference = (%v, %v), want (nil, false)", c, ok)
	}
}

// TestTranscribeCallsTheWhisperEndpoint proves the adapter actually sends
// the audio and returns the provider's text, against a fake
// Whisper-compatible server (a local OpenAI-compatible backend needs no
// credential, which is what a self-hosted Ollama-style server looks like).
func TestTranscribeCallsTheWhisperEndpoint(t *testing.T) {
	var gotModel, gotAudio string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/audio/transcriptions" {
			t.Errorf("request path = %q, want /v1/audio/transcriptions", r.URL.Path)
		}
		gotModel = r.FormValue("model")
		if file, _, err := r.FormFile("file"); err == nil {
			defer file.Close()
			data, _ := io.ReadAll(file)
			gotAudio = string(data)
		}
		_, _ = w.Write([]byte(`{"text":"  turn on the lights  "}`))
	}))
	t.Cleanup(srv.Close)

	client, ok := New(
		map[string]string{Role: "local/whisper-large-v3"},
		map[string]config.Provider{"local": {Class: "openai-compatible", BaseURL: srv.URL + "/v1"}},
		Options{},
	)
	if !ok {
		t.Fatal("New() with a local openai-compatible provider degraded, want a client")
	}

	text, err := client.Transcribe(context.Background(), []byte("OggS\x00fake-audio"))
	if err != nil {
		t.Fatalf("Transcribe() error = %v", err)
	}
	if text != "turn on the lights" {
		t.Errorf("Transcribe() = %q, want the trimmed provider text", text)
	}
	if gotModel != "whisper-large-v3" {
		t.Errorf("model field = %q, want the model from the role ref", gotModel)
	}
	if !strings.Contains(gotAudio, "fake-audio") {
		t.Errorf("file field = %q, want the audio bytes", gotAudio)
	}
}

// TestTranscribeRejectsEmptyAudio proves the contract's non-empty-audio
// requirement is enforced with the typed sentinel, not a provider error.
func TestTranscribeRejectsEmptyAudio(t *testing.T) {
	client, ok := New(
		map[string]string{Role: "local/whisper-large-v3"},
		map[string]config.Provider{"local": {Class: "openai-compatible", BaseURL: "http://127.0.0.1:1/v1"}},
		Options{},
	)
	if !ok {
		t.Fatal("New() degraded for a local openai-compatible provider")
	}
	if _, err := client.Transcribe(context.Background(), nil); !errors.Is(err, messaging.ErrTranscriptionUnavailable) {
		t.Fatalf("Transcribe(nil) error = %v, want ErrTranscriptionUnavailable", err)
	}
}
