// Feature-based config contract tests.
//
// These tests define the REQUIREMENTS for archie-core-abg.19. An
// implementing agent must make every test in this file pass WITHOUT
// modifying the tests themselves. The tests are the spec.
//
// Design:
//   - ~/.config/archie/config.yaml            --  daemon-level fields
//   - ~/.config/archie/config.gateway.yaml    --  chat channels, platforms
//   - ~/.config/archie/config.tools.yaml      --  MCP servers, tool policy
//   - ~/.config/archie/config.memory.yaml     --  memory provider config
//   - ~/.config/archie/config.models.yaml     --  LLM providers and models
//   - ~/.config/archie/config.identities.yaml  --  identity configs
//   - ~/.config/archie/conf.d/*.yaml           --  additional feature files (Linux convention)
//
// Missing files mean "feature disabled"  --  no error, just zero values.
// Legacy config.toml is still supported as a fallback.
// Overlay support: --config-overlay <dir> applies files from <dir>
// on top of the base config directory.
package configuration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/samcharles93/archie-core/internal/secret"
)

// helpers

// writeFile creates a file under dir with the given content.
func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

// tmpConfigDir creates a temp directory for config files.
func tmpConfigDir(t *testing.T) string {
	t.Helper()
	d := t.TempDir()
	// Also create conf.d/ subdirectory for additional feature files.
	if err := os.MkdirAll(filepath.Join(d, "conf.d"), 0o755); err != nil {
		t.Fatal(err)
	}
	return d
}

// ── 1. Daemon-level config ────────────────────────────────────────

func TestLoadDirLoadsDaemonConfig(t *testing.T) {
	dir := tmpConfigDir(t)
	writeFile(t, dir, "config.yaml", `
work_dir: /var/archie/work
db_path: /var/archie/data.db
poll_interval: 30s
max_retries: 5
bot_user: archie
`)

	cfg, err := loadDir(dir, "")
	if err != nil {
		t.Fatalf("LoadDir: %v", err)
	}
	if cfg.WorkDir != "/var/archie/work" {
		t.Errorf("WorkDir = %q", cfg.WorkDir)
	}
	if cfg.DBPath != "/var/archie/data.db" {
		t.Errorf("DBPath = %q", cfg.DBPath)
	}
	if cfg.PollInterval.Std() != 30*time.Second {
		t.Errorf("PollInterval = %v", cfg.PollInterval.Std())
	}
	if cfg.MaxRetries != 5 {
		t.Errorf("MaxRetries = %d", cfg.MaxRetries)
	}
	if cfg.BotUser != "archie" {
		t.Errorf("BotUser = %q", cfg.BotUser)
	}
}

func TestLoadDirAppliesDefaultsForMissingDaemonFields(t *testing.T) {
	dir := tmpConfigDir(t)
	writeFile(t, dir, "config.yaml", `bot_user: solo`)

	cfg, err := loadDir(dir, "")
	if err != nil {
		t.Fatalf("LoadDir: %v", err)
	}
	// Defaults from finalize() must still apply.
	if cfg.PollInterval.Std() != 60*time.Second {
		t.Errorf("PollInterval default = %v, want 60s", cfg.PollInterval.Std())
	}
	if cfg.WorkDir == "" {
		t.Error("WorkDir should have a default")
	}
}

func TestLoadDirRequiresBotUser(t *testing.T) {
	dir := tmpConfigDir(t)
	writeFile(t, dir, "config.yaml", `db_path: /tmp/db`)
	_, err := loadDir(dir, "")
	if err == nil || !strings.Contains(err.Error(), "bot_user") {
		t.Errorf("expected bot_user error, got: %v", err)
	}
}

// ── 2. Feature files are optional ─────────────────────────────────

