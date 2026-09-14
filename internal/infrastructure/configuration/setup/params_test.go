package setup

import (
	"context"
	"testing"
)

// rejectingPrompter fails the test if any question is asked, proving that a
// fully-populated Params never touches the prompt surface.
type rejectingPrompter struct {
	t *testing.T
}

func (r rejectingPrompter) Select(context.Context, string, []string) (int, error) {
	r.t.Fatal("unexpected Select call: Params should have supplied this answer")
	return 0, nil
}

func (r rejectingPrompter) ReadLine(context.Context, string, string) (string, error) {
	r.t.Fatal("unexpected ReadLine call: Params should have supplied this answer")
	return "", nil
}

func (r rejectingPrompter) ReadSecret(context.Context, string) (string, error) {
	r.t.Fatal("unexpected ReadSecret call: Params should have supplied this answer")
	return "", nil
}

func (r rejectingPrompter) Confirm(context.Context, string, bool) (bool, error) {
	r.t.Fatal("unexpected Confirm call: Params should have supplied this answer")
	return false, nil
}

func TestRunParams_ProviderAPIKeyNeverPrompts(t *testing.T) {
	params := Params{
		BotUser:        "archie-bot",
		Operator:       "Ada", // Run asks for the operator unconditionally; see the note below
		ForgeType:      "none",
		Provider:       "openai",
		Model:          "gpt-5.4",
		ProviderAPIKey: "sk-parameterised",
		// Telegram enablement is a yes/no question with no parameter to
		// pre-answer it, so a genuinely prompt-free run supplies the channel
		// outright. Everything else here is supplied, which is the point: the
		// provider key must come from Params rather than ReadSecret.
		TelegramToken:   "tg-token",
		TelegramUserIDs: []int64{111},
	}
	secrets := newFakeSecrets()
	// rejectingPrompter fails the test on any prompt. Before ProviderAPIKey
	// existed this run had to ask for the key, which is why an unattended
	// install could only ever choose the keyless self-hosted provider.
	edits, err := RunParams(context.Background(), rejectingPrompter{t: t}, nil, secrets, ExistingValues{}, params)
	if err != nil {
		t.Fatalf("RunParams: %v", err)
	}
	if err := secrets.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if got := secrets.committed["env:OPENAI_API_KEY"]; got != "sk-parameterised" {
		t.Errorf("committed env:OPENAI_API_KEY = %q, want sk-parameterised", got)
	}

	cfg := generateAndLoad(t, edits)
	if got := cfg.Providers["openai"].Class; got != "openai" {
		t.Errorf("providers.openai.class = %q, want openai", got)
	}
	if cfg.Providers["openai"].APIKey.Engine != "env" {
		t.Errorf("providers.openai.api_key engine = %q, want env: the key belongs in the env file, not the config", cfg.Providers["openai"].APIKey.Engine)
	}
	if got := cfg.Models["builder"]; got != "openai/gpt-5.4" {
		t.Errorf("models.builder = %q, want openai/gpt-5.4", got)
	}
}

func TestRunParams_FullParamsNeverPrompt(t *testing.T) {
	params := Params{
		BotUser:         "archie-bot",
		Operator:        "Ada",
		ForgeType:       "github",
		ForgeHost:       "https://github.acme.internal",
		ForgeToken:      "ghp-token",
		Provider:        "ollama",
		Model:           "llama3",
		TelegramToken:   "tg-token",
		TelegramUserIDs: []int64{111, 222},
	}
	secrets := newFakeSecrets()
	edits, err := RunParams(context.Background(), rejectingPrompter{t: t}, nil, secrets, ExistingValues{}, params)
	if err != nil {
		t.Fatalf("RunParams: %v", err)
	}
	if err := secrets.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}

	cfg := generateAndLoad(t, edits)
	if cfg.BotUser != "archie-bot" {
		t.Errorf("bot_user = %q, want archie-bot", cfg.BotUser)
	}
	if cfg.Chat.Operator != "Ada" {
		t.Errorf("chat.operator = %q, want Ada", cfg.Chat.Operator)
	}
	if cfg.Forge.Type != "github" {
		t.Errorf("forge.type = %q, want github", cfg.Forge.Type)
	}
	if cfg.Forge.Host != "https://github.acme.internal" {
		t.Errorf("forge.host = %q, want https://github.acme.internal", cfg.Forge.Host)
	}
	if cfg.Forge.Token.Key != "ARCHIE_GITHUB_TOKEN" {
		t.Errorf("forge.token.key = %q, want ARCHIE_GITHUB_TOKEN", cfg.Forge.Token.Key)
	}
	for _, role := range []string{"triage", "planner", "builder"} {
		if cfg.Models[role] != "ollama/llama3" {
			t.Errorf("models[%s] = %q, want ollama/llama3", role, cfg.Models[role])
		}
	}
	if got := secrets.committed["env:ARCHIE_GITHUB_TOKEN"]; got != "ghp-token" {
		t.Errorf("committed github token = %q, want ghp-token", got)
	}
	if got := secrets.committed["env:ARCHIE_TELEGRAM_TOKEN"]; got != "tg-token" {
		t.Errorf("committed telegram token = %q, want tg-token", got)
	}
	if len(cfg.Chat.Telegram.AllowedUserIDs) != 2 || cfg.Chat.Telegram.AllowedUserIDs[0] != 111 || cfg.Chat.Telegram.AllowedUserIDs[1] != 222 {
		t.Errorf("chat.telegram.allowed_user_ids = %v, want [111 222]", cfg.Chat.Telegram.AllowedUserIDs)
	}
}

