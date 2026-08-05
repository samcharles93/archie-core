package setup

import (
	"context"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"testing"

	configtemplate "github.com/samcharles93/archie-core"
	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/infrastructure/configuration"
	"github.com/samcharles93/archie-core/internal/infrastructure/configuration/tomlwrite"
	"github.com/samcharles93/archie-core/internal/secret"
)

// fakePrompter drives Run from typed scripted answers rather than raw
// terminal bytes. It mirrors Prompter's real contract (an empty ReadLine
// answer resolves to the caller's default; Select/Confirm/ReadSecret return
// their scripted value directly) so a test reads as "what would the
// operator have typed", not as a simulation of terminal parsing --
// terminalprompt's own tests already cover that parsing.
type fakePrompter struct {
	selects  []int
	lines    []string
	secrets  []string
	confirms []bool
}

func (f *fakePrompter) pop(name string, q *[]string) (string, error) {
	if len(*q) == 0 {
		return "", fmt.Errorf("fakePrompter: no more scripted %s answers", name)
	}
	v := (*q)[0]
	*q = (*q)[1:]
	return v, nil
}

func (f *fakePrompter) Select(_ context.Context, _ string, options []string) (int, error) {
	if len(f.selects) == 0 {
		return -1, fmt.Errorf("fakePrompter: no more scripted selects")
	}
	n := f.selects[0]
	f.selects = f.selects[1:]
	if n < 0 || n >= len(options) {
		return -1, fmt.Errorf("fakePrompter: scripted select %d out of range for %d options", n, len(options))
	}
	return n, nil
}

func (f *fakePrompter) ReadLine(_ context.Context, _, defaultValue string) (string, error) {
	v, err := f.pop("line", &f.lines)
	if err != nil {
		return "", err
	}
	if v == "" {
		return defaultValue, nil
	}
	return v, nil
}

func (f *fakePrompter) ReadSecret(_ context.Context, _ string) (string, error) {
	return f.pop("secret", &f.secrets)
}

func (f *fakePrompter) Confirm(_ context.Context, _ string, defaultYes bool) (bool, error) {
	if len(f.confirms) == 0 {
		return false, fmt.Errorf("fakePrompter: no more scripted confirms")
	}
	v := f.confirms[0]
	f.confirms = f.confirms[1:]
	return v, nil
}

// fakeSecrets records Put calls without committing until Commit is called,
// mirroring the real buffer-then-commit contract Run relies on.
type fakeSecrets struct {
	pending   map[string]string // "engine:key" -> value
	committed map[string]string
}

func newFakeSecrets() *fakeSecrets {
	return &fakeSecrets{pending: map[string]string{}, committed: map[string]string{}}
}

func (f *fakeSecrets) Put(engine, key, value string) error {
	f.pending[engine+":"+key] = value
	return nil
}

func (f *fakeSecrets) Commit() error {
	maps.Copy(f.committed, f.pending)
	return nil
}

// generateAndLoad renders edits against the real embedded template and
// loads the result through a real configuration.Loader -- the same check
// Run's own doc comment says the caller must perform. This is what "every
// config setup writes passes configuration.Validate, verified by a test
// that runs setup non-interactively and validates the output" means in
// practice: proving the real load path (defaults, decode, validate, in
// their real order) accepts it, not asserting against a second,
// hand-rolled representation.
func generateAndLoad(t *testing.T, edits []tomlwrite.Edit) config.Config {
	t.Helper()
	out, err := tomlwrite.Generate(configtemplate.Example, edits)
	if err != nil {
		t.Fatalf("tomlwrite.Generate: %v", err)
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, out, 0o600); err != nil {
		t.Fatal(err)
	}
	doc, err := configuration.New(nil).File(path)
	if err != nil {
		t.Fatalf("generated config does not load: %v\n%s", err, out)
	}
	return doc.Config
}