func TestLoadDirMissingFeatureFilesSucceeds(t *testing.T) {
	dir := tmpConfigDir(t)
	// Only config.yaml exists  --  no gateway, tools, memory, models, identities files.
	writeFile(t, dir, "config.yaml", `bot_user: minimal`)

	cfg, err := loadDir(dir, "")
	if err != nil {
		t.Fatalf("LoadDir: %v", err)
	}
	// Gateway config should be zero-valued (feature disabled).
	if cfg.Chat.Telegram.TokenEnv != "" {
		t.Error("Telegram should be disabled when config.gateway.yaml is absent")
	}
	// Tools should be zero-valued.
	if len(cfg.Providers) != 0 {
		t.Errorf("Providers should be empty when config.models.yaml is absent, got %d", len(cfg.Providers))
	}
}

// ── 3. Gateway feature file ───────────────────────────────────────

func TestLoadDirGatewayConfig(t *testing.T) {
	dir := tmpConfigDir(t)
	writeFile(t, dir, "config.yaml", `bot_user: gwtest`)
	writeFile(t, dir, "config.gateway.yaml", `
chat:
  telegram:
    token_env: TELEGRAM_TOKEN
web:
  listen: "0.0.0.0:9090"
`)

	cfg, err := loadDir(dir, "")
	if err != nil {
		t.Fatalf("LoadDir: %v", err)
	}
	if cfg.Chat.Telegram.TokenEnv != "TELEGRAM_TOKEN" {
		t.Errorf("Telegram token_env = %q", cfg.Chat.Telegram.TokenEnv)
	}
	if cfg.Web.Listen != "0.0.0.0:9090" {
		t.Errorf("Web listen = %q", cfg.Web.Listen)
	}
}

// ── 4. Models feature file ────────────────────────────────────────

func TestLoadDirModelsConfig(t *testing.T) {
	dir := tmpConfigDir(t)
	writeFile(t, dir, "config.yaml", `bot_user: modeltest`)
	writeFile(t, dir, "config.models.yaml", `
models:
  triage: deepseek/deepseek-v4
  planner: anthropic/claude-sonnet
  builder: anthropic/claude-sonnet
providers:
  deepseek:
    class: deepseek
    api_key_env: DEEPSEEK_KEY
  anthropic:
    class: anthropic
    api_key_env: ANTHROPIC_KEY
`)

	cfg, err := loadDir(dir, "")
	if err != nil {
		t.Fatalf("LoadDir: %v", err)
	}
	if cfg.Models["triage"] != "deepseek/deepseek-v4" {
		t.Errorf("triage model = %q", cfg.Models["triage"])
	}
	if cfg.Models["planner"] != "anthropic/claude-sonnet" {
		t.Errorf("planner model = %q", cfg.Models["planner"])
	}
	if len(cfg.Providers) != 2 {
		t.Fatalf("expected 2 providers, got %d", len(cfg.Providers))
	}
	ds, ok := cfg.Providers["deepseek"]
	if !ok || ds.Class != "deepseek" || ds.APIKeyEnv != "DEEPSEEK_KEY" {
		t.Errorf("deepseek provider = %+v", ds)
	}
}

// ── 5. Memory feature file ────────────────────────────────────────

func TestLoadDirMemoryConfig(t *testing.T) {
	dir := tmpConfigDir(t)
	writeFile(t, dir, "config.yaml", `bot_user: memtest`)
	writeFile(t, dir, "config.memory.yaml", `
provider: honcho
provider_config:
  api_url: https://honcho.example.test
  api_key_env: HONCHO_KEY
session_ttl: 72h
`)

	cfg, err := loadDir(dir, "")
	if err != nil {
		t.Fatalf("LoadDir: %v", err)
	}
	// Memory config is exposed via a structured field.
	if cfg.Memory.Provider != "honcho" {
		t.Errorf("Memory provider = %q", cfg.Memory.Provider)
	}
	if cfg.Memory.ProviderConfig["api_url"] != "https://honcho.example.test" {
		t.Errorf("Memory api_url = %q", cfg.Memory.ProviderConfig["api_url"])
	}
	if cfg.Memory.SessionTTL.Std() != 72*time.Hour {
		t.Errorf("Memory session_ttl = %v", cfg.Memory.SessionTTL.Std())
	}
}

// ── 6. Tools feature file ─────────────────────────────────────────