func TestRunParams_PartialParamsAskOnlyTheRest(t *testing.T) {
	// Bot user, provider and model are supplied; operator, forge and
	// telegram are left for the prompter. The provider API key is never in
	// Params (there is no typed field for it), so it is always prompted.
	p := &fakePrompter{
		lines:    []string{""}, // operator left blank
		selects:  []int{2},     // forge: none
		secrets:  []string{"sk-openai-key"},
		confirms: []bool{false},
	}
	secrets := newFakeSecrets()
	edits, err := RunParams(context.Background(), p, nil, secrets, ExistingValues{}, Params{
		BotUser:  "archie-bot",
		Provider: "openai",
		Model:    "gpt-5.4",
	})
	if err != nil {
		t.Fatalf("RunParams: %v", err)
	}
	if err := secrets.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}

	cfg := generateAndLoad(t, edits)
	if cfg.BotUser != "archie-bot" {
		t.Errorf("bot_user = %q, want archie-bot", cfg.BotUser)
	}
	if cfg.Chat.Operator != "" {
		t.Errorf("chat.operator = %q, want empty (prompt left blank)", cfg.Chat.Operator)
	}
	if cfg.Forge.Type != "none" {
		t.Errorf("forge.type = %q, want none", cfg.Forge.Type)
	}
	if cfg.Providers["openai"].APIKey.Key != "OPENAI_API_KEY" {
		t.Errorf("providers.openai.api_key.key = %q, want OPENAI_API_KEY", cfg.Providers["openai"].APIKey.Key)
	}
	for _, role := range []string{"triage", "planner", "builder"} {
		if cfg.Models[role] != "openai/gpt-5.4" {
			t.Errorf("models[%s] = %q, want openai/gpt-5.4", role, cfg.Models[role])
		}
	}
	if got := secrets.committed["env:OPENAI_API_KEY"]; got != "sk-openai-key" {
		t.Errorf("committed openai key = %q, want sk-openai-key", got)
	}
	if len(secrets.committed) != 1 {
		t.Errorf("committed secrets = %v, want only the openai key", secrets.committed)
	}
}

// The --defaults baseline from the plan's Decision 2: bot user archie-bot,
// github, https://github.com, ollama, llama3, operator/telegram skipped. It
// must produce a loadable config with no secrets written, because ollama is
// the one keyless path.
func TestRun_DefaultsBaselineLoads(t *testing.T) {
	params := Params{
		BotUser:   "archie-bot",
		ForgeType: "github",
		ForgeHost: "https://github.com",
		Provider:  "ollama",
		Model:     "llama3",
	}
	secrets := newFakeSecrets()
	edits, err := RunParams(context.Background(), DefaultPrompter{}, nil, secrets, ExistingValues{}, params)
	if err != nil {
		t.Fatalf("RunParams: %v", err)
	}
	if err := secrets.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}

	cfg := generateAndLoad(t, edits)
	if cfg.BotUser != "archie-bot" {
		t.Errorf("bot_user = %q, want archie-bot", cfg.BotUser)
	}
	if cfg.Forge.Type != "github" || cfg.Forge.Host != "https://github.com" {
		t.Errorf("forge = %q/%q, want github/https://github.com", cfg.Forge.Type, cfg.Forge.Host)
	}
	for _, role := range []string{"triage", "planner", "builder"} {
		if cfg.Models[role] != "ollama/llama3" {
			t.Errorf("models[%s] = %q, want ollama/llama3", role, cfg.Models[role])
		}
	}
	if cfg.Providers["ollama"].Class != "ollama" {
		t.Errorf("providers.ollama.class = %q, want ollama", cfg.Providers["ollama"].Class)
	}
	if len(secrets.committed) != 0 {
		t.Errorf("committed secrets = %v, want none: the defaults baseline is keyless", secrets.committed)
	}
}
