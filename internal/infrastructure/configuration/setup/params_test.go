package setup

import (
	"context"
	"testing"
)

// rejectingPrompter fails the test if any question is asked, proving that a
// fully-populated Params never touches the prompt surface.
// scriptedSecretPrompter answers only the secret reads. Any other prompt means
// Params failed to supply a non-secret answer; an exhausted script means a
// secret was read that the test did not expect.
type scriptedSecretPrompter struct {
	t       *testing.T
	secrets []string
	next    int
}

func (s *scriptedSecretPrompter) take(prompt string) string {
	if s.next >= len(s.secrets) {
		s.t.Fatalf("unexpected secret read %q: Params supplied every non-secret answer and no more were scripted", prompt)
	}
	v := s.secrets[s.next]
	s.next++
	return v
}

func (s *scriptedSecretPrompter) Select(context.Context, string, []string) (int, error) {
	s.t.Fatal("unexpected Select: Params should have supplied this answer")
	return 0, nil
}

func (s *scriptedSecretPrompter) ReadLine(context.Context, string, string) (string, error) {
	s.t.Fatal("unexpected ReadLine: Params should have supplied this answer")
	return "", nil
}

func (s *scriptedSecretPrompter) ReadSecret(_ context.Context, prompt string) (string, error) {
	return s.take(prompt), nil
}

func (s *scriptedSecretPrompter) Confirm(context.Context, string, bool) (bool, error) {
	s.t.Fatal("unexpected Confirm: Params should have supplied this answer")
	return false, nil
}

// TestRunParams_SecretsComeOnlyFromThePrompter pins the rule that a secret is
// never a parameter. Params supplies every non-secret answer, so the only
// prompts reached are the secret reads, and the values they return are the only
// values that reach the secret sink. TOML gets a reference, never the value.
func TestRunParams_SecretsComeOnlyFromThePrompter(t *testing.T) {
	params := Params{
		BotUser:         "archie-bot",
		Operator:        "Ada",
		ForgeType:       "github",
		ForgeHost:       "https://github.acme.internal",
		Provider:        "ollama", // keyless, so no provider secret is read
		Model:           "llama3",
		TelegramUserIDs: []int64{111, 222},
	}
	p := &scriptedSecretPrompter{t: t, secrets: []string{"ghp-token", "tg-token"}}
	secrets := newFakeSecrets()
	edits, err := RunParams(context.Background(), p, nil, secrets, ExistingValues{}, params)
	if err != nil {
		t.Fatalf("RunParams: %v", err)
	}
	if err := secrets.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if p.next != len(p.secrets) {
		t.Errorf("read %d secrets, want %d: every secret must come from a prompt", p.next, len(p.secrets))
	}
	if got := secrets.committed["env:ARCHIE_GITHUB_TOKEN"]; got != "ghp-token" {
		t.Errorf("committed env:ARCHIE_GITHUB_TOKEN = %q, want ghp-token", got)
	}
	if got := secrets.committed["env:ARCHIE_TELEGRAM_TOKEN"]; got != "tg-token" {
		t.Errorf("committed env:ARCHIE_TELEGRAM_TOKEN = %q, want tg-token", got)
	}

	cfg := generateAndLoad(t, edits)
	if cfg.BotUser != "archie-bot" {
		t.Errorf("bot_user = %q, want archie-bot", cfg.BotUser)
	}
	if cfg.Forge.Token.Engine != "env" || cfg.Forge.Token.Key != "ARCHIE_GITHUB_TOKEN" {
		t.Errorf("forge.token = %+v, want a reference to env/ARCHIE_GITHUB_TOKEN", cfg.Forge.Token)
	}
}

// TestRunParams_NonSecretParamsNeverPrompt is the non-secret half: every
// answer Params can carry is supplied, so nothing is asked for except the
// secrets, which Params deliberately cannot supply.
func TestRunParams_NonSecretParamsNeverPrompt(t *testing.T) {
	params := Params{
		BotUser:         "archie-bot",
		Operator:        "Ada",
		ForgeType:       "none",
		Provider:        "ollama",
		Model:           "llama3",
		TelegramUserIDs: []int64{111, 222},
	}
	// Ollama is keyless and forge "none" stores no token, so this run asks for
	// exactly one secret: the Telegram bot token.
	p := &scriptedSecretPrompter{t: t, secrets: []string{"tg-token"}}
	secrets := newFakeSecrets()
	edits, err := RunParams(context.Background(), p, nil, secrets, ExistingValues{}, params)
	if err != nil {
		t.Fatalf("RunParams: %v", err)
	}
	cfg := generateAndLoad(t, edits)
	if cfg.BotUser != "archie-bot" {
		t.Errorf("bot_user = %q, want archie-bot", cfg.BotUser)
	}
	if cfg.Forge.Type != "none" {
		t.Errorf("forge.type = %q, want none", cfg.Forge.Type)
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