func TestLoadDirToolsConfig(t *testing.T) {
	dir := tmpConfigDir(t)
	writeFile(t, dir, "config.yaml", `bot_user: tooltest`)
	writeFile(t, dir, "config.tools.yaml", `
mcp_servers:
  - name: filesystem
    transport: stdio
    command: npx
    args: ["-y", "@modelcontextprotocol/server-filesystem", "/tmp"]
  - name: web-search
    transport: http
    url: https://search-mcp.example.test
tool_policy:
  max_result_chars: 100000
  parallel_execution: true
`)

	cfg, err := loadDir(dir, "")
	if err != nil {
		t.Fatalf("LoadDir: %v", err)
	}
	if len(cfg.Tools.MCPServers) != 2 {
		t.Fatalf("expected 2 MCP servers, got %d", len(cfg.Tools.MCPServers))
	}
	if cfg.Tools.MCPServers[0].Name != "filesystem" {
		t.Errorf("server[0].name = %q", cfg.Tools.MCPServers[0].Name)
	}
	if cfg.Tools.MCPServers[0].Transport != "stdio" {
		t.Errorf("server[0].transport = %q", cfg.Tools.MCPServers[0].Transport)
	}
	if cfg.Tools.MCPServers[1].Transport != "http" {
		t.Errorf("server[1].transport = %q", cfg.Tools.MCPServers[1].Transport)
	}
	if cfg.Tools.Policy.MaxResultChars != 100000 {
		t.Errorf("max_result_chars = %d", cfg.Tools.Policy.MaxResultChars)
	}
	if !cfg.Tools.Policy.ParallelExecution {
		t.Error("parallel_execution should be true")
	}
}

// ── 7. Identities feature file ────────────────────────────────────

func TestLoadDirIdentitiesConfig(t *testing.T) {
	dir := tmpConfigDir(t)
	writeFile(t, dir, "config.yaml", `bot_user: idtest`)
	writeFile(t, dir, "config.identities.yaml", `
identities:
  - name: primary
    bot_user: archie
    forge:
      type: gitea
      host: https://git.example.test
      token:
        engine: env
        key: ARCHIE_TOKEN
    repos:
      - owner: sam
        name: archie-core
        ecosystem: go
  - name: secondary
    bot_user: winter
    forge:
      type: gitea
      host: https://git.example.test
      token:
        engine: env
        key: WINTER_TOKEN
    repos:
      - owner: sam
        name: example-service
        ecosystem: go
`)

	cfg, err := loadDir(dir, "")
	if err != nil {
		t.Fatalf("LoadDir: %v", err)
	}
	if len(cfg.Identities) != 2 {
		t.Fatalf("expected 2 identities, got %d", len(cfg.Identities))
	}
	if cfg.Identities[0].Name != "primary" {
		t.Errorf("identities[0].name = %q", cfg.Identities[0].Name)
	}
	if cfg.Identities[1].Name != "secondary" {
		t.Errorf("identities[1].name = %q", cfg.Identities[1].Name)
	}
	if cfg.Identities[0].Forge.Token != (secret.SecretRef{Engine: "env", Key: "ARCHIE_TOKEN"}) {
		t.Errorf("identities[0].forge.token = %#v", cfg.Identities[0].Forge.Token)
	}
}

// ── 8. conf.d/ additional feature files ─────────────────────────

func TestLoadDirConfigDExtraFeatures(t *testing.T) {
	dir := tmpConfigDir(t)
	writeFile(t, dir, "config.yaml", `bot_user: extratest`)
	// A third-party plugin could drop its config here.
	writeFile(t, dir, "conf.d/custom-tool.yaml", `
custom_tools:
  - name: my-analyzer
    command: /usr/local/bin/analyzer
`)

	cfg, err := loadDir(dir, "")
	if err != nil {
		t.Fatalf("LoadDir: %v", err)
	}
	// Extra features are accessible via a generic key-value map.
	if cfg.Extra == nil {
		t.Fatal("Extra should not be nil when conf.d/ files are present")
	}
	custom, ok := cfg.Extra["custom-tool"]
	if !ok {
		t.Fatal("Extra should contain 'custom-tool' key from conf.d/custom-tool.yaml")
	}
	if customMap, ok := custom.(map[string]any); ok {
		tools, ok := customMap["custom_tools"].([]any)
		if !ok || len(tools) != 1 {
			t.Errorf("custom_tools = %v", customMap["custom_tools"])
		}
	} else {
		t.Errorf("extra value should be map[string]any, got %T", custom)
	}
}