func TestRun_GitHubCloudProvider(t *testing.T) {
	p := &fakePrompter{
		lines:    []string{"acme-archie", "Ada Lovelace", "gpt-5.4"},
		selects:  []int{0, 0}, // OpenAI, GitHub
		secrets:  []string{"sk-openai-token", "ghp-github-token"},
		confirms: []bool{false}, // no telegram
	}
	secrets := newFakeSecrets()
	edits, err := Run(context.Background(), p, nil, secrets, ExistingValues{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if err := secrets.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}

	cfg := generateAndLoad(t, edits)
	if cfg.BotUser != "acme-archie" {
		t.Errorf("bot_user = %q, want acme-archie", cfg.BotUser)
	}
	if cfg.Chat.Operator != "Ada Lovelace" {
		t.Errorf("chat.operator = %q, want Ada Lovelace", cfg.Chat.Operator)
	}
	if cfg.Forge.Type != "github" {
		t.Errorf("forge.type = %q, want github", cfg.Forge.Type)
	}
	if cfg.Forge.Token.Key != "ARCHIE_GITHUB_TOKEN" {
		t.Errorf("forge.token.key = %q, want ARCHIE_GITHUB_TOKEN", cfg.Forge.Token.Key)
	}
	if got, want := secrets.committed["env:ARCHIE_GITHUB_TOKEN"], "ghp-github-token"; got != want {
		t.Errorf("committed github token = %q, want %q", got, want)
	}
	if cfg.Providers["openai"].Class != "openai" {
		t.Errorf("providers.openai.class = %q, want openai", cfg.Providers["openai"].Class)
	}
	if cfg.Providers["openai"].APIKey.Key != "OPENAI_API_KEY" {
		t.Errorf("providers.openai.api_key.key = %q, want OPENAI_API_KEY", cfg.Providers["openai"].APIKey.Key)
	}
	if got, want := secrets.committed["env:OPENAI_API_KEY"], "sk-openai-token"; got != want {
		t.Errorf("committed openai key = %q, want %q", got, want)
	}
	for _, role := range []string{"triage", "planner", "builder"} {
		if cfg.Models[role] != "openai/gpt-5.4" {
			t.Errorf("models[%s] = %q, want openai/gpt-5.4", role, cfg.Models[role])
		}
	}
}

func TestRun_GiteaForge(t *testing.T) {
	p := &fakePrompter{
		lines:    []string{"acme-archie", "", "gpt-5.4", "https://git.acme.internal"},
		selects:  []int{0, 1}, // OpenAI, Gitea
		secrets:  []string{"sk-openai-token", "gitea-token"},
		confirms: []bool{false},
	}
	secrets := newFakeSecrets()
	edits, err := Run(context.Background(), p, nil, secrets, ExistingValues{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if err := secrets.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}

	cfg := generateAndLoad(t, edits)
	if cfg.Forge.Type != "gitea" {
		t.Errorf("forge.type = %q, want gitea", cfg.Forge.Type)
	}
	if cfg.Forge.Host != "https://git.acme.internal" {
		t.Errorf("forge.host = %q, want https://git.acme.internal", cfg.Forge.Host)
	}
	if cfg.Forge.Token.Key != "ARCHIE_GITEA_TOKEN" {
		t.Errorf("forge.token.key = %q, want ARCHIE_GITEA_TOKEN", cfg.Forge.Token.Key)
	}
	if got, want := secrets.committed["env:ARCHIE_GITEA_TOKEN"], "gitea-token"; got != want {
		t.Errorf("committed gitea token = %q, want %q", got, want)
	}
}

func TestRun_ForgeNone(t *testing.T) {
	p := &fakePrompter{
		lines:    []string{"acme-archie", "", "gpt-5.4"},
		selects:  []int{0, 2}, // OpenAI, None
		secrets:  []string{"sk-openai-token"},
		confirms: []bool{false},
	}
	secrets := newFakeSecrets()
	edits, err := Run(context.Background(), p, nil, secrets, ExistingValues{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if err := secrets.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}

	cfg := generateAndLoad(t, edits)
	if cfg.Forge.Type != "none" {
		t.Errorf("forge.type = %q, want none", cfg.Forge.Type)
	}
	if cfg.Forge.Token.Key != "" {
		t.Errorf("forge.token.key = %q, want empty: no token was ever collected for forge type none", cfg.Forge.Token.Key)
	}
	if len(secrets.committed) != 1 { // just the openai key
		t.Errorf("committed secrets = %v, want only the openai key", secrets.committed)
	}
}

type fakeDiscovery struct {
	models []string
	err    error
}

func (f *fakeDiscovery) ListOllamaModels(context.Context) ([]string, error) {
	return f.models, f.err
}

func TestRun_SelfHostedWithDiscoveredModels(t *testing.T) {
	p := &fakePrompter{
		lines:    []string{"acme-archie", ""},
		selects:  []int{len(cloudProviders), 0, 2}, // provider: self-hosted; ollama model list: index 0; forge: none
		confirms: []bool{false},
	}
	discovery := &fakeDiscovery{models: []string{"llama3", "qwen2.5:7b"}}
	secrets := newFakeSecrets()
	edits, err := Run(context.Background(), p, discovery, secrets, ExistingValues{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	cfg := generateAndLoad(t, edits)
	for _, role := range []string{"triage", "planner", "builder"} {
		if cfg.Models[role] != "ollama/llama3" {
			t.Errorf("models[%s] = %q, want ollama/llama3", role, cfg.Models[role])
		}
	}
	if cfg.Providers["ollama"].Class != "ollama" {
		t.Errorf("providers.ollama.class = %q, want ollama", cfg.Providers["ollama"].Class)
	}
	if len(secrets.committed) != 0 {
		t.Errorf("committed secrets = %v, want none: self-hosted needs no API key", secrets.committed)
	}
}

func TestRun_SelfHostedDiscoveryUnavailableFallsBackToFreeText(t *testing.T) {
	p := &fakePrompter{
		lines:    []string{"acme-archie", "", "custom-model:latest"},
		selects:  []int{len(cloudProviders), 2}, // self-hosted, forge none
		confirms: []bool{false},
	}
	discovery := &fakeDiscovery{err: fmt.Errorf("ollama not found")}
	secrets := newFakeSecrets()
	edits, err := Run(context.Background(), p, discovery, secrets, ExistingValues{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	cfg := generateAndLoad(t, edits)
	if cfg.Models["triage"] != "ollama/custom-model:latest" {
		t.Errorf("models[triage] = %q, want ollama/custom-model:latest", cfg.Models["triage"])
	}
}

func TestRun_TelegramRequiresAllowedUserIDs(t *testing.T) {
	p := &fakePrompter{
		lines:    []string{"acme-archie", "", "gpt-5.4", ""}, // last "" is the empty allowed-ids answer
		selects:  []int{0, 2},                                // openai, forge none
		secrets:  []string{"sk-openai-token", "tg-token"},
		confirms: []bool{true}, // yes, configure telegram
	}
	secrets := newFakeSecrets()
	_, err := Run(context.Background(), p, nil, secrets, ExistingValues{})
	if err == nil {
		t.Fatal("Run() = nil error, want a failure: telegram configured with no allowed user IDs answers nobody")
	}
	if secrets.committed["env:ARCHIE_TELEGRAM_TOKEN"] != "" {
		t.Error("telegram token must not be committed when the flow aborts on missing allowed user IDs")
	}
}

func TestRun_TelegramWithAllowedUserIDs(t *testing.T) {
	p := &fakePrompter{
		lines:    []string{"acme-archie", "", "gpt-5.4", "111, 222"},
		selects:  []int{0, 2},
		secrets:  []string{"sk-openai-token", "tg-token"},
		confirms: []bool{true},
	}
	secrets := newFakeSecrets()
	edits, err := Run(context.Background(), p, nil, secrets, ExistingValues{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if err := secrets.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	cfg := generateAndLoad(t, edits)
	if cfg.Chat.Telegram.TokenEnv != "ARCHIE_TELEGRAM_TOKEN" {
		t.Errorf("chat.telegram.token_env = %q, want ARCHIE_TELEGRAM_TOKEN", cfg.Chat.Telegram.TokenEnv)
	}
	if len(cfg.Chat.Telegram.AllowedUserIDs) != 2 || cfg.Chat.Telegram.AllowedUserIDs[0] != 111 || cfg.Chat.Telegram.AllowedUserIDs[1] != 222 {
		t.Errorf("chat.telegram.allowed_user_ids = %v, want [111 222]", cfg.Chat.Telegram.AllowedUserIDs)
	}
	if got, want := secrets.committed["env:ARCHIE_TELEGRAM_TOKEN"], "tg-token"; got != want {
		t.Errorf("committed telegram token = %q, want %q", got, want)
	}
}

// config.example.toml ships [providers.openai] active by default with an
// api_key pointing at the bws engine, which errors when the bws CLI isn't
// installed. cmd/archied/provider_secrets.go resolves every entry in
// cfg.Providers at startup, not just the ones [models] references, so an
// operator who picks any other provider must not be left with that entry
// still pointing at bws -- the daemon would refuse to boot on a config
// setup itself produced.
func TestRun_NonOpenAIProviderNeutralisesTemplateDefaultOpenAIKey(t *testing.T) {
	p := &fakePrompter{
		lines:    []string{"acme-archie", "", "claude-opus"},
		selects:  []int{1, 2}, // Anthropic, forge none
		secrets:  []string{"sk-anthropic-token"},
		confirms: []bool{false},
	}
	secrets := newFakeSecrets()
	edits, err := Run(context.Background(), p, nil, secrets, ExistingValues{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	cfg := generateAndLoad(t, edits)
	if cfg.Providers["openai"].APIKey != (secret.SecretRef{}) {
		t.Fatalf("providers.openai.api_key = %+v, want the zero value: it must not still point at the "+
			"template's default bws reference once a different provider is chosen, or the daemon refuses to boot",
			cfg.Providers["openai"].APIKey)
	}
}

func TestRun_OpenAISkippedKeyNeutralisesTemplateDefault(t *testing.T) {
	p := &fakePrompter{
		lines:    []string{"acme-archie", "", "gpt-5.4"},
		selects:  []int{0, 2}, // OpenAI, forge none
		secrets:  []string{""},
		confirms: []bool{false},
	}
	secrets := newFakeSecrets()
	edits, err := Run(context.Background(), p, nil, secrets, ExistingValues{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	cfg := generateAndLoad(t, edits)
	if cfg.Providers["openai"].APIKey != (secret.SecretRef{}) {
		t.Fatalf("providers.openai.api_key = %+v, want the zero value when the key prompt was left blank, "+
			"not the template's default bws reference", cfg.Providers["openai"].APIKey)
	}
}

// The drift-bug regression test: for every forge type that writes a token,
// the key decoded from the generated TOML must equal the key the secret was
// actually stored under -- not merely equal some hardcoded literal on the
// assertion side, which would pass even if both sides had independently
// drifted to the same wrong value.
func TestRun_ForgeTokenKeyMatchesStoredSecretKey(t *testing.T) {
	tests := []struct {
		name       string
		forgeIndex int
		lines      []string
		secret     string
	}{
		{name: "github", forgeIndex: 0, lines: []string{"acme-archie", "", "gpt-5.4"}, secret: "ghp-token"},
		{name: "gitea", forgeIndex: 1, lines: []string{"acme-archie", "", "gpt-5.4", "https://gitea.acme.internal"}, secret: "gitea-token"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := &fakePrompter{
				lines:    tc.lines,
				selects:  []int{0, tc.forgeIndex},
				secrets:  []string{"sk-openai-token", tc.secret},
				confirms: []bool{false},
			}
			secrets := newFakeSecrets()
			edits, err := Run(context.Background(), p, nil, secrets, ExistingValues{})
			if err != nil {
				t.Fatalf("Run: %v", err)
			}
			if err := secrets.Commit(); err != nil {
				t.Fatalf("Commit: %v", err)
			}
			cfg := generateAndLoad(t, edits)

			storedUnderKey := ""
			for k, v := range secrets.committed {
				if v == tc.secret {
					storedUnderKey = k
				}
			}
			if storedUnderKey == "" {
				t.Fatalf("secret %q was never committed: %v", tc.secret, secrets.committed)
			}
			wantKey := "env:" + cfg.Forge.Token.Key
			if storedUnderKey != wantKey {
				t.Errorf("forge.token.key decodes to %q, but the secret was stored under %q -- these must match", cfg.Forge.Token.Key, storedUnderKey)
			}
		})
	}
}

// A second run against an already-generated config, changing one answer,
// must not disturb the rest of the file -- tomlwrite's own guarantee
// (archie-core-rs9), exercised here through setup's edits specifically.
func TestRun_SecondRunAgainstOwnOutputChangesOnlyOneField(t *testing.T) {
	p1 := &fakePrompter{
		lines:    []string{"acme-archie", "", "gpt-5.4"},
		selects:  []int{0, 2},
		secrets:  []string{"sk-openai-token"},
		confirms: []bool{false},
	}
	secrets := newFakeSecrets()
	edits1, err := Run(context.Background(), p1, nil, secrets, ExistingValues{})
	if err != nil {
		t.Fatalf("first Run: %v", err)
	}
	first, err := tomlwrite.Generate(configtemplate.Example, edits1)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	p2 := &fakePrompter{
		lines:    []string{"acme-archie-renamed", "", "gpt-5.4"},
		selects:  []int{0, 2},
		secrets:  []string{"sk-openai-token"},
		confirms: []bool{false},
	}
	edits2, err := Run(context.Background(), p2, nil, secrets, ExistingValues{BotUser: "acme-archie"})
	if err != nil {
		t.Fatalf("second Run: %v", err)
	}
	second, err := tomlwrite.Apply(first, edits2)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	firstLines := splitLines(t, first)
	secondLines := splitLines(t, second)
	if len(firstLines) != len(secondLines) {
		t.Fatalf("line count changed: %d -> %d", len(firstLines), len(secondLines))
	}
	changed := 0
	for i := range firstLines {
		if firstLines[i] != secondLines[i] {
			changed++
			if firstLines[i] != `bot_user = "acme-archie"` {
				t.Errorf("unexpected line changed on re-run: %q -> %q", firstLines[i], secondLines[i])
			}
		}
	}
	if changed != 1 {
		t.Errorf("expected exactly one changed line (bot_user), got %d", changed)
	}
}

func splitLines(t *testing.T, b []byte) []string {
	t.Helper()
	var lines []string
	start := 0
	for i, c := range b {
		if c == '\n' {
			lines = append(lines, string(b[start:i]))
			start = i + 1
		}
	}
	if start < len(b) {
		lines = append(lines, string(b[start:]))
	}
	return lines
}