// ── 9. Backward compatibility: legacy config.toml ─────────────────

func TestLoadDirFallsBackToLegacyToml(t *testing.T) {
	dir := tmpConfigDir(t)
	// No config.yaml  --  only the legacy config.toml exists.
	writeFile(t, dir, "config.toml", `
bot_user = "legacy"
poll_interval = "90s"

[forge]
type = "github"
host = "https://github.test"
token_env = "GH_TOKEN"

[[repos]]
owner = "acme"
name = "legacy-repo"
`)

	cfg, err := loadDir(dir, "")
	if err != nil {
		t.Fatalf("LoadDir: %v", err)
	}
	if cfg.BotUser != "legacy" {
		t.Errorf("BotUser = %q", cfg.BotUser)
	}
	if cfg.Forge.Type != "github" {
		t.Errorf("Forge.Type = %q", cfg.Forge.Type)
	}
	if len(cfg.Repos) != 1 || cfg.Repos[0].Name != "legacy-repo" {
		t.Errorf("Repos = %+v", cfg.Repos)
	}
}

func TestLoadDirYamlTakesPrecedenceOverToml(t *testing.T) {
	dir := tmpConfigDir(t)
	// Both exist  --  YAML wins.
	writeFile(t, dir, "config.yaml", `bot_user: yaml-wins`)
	writeFile(t, dir, "config.toml", `bot_user = "toml-loses"`)

	cfg, err := loadDir(dir, "")
	if err != nil {
		t.Fatalf("LoadDir: %v", err)
	}
	if cfg.BotUser != "yaml-wins" {
		t.Errorf("BotUser = %q, want yaml-wins (YAML must take precedence)", cfg.BotUser)
	}
}

// ── 10. Overlay support ────────────────────────────────────────────

func TestLoadDirOverlay(t *testing.T) {
	baseDir := tmpConfigDir(t)
	writeFile(t, baseDir, "config.yaml", `
bot_user: base-bot
work_dir: /base/work
max_retries: 3
`)
	writeFile(t, baseDir, "config.models.yaml", `
models:
  triage: base/model
`)

	overlayDir := tmpConfigDir(t)
	// Overlay only needs to specify what differs.
	writeFile(t, overlayDir, "config.yaml", `
bot_user: overlay-bot
`)
	writeFile(t, overlayDir, "config.models.yaml", `
models:
  triage: overlay/model
  planner: overlay/planner
`)

	cfg, err := loadDir(baseDir, overlayDir)
	if err != nil {
		t.Fatalf("LoadDir: %v", err)
	}
	// Overridden by overlay.
	if cfg.BotUser != "overlay-bot" {
		t.Errorf("BotUser = %q, want overlay-bot", cfg.BotUser)
	}
	// Inherited from base (not mentioned in overlay).
	if cfg.WorkDir != "/base/work" {
		t.Errorf("WorkDir = %q, want /base/work (inherited from base)", cfg.WorkDir)
	}
	if cfg.MaxRetries != 3 {
		t.Errorf("MaxRetries = %d, want 3 (inherited from base)", cfg.MaxRetries)
	}
	// Model from overlay takes precedence; model only in overlay is merged.
	if cfg.Models["triage"] != "overlay/model" {
		t.Errorf("triage = %q, want overlay/model", cfg.Models["triage"])
	}
	if cfg.Models["planner"] != "overlay/planner" {
		t.Errorf("planner = %q, want overlay/planner (new key from overlay)", cfg.Models["planner"])
	}
}

func TestLoadDirOverlayFeatureFileOnlyInOverlay(t *testing.T) {
	baseDir := tmpConfigDir(t)
	writeFile(t, baseDir, "config.yaml", `bot_user: base-only`)

	overlayDir := tmpConfigDir(t)
	// Overlay adds a feature file not present in base.
	writeFile(t, overlayDir, "config.yaml", `bot_user: override-bot`)
	writeFile(t, overlayDir, "config.gateway.yaml", `
chat:
  telegram:
    token_env: OVERLAY_TG_TOKEN
`)

	cfg, err := loadDir(baseDir, overlayDir)
	if err != nil {
		t.Fatalf("LoadDir: %v", err)
	}
	if cfg.Chat.Telegram.TokenEnv != "OVERLAY_TG_TOKEN" {
		t.Errorf("Telegram token_env = %q, want OVERLAY_TG_TOKEN (feature file from overlay)", cfg.Chat.Telegram.TokenEnv)
	}
}

func TestLoadDirOverlayEmptyDirIsNoop(t *testing.T) {
	baseDir := tmpConfigDir(t)
	writeFile(t, baseDir, "config.yaml", `bot_user: base-only`)

	overlayDir := tmpConfigDir(t)
	// Empty overlay directory  --  same as calling loadDir(baseDir, "").

	cfg, err := loadDir(baseDir, overlayDir)
	if err != nil {
		t.Fatalf("LoadDir: %v", err)
	}
	if cfg.BotUser != "base-only" {
		t.Errorf("BotUser = %q, want base-only", cfg.BotUser)
	}
}

// ── 11. Validation ─────────────────────────────────────────────────

func TestLoadDirRejectsInvalidFeatureFile(t *testing.T) {
	dir := tmpConfigDir(t)
	writeFile(t, dir, "config.yaml", `bot_user: valid`)
	writeFile(t, dir, "config.models.yaml", `this: [is: not: valid: yaml: }}`)

	_, err := loadDir(dir, "")
	if err == nil {
		t.Fatal("expected error for malformed feature file")
	}
}

func TestLoadDirRejectsUnknownFeatureFile(t *testing.T) {
	dir := tmpConfigDir(t)
	writeFile(t, dir, "config.yaml", `bot_user: valid`)
	// config.unknown.yaml is not a known feature  --  should error, not silently ignore.
	writeFile(t, dir, "config.unknown.yaml", `foo: bar`)

	_, err := loadDir(dir, "")
	if err == nil {
		t.Fatal("expected error for unknown feature file (silent ignore would hide typos)")
	}
	if !strings.Contains(err.Error(), "unknown") && !strings.Contains(err.Error(), "unrecognized") {
		t.Errorf("error should mention unknown/unrecognized file, got: %v", err)
	}
}

func TestLoadDirRejectsDuplicateFeatureFiles(t *testing.T) {
	dir := tmpConfigDir(t)
	writeFile(t, dir, "config.yaml", `bot_user: dupetest`)
	// Two files claiming the same feature.
	writeFile(t, dir, "config.gateway.yaml", `chat: {telegram: {token_env: FIRST}}`)
	writeFile(t, dir, "conf.d/gateway.yaml", `chat: {telegram: {token_env: SECOND}}`)

	_, err := loadDir(dir, "")
	if err == nil {
		t.Fatal("expected error for duplicate feature files")
	}
	if !strings.Contains(err.Error(), "duplicate") && !strings.Contains(err.Error(), "gateway") {
		t.Errorf("error should mention duplicate and the feature name, got: %v", err)
	}
}

func TestLoadDirRequiresConfigYamlOrConfigToml(t *testing.T) {
	dir := tmpConfigDir(t)
	// Neither config.yaml nor config.toml exists.
	_, err := loadDir(dir, "")
	if err == nil || !strings.Contains(err.Error(), "config.yaml") && !strings.Contains(err.Error(), "config.toml") {
		t.Errorf("expected error about missing config.yaml or config.toml, got: %v", err)
	}
}

// ── 12. Agent config (still supported) ────────────────────────────

func TestLoadDirAgentConfig(t *testing.T) {
	dir := tmpConfigDir(t)
	writeFile(t, dir, "config.yaml", `
bot_user: agenttest
agent:
  mode: subprocess
  command: /usr/local/bin/archie-agent
  env:
    - DEBUG
    - NO_COLOR
budgets:
  max_steps: 60
  max_tokens: 500000
  wall_clock: 30m
`)

	cfg, err := loadDir(dir, "")
	if err != nil {
		t.Fatalf("LoadDir: %v", err)
	}
	if cfg.Agent.Mode != "subprocess" {
		t.Errorf("Agent.Mode = %q", cfg.Agent.Mode)
	}
	if cfg.Agent.Command != "/usr/local/bin/archie-agent" {
		t.Errorf("Agent.Command = %q", cfg.Agent.Command)
	}
	if cfg.Budgets.MaxSteps != 60 {
		t.Errorf("Budgets.MaxSteps = %d", cfg.Budgets.MaxSteps)
	}
}

// ── 13. NATS and Containers (still in daemon config) ──────────────

func TestLoadDirNATSAndContainersConfig(t *testing.T) {
	dir := tmpConfigDir(t)
	writeFile(t, dir, "config.yaml", `
bot_user: infra
nats:
  url: nats://nats.example:4222
  token_env: NATS_TOKEN
containers:
  enabled: true
  image: ghcr.io/sam/archie-agent:latest
  max_concurrency: 4
  pull_policy: always
`)

	cfg, err := loadDir(dir, "")
	if err != nil {
		t.Fatalf("LoadDir: %v", err)
	}
	if cfg.NATS.URL != "nats://nats.example:4222" {
		t.Errorf("NATS.URL = %q", cfg.NATS.URL)
	}
	if !cfg.Containers.Enabled {
		t.Error("Containers should be enabled")
	}
	if cfg.Containers.Image != "ghcr.io/sam/archie-agent:latest" {
		t.Errorf("Containers.Image = %q", cfg.Containers.Image)
	}
}

// ── 14. Dispatch config ─────────────────────────────────────────────

func TestLoadDirDispatchConfig(t *testing.T) {
	dir := tmpConfigDir(t)
	writeFile(t, dir, "config.yaml", `
bot_user: dispatchtest
dispatch:
  trigger: label
  ack_reaction: rock
  labels:
    queued: bot:queued
    working: bot:working
    pr: bot:pr
`)

	cfg, err := loadDir(dir, "")
	if err != nil {
		t.Fatalf("LoadDir: %v", err)
	}
	if cfg.Dispatch.Trigger != "label" {
		t.Errorf("Dispatch.Trigger = %q", cfg.Dispatch.Trigger)
	}
	if cfg.Dispatch.AckReaction != "rock" {
		t.Errorf("Dispatch.AckReaction = %q", cfg.Dispatch.AckReaction)
	}
	if cfg.Dispatch.Labels["queued"] != "bot:queued" {
		t.Errorf("Dispatch.Labels[queued] = %q", cfg.Dispatch.Labels["queued"])
	}
}

// ── 15. Notify config ───────────────────────────────────────────────

func TestLoadDirNotifyConfig(t *testing.T) {
	dir := tmpConfigDir(t)
	writeFile(t, dir, "config.yaml", `
bot_user: notifytest
notify:
  webhook: https://hooks.example.test/archie
`)

	cfg, err := loadDir(dir, "")
	if err != nil {
		t.Fatalf("LoadDir: %v", err)
	}
	if cfg.Notify.Webhook != "https://hooks.example.test/archie" {
		t.Errorf("Notify.Webhook = %q", cfg.Notify.Webhook)
	}
}

// ── 16. DiffCapLines config ─────────────────────────────────────────

func TestLoadDirDiffCapLinesConfig(t *testing.T) {
	dir := tmpConfigDir(t)
	writeFile(t, dir, "config.yaml", `
bot_user: difftest
diff_cap_lines: 200
`)

	cfg, err := loadDir(dir, "")
	if err != nil {
		t.Fatalf("LoadDir: %v", err)
	}
	if cfg.DiffCapLines != 200 {
		t.Errorf("DiffCapLines = %d, want 200", cfg.DiffCapLines)
	}
}

// ── 17. Skills and Plugin dirs ──────────────────────────────────────

func TestLoadDirSkillsAndPluginDirs(t *testing.T) {
	dir := tmpConfigDir(t)
	writeFile(t, dir, "config.yaml", `
bot_user: skillstest
skills_dir: /opt/archie/skills
plugin_dir: /opt/archie/plugins
`)

	cfg, err := loadDir(dir, "")
	if err != nil {
		t.Fatalf("LoadDir: %v", err)
	}
	if cfg.SkillsDir != "/opt/archie/skills" {
		t.Errorf("SkillsDir = %q", cfg.SkillsDir)
	}
	if cfg.PluginDir != "/opt/archie/plugins" {
		t.Errorf("PluginDir = %q", cfg.PluginDir)
	}
}

// ── 18. API stability: ForTask must still work ─────────────────────

func TestLoadDirForTaskRoundTrip(t *testing.T) {
	dir := tmpConfigDir(t)
	writeFile(t, dir, "config.yaml", `
bot_user: tasktest
diff_cap_lines: 321
`)
	writeFile(t, dir, "config.models.yaml", `
models:
  builder: anthropic/claude
  planner: openai/gpt
`)

	cfg, err := loadDir(dir, "")
	if err != nil {
		t.Fatalf("LoadDir: %v", err)
	}

	taskCfg := cfg.ForTask()
	// ForTask must still carry the merged models.
	if taskCfg.Models["builder"] != "anthropic/claude" {
		t.Errorf("ForTask models[builder] = %q", taskCfg.Models["builder"])
	}
	if taskCfg.Models["planner"] != "openai/gpt" {
		t.Errorf("ForTask models[planner] = %q", taskCfg.Models["planner"])
	}
	if taskCfg.DiffCapLines != 321 {
		t.Errorf("ForTask DiffCapLines = %d", taskCfg.DiffCapLines)
	}
	// ForTask must NOT carry secret references.
	roundTripped := taskCfg.ToConfig()
	if roundTripped.Forge.Token != (secret.SecretRef{}) {
		t.Error("ForTask must not leak Forge.Token")
	}
}

// ── 19. Existing Load function must be preserved ────────────────────

func TestLoadFunctionStillWorks(t *testing.T) {
	dir := tmpConfigDir(t)
	writeFile(t, dir, "config.toml", `
bot_user = "oldstyle"
poll_interval = "120s"

[forge]
type = "github"
token = { engine = "env", key = "GH_TOKEN" }

[[repos]]
owner = "acme"
name = "old-repo"
`)

	cfg, err := loadFile(filepath.Join(dir, "config.toml"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.BotUser != "oldstyle" {
		t.Errorf("BotUser = %q", cfg.BotUser)
	}
}

func TestLoadOverlayFunctionStillWorks(t *testing.T) {
	baseDir := tmpConfigDir(t)
	overlayDir := tmpConfigDir(t)
	writeFile(t, baseDir, "config.toml", `
bot_user = "base"
max_retries = 2
[forge]
type = "github"
token = { engine = "env", key = "BASE_TOKEN" }
[[repos]]
owner = "acme"
name = "base-repo"
`)
	writeFile(t, overlayDir, "config.toml", `
bot_user = "overlaid"
max_retries = 5
`)

	cfg, err := loadOverlay(
		filepath.Join(baseDir, "config.toml"),
		filepath.Join(overlayDir, "config.toml"),
	)
	if err != nil {
		t.Fatalf("LoadOverlay: %v", err)
	}
	if cfg.BotUser != "overlaid" {
		t.Errorf("BotUser = %q, want overlaid", cfg.BotUser)
	}
	if cfg.MaxRetries != 5 {
		t.Errorf("MaxRetries = %d, want 5 (overridden by overlay)", cfg.MaxRetries)
	}
	// Inherited from base.
	if cfg.Forge.Token != (secret.SecretRef{Engine: "env", Key: "BASE_TOKEN"}) {
		t.Errorf("Forge.Token = %#v, want {env BASE_TOKEN} (inherited)", cfg.Forge.Token)
	}
}
