package configuration

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/secret"
)

func TestLoadDispatchAckReaction(t *testing.T) {
	tests := []struct {
		name     string
		dispatch string
		wantAck  string
	}{
		{name: "default", wantAck: "eyes"},
		{name: "disabled", dispatch: "\n[dispatch]\nack_reaction = \"off\"\n", wantAck: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.toml")
			contents := "bot_user = \"widget\"\n" + tt.dispatch + "\n[[repos]]\nowner = \"acme\"\nname = \"app\"\n"
			if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
				t.Fatal(err)
			}

			cfg, err := loadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if cfg.Dispatch.AckReaction != tt.wantAck {
				t.Errorf("AckReaction: got %q, want %q", cfg.Dispatch.AckReaction, tt.wantAck)
			}
		})
	}
}

func TestLegacyContainersEnabledCannotSelectExecutionTopology(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(map[bool]string{false: "disabled", true: "enabled"}[enabled], func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.toml")
			contents := fmt.Sprintf("bot_user = \"widget\"\n[forge]\ntype = \"none\"\n[containers]\nenabled = %t\n", enabled)
			if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
				t.Fatal(err)
			}

			doc, err := New(nil).File(path)
			if err != nil {
				t.Fatal(err)
			}
			if doc.Config.Containers.LegacyEnabled != enabled {
				t.Errorf("LegacyEnabled = %v, want decoded %v", doc.Config.Containers.LegacyEnabled, enabled)
			}
			if doc.Config.Containers.Image != defaultContainerImage {
				t.Errorf("Image = %q, want mandatory default %q", doc.Config.Containers.Image, defaultContainerImage)
			}
		})
	}
}

func TestLoadExpandsConfiguredHomePaths(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(t.TempDir(), "config.toml")
	contents := `
bot_user = "widget"
work_dir = "~/archie/work"
db_path = "~/archie/archie.db"

[chat]
workspace = "~/archie/workspace"

[[repos]]
owner = "acme"
name = "app"
`
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := loadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	wants := map[string]struct {
		got  string
		want string
	}{
		"work_dir":       {got: cfg.WorkDir, want: filepath.Join(home, "archie", "work")},
		"db_path":        {got: cfg.DBPath, want: filepath.Join(home, "archie", "archie.db")},
		"chat.workspace": {got: cfg.Chat.Workspace, want: filepath.Join(home, "archie", "workspace")},
	}
	for name, tt := range wants {
		t.Run(name, func(t *testing.T) {
			if tt.got != tt.want {
				t.Errorf("got %q, want %q", tt.got, tt.want)
			}
		})
	}
}

func TestResolveSelectsFileFormatsAndDirectories(t *testing.T) {
	tests := []struct {
		name     string
		prepare  func(t *testing.T, dir string) string
		wantUser string
		wantPath string
	}{
		{
			name: "toml file",
			prepare: func(t *testing.T, dir string) string {
				path := filepath.Join(dir, "config.toml")
				if err := os.WriteFile(path, []byte("bot_user = \"toml-bot\"\n[[repos]]\nowner = \"acme\"\nname = \"app\"\n"), 0o600); err != nil {
					t.Fatal(err)
				}
				return path
			},
			wantUser: "toml-bot", wantPath: "config.toml",
		},
		{
			name: "yaml file",
			prepare: func(t *testing.T, dir string) string {
				path := filepath.Join(dir, "config.yaml")
				if err := os.WriteFile(path, []byte("bot_user: yaml-bot\nrepos:\n  - owner: acme\n    name: app\n"), 0o600); err != nil {
					t.Fatal(err)
				}
				return path
			},
			wantUser: "yaml-bot", wantPath: "config.yaml",
		},
		{
			name: "directory",
			prepare: func(t *testing.T, dir string) string {
				if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("bot_user: directory-bot\nrepos:\n  - owner: acme\n    name: app\n"), 0o600); err != nil {
					t.Fatal(err)
				}
				return dir
			},
			wantUser: "directory-bot", wantPath: "config.yaml",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			doc, err := New(nil).Resolve(tt.prepare(t, dir), "")
			if err != nil {
				t.Fatal(err)
			}
			if doc.Config.BotUser != tt.wantUser {
				t.Errorf("BotUser = %q, want %q", doc.Config.BotUser, tt.wantUser)
			}
			if got := filepath.Base(doc.Provenance.Paths()[0]); got != tt.wantPath {
				t.Errorf("provenance path = %q, want %q", got, tt.wantPath)
			}
		})
	}
}

// TestLoadForgeTokenEnvBackwardCompat guards against a real production
// incident: the secrets-engine migration replaced [forge]'s flat
// token_env string with a {engine, key} struct, but TOML silently ignores
// unknown fields — so deployed configs still using the old token_env key
// had their token config dropped entirely and finalize() defaulted to
// demanding ARCHIE_GITHUB_TOKEN, crash-looping a gitea-backed daemon.
func TestLoadForgeTokenEnvBackwardCompat(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	contents := "bot_user = \"widget\"\n" +
		"[forge]\ntype = \"gitea\"\nhost = \"https://git.example.test\"\ntoken_env = \"MY_GITEA_TOKEN\"\n" +
		"[[repos]]\nowner = \"acme\"\nname = \"app\"\n"
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := loadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Forge.Token != (secret.SecretRef{Engine: "env", Key: "MY_GITEA_TOKEN"}) {
		t.Errorf("Forge.Token = %#v, want {env MY_GITEA_TOKEN} (from legacy token_env)", cfg.Forge.Token)
	}
}

// TestLoadForgeTokenTakesPrecedenceOverTokenEnv verifies the new-style
// [forge.token] wins when both the legacy token_env and the new token
// struct are present (e.g. mid-migration configs).
func TestLoadForgeTokenTakesPrecedenceOverTokenEnv(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	contents := "bot_user = \"widget\"\n" +
		"[forge]\ntype = \"gitea\"\ntoken_env = \"OLD_TOKEN\"\n" +
		"[forge.token]\nengine = \"env\"\nkey = \"NEW_TOKEN\"\n" +
		"[[repos]]\nowner = \"acme\"\nname = \"app\"\n"
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := loadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Forge.Token != (secret.SecretRef{Engine: "env", Key: "NEW_TOKEN"}) {
		t.Errorf("Forge.Token = %#v, want {env NEW_TOKEN} (new-style token wins)", cfg.Forge.Token)
	}
}

func TestLoadTelegramTokenSecretRefAndLegacyFallback(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	contents := "bot_user = \"widget\"\n" +
		"[chat.telegram]\ntoken = { engine = \"bws\", key = \"TELEGRAM_BOT_TOKEN\" }\n" +
		"token_env = \"TELEGRAM_LEGACY_TOKEN\"\n" +
		"[[repos]]\nowner = \"acme\"\nname = \"app\"\n"
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := loadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := secret.SecretRef{Engine: "bws", Key: "TELEGRAM_BOT_TOKEN"}
	if cfg.Chat.Telegram.Token != want {
		t.Errorf("Telegram.Token = %#v, want %#v", cfg.Chat.Telegram.Token, want)
	}
	if cfg.Chat.Telegram.TokenEnv != "TELEGRAM_LEGACY_TOKEN" {
		t.Errorf("Telegram.TokenEnv = %q, want TELEGRAM_LEGACY_TOKEN", cfg.Chat.Telegram.TokenEnv)
	}
}

// TestLoadRejectsInvalidConfigEnumsAndGlobs pins which layer rejects each kind
// of invalid value. Every file source produces a BOOTSTRAP document, and the
// settings the control plane owns are layered over it later; a stale TOML value
// in one of those must not fail the load (docs/prds/runtime-control-plane.md,
// "Bootstrap, migration, and recovery"). So a check over a database-owned
// setting is asserted with Validate instead -- what boot.runtimeConfig runs over
// the document a process will use, and the checks the loader no longer applies.
// Each case asserts both halves it applies to, so a check that quietly
// disappeared fails the case rather than passing it.
func TestLoadRejectsInvalidConfigEnumsAndGlobs(t *testing.T) {
	tests := []struct {
		name        string
		body        string
		wantLoadErr bool
	}{
		{
			name: "dispatch trigger",
			body: fileConfigPrefix + "[dispatch]\ntrigger = \"labels\"\n",
		},
		{
			name: "provider userinfo",
			body: fileConfigPrefix + "[providers.openai]\nclass = \"openai\"\nbase_url = \"https://token@example.com/v1\"\n",
		},
		{
			name: "provider query secret",
			body: fileConfigPrefix + "[providers.openai]\nclass = \"openai\"\nbase_url = \"https://example.com/v1?api_key=secret\"\n",
		},
		{
			name: "test glob",
			body: fileConfigPrefix + "[[repos]]\nowner = \"acme\"\nname = \"app\"\ntest_glob = \"[\"\n",
		},
		{
			// File-owned settings: no control-plane resource carries them, so
			// the load path is the layer that has to reject them.
			name:        "forge intake",
			body:        fileConfigPrefix + "[forge]\nintake = \"not-a-real-intake\"\n",
			wantLoadErr: true,
		},
		{
			name:        "memory engine",
			body:        fileConfigPrefix + "[memory]\nengine = \"not-a-real-engine\"\n",
			wantLoadErr: true,
		},
		{
			name:        "negative capture retention",
			body:        fileConfigPrefix + "[capture]\nretention = \"-1h\"\n",
			wantLoadErr: true,
		},
		{
			name:        "image default naming no provider",
			body:        fileConfigPrefix + "[image]\ndefault = \"not-a-real-provider\"\n",
			wantLoadErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.toml")
			if err := os.WriteFile(path, []byte(tt.body), 0o600); err != nil {
				t.Fatal(err)
			}

			doc, err := New(nil).File(path)
			if tt.wantLoadErr {
				if err == nil {
					t.Fatal("File() = nil error, want the bootstrap document rejected")
				}
				return
			}
			if err != nil {
				t.Fatalf("File: %v (a database-owned value must not fail the load)", err)
			}
			if err := Validate(&doc.Config); err == nil {
				t.Fatal("Validate = nil, want the value rejected there")
			}
		})
	}
}

func TestLoadLegacyAgentSectionWithoutApplyingExecutionDefaults(t *testing.T) {
	tests := []struct {
		name        string
		agent       string
		wantMode    string
		wantCommand string
		wantEnv     []string
	}{
		{name: "absent section stays empty"},
		{
			name: "legacy values decode without validation", agent: "\n[agent]\nmode = \"removed-mode\"\ncommand = \"/opt/old-agent\"\nenv = [\"TOKEN=value\"]\n",
			wantMode: "removed-mode", wantCommand: "/opt/old-agent", wantEnv: []string{"TOKEN=value"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.toml")
			contents := "bot_user = \"widget\"\n" + tt.agent + "\n[[repos]]\nowner = \"acme\"\nname = \"app\"\n"
			if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
				t.Fatal(err)
			}
			cfg, err := loadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if cfg.LegacyAgent.Mode != tt.wantMode || cfg.LegacyAgent.Command != tt.wantCommand || strings.Join(cfg.LegacyAgent.Env, ",") != strings.Join(tt.wantEnv, ",") {
				t.Fatalf("legacy agent config = %#v", cfg.LegacyAgent)
			}
		})
	}
}

// TestLoadToolPolicyDefaultsAndOverrides covers the result-size and turn-budget
// limits end to end from TOML. Before these fields carried toml tags they could
// not be set from the daemon's config at all, so a test that only exercised
// defaulting would have passed against a setting no operator could reach.
//
// A negative value means "disabled" rather than zero: defaulting cannot tell an
// explicit 0 from an absent key, so zero has to stay available as "unset".
func TestLoadToolPolicyDefaultsAndOverrides(t *testing.T) {
	tests := []struct {
		name          string
		policy        string
		wantMaxResult int
		wantSpillDir  string
	}{
		{
			// SpillDir has no default on purpose: a path outside
			// chat.workspace is one the read tool refuses, so the model would
			// be handed a reference it cannot open. Absent means truncate
			// inline.
			name:          "defaults",
			wantMaxResult: 50_000,

			wantSpillDir: "",
		},
		{
			name:          "explicit values",
			policy:        "[tools.tool_policy]\nmax_result_chars = 1234\nspill_dir = \"/var/spool/archie\"\n",
			wantMaxResult: 1234,
			wantSpillDir:  "/var/spool/archie",
		},
		{
			name:          "negative disables the per-result cap",
			policy:        "[tools.tool_policy]\nmax_result_chars = -1\n",
			wantMaxResult: -1,
			wantSpillDir:  "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.toml")
			contents := "bot_user = \"widget\"\n" + tt.policy +
				"\n[[repos]]\nowner = \"acme\"\nname = \"app\"\n"
			if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
				t.Fatal(err)
			}

			cfg, err := loadFile(path)
			if err != nil {
				t.Fatal(err)
			}

			policy := cfg.Tools.Policy
			if policy.MaxResultChars != tt.wantMaxResult {
				t.Errorf("MaxResultChars = %d, want %d", policy.MaxResultChars, tt.wantMaxResult)
			}

			if policy.SpillDir != tt.wantSpillDir {
				t.Errorf("SpillDir = %q, want %q", policy.SpillDir, tt.wantSpillDir)
			}
		})
	}
}

func TestRemovedTurnCapsAreIgnoredByLegacyConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	contents := "bot_user = \"widget\"\n[budgets]\nmax_tokens = 1\n" +
		"[tools.tool_policy]\nmax_result_chars = 1234\nturn_budget_chars = 1\n" +
		"[[repos]]\nowner = \"acme\"\nname = \"app\"\n"
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := loadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	for _, removed := range []string{"max_tokens", "turn_budget_chars"} {
		if strings.Contains(string(encoded), removed) {
			t.Fatalf("removed turn cap %q survived config decode: %s", removed, encoded)
		}
	}
	if cfg.Tools.Policy.MaxResultChars != 1234 {
		t.Fatalf("per-tool result cap = %d, want 1234", cfg.Tools.Policy.MaxResultChars)
	}
}

// TestLoadWebFetchDefaultsAndOverrides covers the fetch tool's settings.
//
// The enabled case matters most: it is a pointer precisely so an operator can
// turn the tool off, which a plain bool could not express because defaulting
// cannot tell `enabled = false` from an absent key.
func TestLoadWebFetchDefaultsAndOverrides(t *testing.T) {
	tests := []struct {
		name         string
		webFetch     string
		wantEnabled  bool
		wantTimeout  time.Duration
		wantMaxBytes int64
		wantPrivate  bool
	}{
		{
			name:         "defaults",
			wantEnabled:  true,
			wantTimeout:  30 * time.Second,
			wantMaxBytes: 2_000_000,
		},
		{
			name:         "explicitly disabled",
			webFetch:     "[tools.web_fetch]\nenabled = false\n",
			wantEnabled:  false,
			wantTimeout:  30 * time.Second,
			wantMaxBytes: 2_000_000,
		},
		{
			name:         "overrides",
			webFetch:     "[tools.web_fetch]\ntimeout = \"5s\"\nmax_bytes = 4096\nallow_private_networks = true\n",
			wantEnabled:  true,
			wantTimeout:  5 * time.Second,
			wantMaxBytes: 4096,
			wantPrivate:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.toml")
			contents := "bot_user = \"widget\"\n" + tt.webFetch +
				"\n[[repos]]\nowner = \"acme\"\nname = \"app\"\n"
			if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
				t.Fatal(err)
			}

			cfg, err := loadFile(path)
			if err != nil {
				t.Fatal(err)
			}

			wf := cfg.Tools.WebFetch
			if got := wf.IsEnabled(); got != tt.wantEnabled {
				t.Errorf("IsEnabled() = %v, want %v", got, tt.wantEnabled)
			}
			if got := wf.Timeout.Std(); got != tt.wantTimeout {
				t.Errorf("Timeout = %s, want %s", got, tt.wantTimeout)
			}
			if wf.MaxBytes != tt.wantMaxBytes {
				t.Errorf("MaxBytes = %d, want %d", wf.MaxBytes, tt.wantMaxBytes)
			}
			if wf.AllowPrivateNetworks != tt.wantPrivate {
				t.Errorf("AllowPrivateNetworks = %v, want %v", wf.AllowPrivateNetworks, tt.wantPrivate)
			}
		})
	}
}

func TestLoadContainerVolumeTTL(t *testing.T) {
	tests := []struct {
		name    string
		ttl     string
		wantTTL time.Duration
	}{
		{name: "default", wantTTL: 72 * time.Hour},
		{name: "explicit", ttl: "volume_ttl = \"6h\"\n", wantTTL: 6 * time.Hour},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.toml")
			contents := "bot_user = \"widget\"\n" +
				"[nats]\nurl = \"nats://localhost:4222\"\n" +
				"[containers]\nenabled = true\nimage = \"archie-agent:test\"\n" + tt.ttl +
				"[[repos]]\nowner = \"acme\"\nname = \"app\"\npersistent_storage = true\n"
			if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
				t.Fatal(err)
			}

			cfg, err := loadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if got := cfg.Containers.VolumeTTL.Std(); got != tt.wantTTL {
				t.Fatalf("Containers.VolumeTTL = %s, want %s", got, tt.wantTTL)
			}
		})
	}
}

// TestLoadRejectsNegativeContainerVolumeTTL: containers are a control-plane
// resource (container-runtime-policies), so a negative volume_ttl is judged by
// Validate rather than by the load path -- a stale TOML value in a database-owned
// setting must not fail a process's startup. The second half of the assertion is
// what keeps dropping the check from passing this test.
func TestLoadRejectsNegativeContainerVolumeTTL(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	contents := "bot_user = \"widget\"\n" +
		"[nats]\nurl = \"nats://localhost:4222\"\n" +
		"[containers]\nenabled = true\nimage = \"archie-agent:test\"\nvolume_ttl = \"-1h\"\n" +
		"[[repos]]\nowner = \"acme\"\nname = \"app\"\npersistent_storage = true\n"
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}

	doc, err := New(nil).File(path)
	if err != nil {
		t.Fatalf("File: %v (a database-owned value must not fail the load)", err)
	}
	if err := Validate(&doc.Config); err == nil {
		t.Fatal("Validate accepted a negative containers.volume_ttl")
	}
}

func TestExampleConfigLoads(t *testing.T) {
	cfg, err := loadFile(filepath.Join("..", "..", "..", "config.example.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Forge.Host != "https://github.com" {
		t.Errorf("Forge.Host: got %q", cfg.Forge.Host)
	}
	if cfg.Dispatch.AckReaction != "eyes" {
		t.Errorf("Dispatch.AckReaction: got %q", cfg.Dispatch.AckReaction)
	}
	// [[repos]] is commented out in the template on purpose: an active
	// example here would validate as a normal repo (owner/name are only
	// checked for non-empty) and archied would silently poll one that
	// doesn't exist. At least one entry is required before archied will
	// poll anything, but the template cannot supply it -- see the comment
	// above [[repos]] in config.example.toml.
	if len(cfg.Repos) != 0 {
		t.Errorf("Repos: got %+v, want none: config.example.toml's [[repos]] entry is documentation, not a live default", cfg.Repos)
	}
}

func TestOverlay(t *testing.T) {
	dir := t.TempDir()
	basePath := filepath.Join(dir, "config.toml")
	base := "bot_user = \"widget\"\nwork_dir = \"/base/work\"\n" +
		"[agent]\nmode = \"inprocess\"\n" +
		"[[repos]]\nowner = \"acme\"\nname = \"app\"\n"
	if err := os.WriteFile(basePath, []byte(base), 0o600); err != nil {
		t.Fatal(err)
	}

	overlayPath := filepath.Join(dir, "config.docker.toml")
	overlay := "work_dir = \"/var/lib/archie/work\"\n[agent]\nmode = \"nats\"\n" +
		"[nats]\nurl = \"nats://nats:4222\"\n" +
		"[containers]\nenabled = true\nimage = \"archie-agent:latest\"\n"
	if err := os.WriteFile(overlayPath, []byte(overlay), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := loadOverlay(basePath, overlayPath)
	if err != nil {
		t.Fatal(err)
	}
	// Overlay-set fields win.
	if cfg.WorkDir != "/var/lib/archie/work" {
		t.Errorf("WorkDir: got %q, want overlay value", cfg.WorkDir)
	}
	if cfg.LegacyAgent.Mode != "nats" {
		t.Errorf("LegacyAgent.Mode: got %q, want %q", cfg.LegacyAgent.Mode, "nats")
	}
	// Fields the overlay omits keep the base value.
	if cfg.BotUser != "widget" {
		t.Errorf("BotUser: got %q, want base value", cfg.BotUser)
	}
	if len(cfg.Repos) != 1 || cfg.Repos[0].Owner != "acme" {
		t.Errorf("Repos: got %+v, want base repos", cfg.Repos)
	}

	// No overlay path behaves like loadFile(basePath).
	baseOnly, err := loadOverlay(basePath, "")
	if err != nil {
		t.Fatal(err)
	}
	if baseOnly.LegacyAgent.Mode != "inprocess" {
		t.Errorf("LegacyAgent.Mode with empty overlay: got %q, want %q", baseOnly.LegacyAgent.Mode, "inprocess")
	}
}

// TestResolveFileOverlayPreservesOmittedMapEntryFields characterizes the
// -config-overlay path at the depth the runtime overlay is already guarded at
// (TestApplyOverlayValuesPreservesOmittedNestedFields).
//
// It goes through Loader.Resolve, the production entry point, because the
// hazard was a property of how the overlay file is applied, not of any one
// decode helper: Loader.overlayFile decoded the overlay into the very config
// the base file produced, and a map-valued entry is replaced wholesale, so every
// field of that entry the overlay does not name was cleared.
//
// That is archie-core-e2e2 on a user-visible path. deployments/dev.toml is the
// documented -config-overlay argument and sets [services.state] target/listen,
// while the installed config.toml is where [services.state].target_token lives:
// the overlay silently dropped the token. The directory form of the same flag is
// covered by TestDirOverlayPreservesOmittedMapEntryFields.
func TestResolveFileOverlayPreservesOmittedMapEntryFields(t *testing.T) {
	tests := []struct {
		name        string
		base        string
		overlayName string
		overlay     string
		check       func(t *testing.T, cfg config.Config)
	}{
		{
			// The deployment shape quoted above, with the value that is read
			// only when a client dials a non-loopback State Store.
			name:    "services.state entry keeps the token it does not name",
			base:    "[services.state]\ntarget = \"127.0.0.1:9090\"\ntarget_token = \"secret\"\n",
			overlay: "[services.state]\ntarget = \"10.0.0.5:9090\"\n",
			check: func(t *testing.T, cfg config.Config) {
				t.Helper()
				got := cfg.Services[config.ServiceNameState]
				if got.Target != "10.0.0.5:9090" {
					t.Errorf("services.state.target = %q, want the overlay's 10.0.0.5:9090", got.Target)
				}
				if got.TargetToken != "secret" {
					t.Errorf("services.state.target_token = %q, want the base's secret: a file overlay must not clear a field it does not name", got.TargetToken)
				}
			},
		},
		{
			// An enabled hosted provider must keep its class and key env: the
			// overlay naming only base_url would otherwise clear them and fail
			// validation after the load.
			name: "image.hosted entry keeps the fields it does not name",
			base: "[image.hosted.minimax]\nenabled = true\nclass = \"minimax\"\n" +
				"api_key_env = \"MINIMAX_API_KEY\"\n",
			overlay: "[image.hosted.minimax]\nbase_url = \"https://api.example\"\n",
			check: func(t *testing.T, cfg config.Config) {
				t.Helper()
				got := cfg.Image.Hosted["minimax"]
				want := config.ImageHostedProvider{
					Enabled:   true,
					Class:     "minimax",
					APIKeyEnv: "MINIMAX_API_KEY",
					BaseURL:   "https://api.example",
				}
				if got != want {
					t.Errorf("image.hosted.minimax = %+v, want %+v", got, want)
				}
			},
		},
		{
			name: "providers entry keeps the fields it does not name",
			base: "[providers.anthropic]\nclass = \"anthropic\"\n" +
				"api_key_env = \"ANTHROPIC_API_KEY\"\n",
			overlay: "[providers.anthropic]\nbase_url = \"https://proxy.example\"\n",
			check: func(t *testing.T, cfg config.Config) {
				t.Helper()
				got := cfg.Providers["anthropic"]
				want := config.Provider{
					Class:     "anthropic",
					APIKeyEnv: "ANTHROPIC_API_KEY",
					BaseURL:   "https://proxy.example",
				}
				if got != want {
					t.Errorf("providers.anthropic = %+v, want %+v", got, want)
				}
			},
		},
		{
			// The overlay file may be YAML (-config-overlay accepts either
			// format), and it is folded by the same code.
			name:        "yaml overlay keeps the token it does not name",
			base:        "[services.state]\ntarget = \"127.0.0.1:9090\"\ntarget_token = \"secret\"\n",
			overlayName: "dev.yaml",
			overlay:     "services:\n  state:\n    target: 10.0.0.5:9090\n",
			check: func(t *testing.T, cfg config.Config) {
				t.Helper()
				got := cfg.Services[config.ServiceNameState]
				if got.Target != "10.0.0.5:9090" {
					t.Errorf("services.state.target = %q, want the overlay's 10.0.0.5:9090", got.Target)
				}
				if got.TargetToken != "secret" {
					t.Errorf("services.state.target_token = %q, want the base's secret", got.TargetToken)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			basePath := filepath.Join(dir, "config.toml")
			overlayName := tt.overlayName
			if overlayName == "" {
				overlayName = "dev.toml"
			}
			overlayPath := filepath.Join(dir, overlayName)
			if err := os.WriteFile(basePath, []byte(minimalValidConfigTOML+tt.base), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(overlayPath, []byte(tt.overlay), 0o600); err != nil {
				t.Fatal(err)
			}

			doc, err := New(nil).Resolve(basePath, overlayPath)
			if err != nil {
				t.Fatalf("Resolve: %v", err)
			}
			tt.check(t, doc.Config)
		})
	}
}

// TestLoadAcceptsStaleDatabaseOwnedValues is the bootstrap half of the layer
// split (archie-core-i3qm). docs/prds/runtime-control-plane.md,
// "Bootstrap, migration, and recovery": after migration, settings in TOML are
// ignored and cannot block State Store startup. The other half is the boot
// itself -- archie-state-store seeds these same settings into the control plane
// on a fresh database, and a seed it cannot validate is skipped rather than fatal
// (TestStateStoreBootsWithStaleDatabaseOwnedValues boots the process for that).
//
// Each case is a setting with a control-plane resource behind it, seeded from
// that field by controlplane.Server.ImportConfig: providers
// (provider-settings), poll_interval and dispatch (scheduling-policy),
// containers (container-runtime-policies), repositories
// (repository-policies). The second assertion is what keeps this from being a
// weaker gate: the value is still rejected by Validate, which is what the daemon
// and the Gateway run over the document a process will actually use. Nothing in
// this package layers that document, so the check here is the file document on
// its own -- the test is that the loader judges no database-owned field, not
// that the value is harmless.
func TestLoadAcceptsStaleDatabaseOwnedValues(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{
			name: "providers base_url carries userinfo",
			body: fileConfigPrefix + "[providers.openai]\nclass = \"openai\"\nbase_url = \"https://token@example.com/v1\"\n",
		},
		{
			name: "negative poll_interval",
			body: fileConfigPrefix + "poll_interval = \"-5s\"\n",
		},
		{
			name: "unrecognised dispatch.trigger",
			body: fileConfigPrefix + "[dispatch]\ntrigger = \"labels\"\n",
		},
		{
			name: "negative containers.volume_ttl",
			body: fileConfigPrefix + "[containers]\nvolume_ttl = \"-1m\"\n",
		},
		{
			name: "malformed repos test_glob",
			body: fileConfigPrefix + "[[repos]]\nowner = \"acme\"\nname = \"app\"\ntest_glob = \"[\"\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.toml")
			if err := os.WriteFile(path, []byte(tt.body), 0o600); err != nil {
				t.Fatal(err)
			}

			doc, err := New(nil).File(path)
			if err != nil {
				t.Fatalf("File: %v (a stale database-owned value must not fail the load)", err)
			}
			if err := Validate(&doc.Config); err == nil {
				t.Fatal("Validate = nil, want the control plane's own layer to reject it")
			}
		})
	}
}

// fileConfigPrefix is the smallest document the load path accepts on its own,
// so each case above adds one stale setting and nothing else.
const fileConfigPrefix = "bot_user = \"widget\"\nwork_dir = \"/base/work\"\n"

// TestDirOverlayPreservesOmittedMapEntryFields is the directory form of the
// same guarantee TestResolveFileOverlayPreservesOmittedMapEntryFields pins.
// -config-overlay accepts a directory as well as a file, Loader.Dir documents
// "the same field-level precedence as [Loader.Overlay]", and Loader.Resolve
// routes a directory source to Dir -- so a map-valued entry the overlay only
// partly addresses must keep the fields it does not name on both forms of the
// flag, including when the entry arrives through a feature file.
func TestDirOverlayPreservesOmittedMapEntryFields(t *testing.T) {
	tests := []struct {
		name         string
		baseFiles    map[string]string
		overlayFiles map[string]string
		check        func(t *testing.T, cfg config.Config)
	}{
		{
			name: "main config keeps the token it does not name",
			baseFiles: map[string]string{
				"config.yaml": "bot_user: widget\nservices:\n  state:\n    target: 127.0.0.1:9090\n    target_token: secret\n",
			},
			overlayFiles: map[string]string{
				"config.yaml": "services:\n  state:\n    target: 10.0.0.5:9090\n",
			},
			check: func(t *testing.T, cfg config.Config) {
				t.Helper()
				got := cfg.Services[config.ServiceNameState]
				if got.Target != "10.0.0.5:9090" {
					t.Errorf("services.state.target = %q, want the overlay's 10.0.0.5:9090", got.Target)
				}
				if got.TargetToken != "secret" {
					t.Errorf("services.state.target_token = %q, want the base's secret: a directory overlay must not clear a field it does not name", got.TargetToken)
				}
			},
		},
		{
			name: "feature file keeps the fields it does not name",
			baseFiles: map[string]string{
				"config.yaml":        "bot_user: widget\n",
				"config.models.yaml": "providers:\n  anthropic:\n    class: anthropic\n    api_key_env: ANTHROPIC_API_KEY\n",
			},
			overlayFiles: map[string]string{
				"config.models.yaml": "providers:\n  anthropic:\n    base_url: https://proxy.example\n",
			},
			check: func(t *testing.T, cfg config.Config) {
				t.Helper()
				got := cfg.Providers["anthropic"]
				want := config.Provider{
					Class:     "anthropic",
					APIKeyEnv: "ANTHROPIC_API_KEY",
					BaseURL:   "https://proxy.example",
				}
				if got != want {
					t.Errorf("providers.anthropic = %+v, want %+v", got, want)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			baseDir, overlayDir := tmpConfigDir(t), tmpConfigDir(t)
			for name, content := range tt.baseFiles {
				writeFile(t, baseDir, name, content)
			}
			for name, content := range tt.overlayFiles {
				writeFile(t, overlayDir, name, content)
			}

			doc, err := New(nil).Resolve(baseDir, overlayDir)
			if err != nil {
				t.Fatalf("Resolve: %v", err)
			}
			tt.check(t, doc.Config)
		})
	}
}

func TestLoadBytesDoesNotExist(t *testing.T) {
	_, err := loadBytes(nil)
	if err == nil {
		t.Error("expected error from nil LoadBytes")
	}
}

func TestIdentitiesConfigParsesTwoIdentities(t *testing.T) {
	cfg, err := loadBytes([]byte(`
bot_user = "default-legacy"

[[identities]]
name = "archie"
bot_user = "archie"
forge = { type = "gitea", host = "https://git.example.test", token = { engine = "env", key = "ARCHIE_TOKEN" } }

[[identities.repos]]
owner = "acme"
name = "archie-core"
base = "main"
ecosystem = "go"

[[identities]]
name = "winter"
bot_user = "winter"
forge = { type = "gitea", host = "https://git.example.test", token = { engine = "env", key = "WINTER_TOKEN" } }

[[identities.repos]]
owner = "acme"
name = "example-service"
base = "main"
ecosystem = "go"
`))
	if err != nil {
		t.Fatalf("LoadBytes: %v", err)
	}
	if len(cfg.Identities) != 2 {
		t.Fatalf("len(Identities) = %d, want 2", len(cfg.Identities))
	}
	if cfg.Identities[0].Name != "archie" {
		t.Errorf("Identities[0].Name = %q", cfg.Identities[0].Name)
	}
	if cfg.Identities[0].BotUser != "archie" {
		t.Errorf("Identities[0].BotUser = %q", cfg.Identities[0].BotUser)
	}
	if cfg.Identities[0].Forge.Token != (secret.SecretRef{Engine: "env", Key: "ARCHIE_TOKEN"}) {
		t.Errorf("Identities[0].Forge.Token = %#v", cfg.Identities[0].Forge.Token)
	}
	if len(cfg.Identities[0].Repos) != 1 || cfg.Identities[0].Repos[0].Name != "archie-core" {
		t.Errorf("Identities[0].Repos = %+v", cfg.Identities[0].Repos)
	}
	if cfg.Identities[1].Name != "winter" {
		t.Errorf("Identities[1].Name = %q", cfg.Identities[1].Name)
	}
	if cfg.Identities[1].BotUser != "winter" {
		t.Errorf("Identities[1].BotUser = %q", cfg.Identities[1].BotUser)
	}
	if cfg.Identities[1].Forge.Token != (secret.SecretRef{Engine: "env", Key: "WINTER_TOKEN"}) {
		t.Errorf("Identities[1].Forge.Token = %#v", cfg.Identities[1].Forge.Token)
	}
	if len(cfg.Identities[1].Repos) != 1 || cfg.Identities[1].Repos[0].Name != "example-service" {
		t.Errorf("Identities[1].Repos = %+v", cfg.Identities[1].Repos)
	}
}

func TestIdentitiesConfigFallsBackToLegacyWhenEmpty(t *testing.T) {
	cfg, err := loadBytes([]byte(`
bot_user = "solo"
forge = { type = "github", host = "https://github.test", token_env = "GH_TOKEN" }

[[repos]]
owner = "acme"
name = "my-repo"
`))
	if err != nil {
		t.Fatalf("LoadBytes: %v", err)
	}
	if len(cfg.Identities) != 0 {
		t.Errorf("len(Identities) = %d, want 0 (empty = legacy mode)", len(cfg.Identities))
	}
	if cfg.BotUser != "solo" {
		t.Errorf("BotUser = %q, want solo (legacy field active)", cfg.BotUser)
	}
}

func TestIdentitiesConfigRejectsEmptyName(t *testing.T) {
	_, err := loadBytes([]byte(`
[[identities]]
bot_user = "no-name"
forge = { type = "github", token_env = "X" }
`))
	if err == nil {
		t.Error("expected error for identity with empty name")
	}
}

func TestIdentitiesConfigRejectsMissingForgeToken(t *testing.T) {
	// Unlike the top-level [forge], a per-identity forge has no default
	// token  --  each identity needs its own secret reference (e.g.
	// distinct bot accounts each with their own token). Omitting it must
	// be a startup error, not a silent empty-string token.
	_, err := loadBytes([]byte(`
[[identities]]
name = "archie"
bot_user = "archie"
forge = { type = "github" }

[[identities.repos]]
owner = "acme"
name = "archie-core"
`))
	if err == nil {
		t.Fatal("expected error for identity with no forge.token")
	}
	if !strings.Contains(err.Error(), "forge.token") {
		t.Errorf("error = %q, want mention of forge.token", err.Error())
	}
}

// loadBytes writes data to a temporary file and loads it, so tests can
// exercise decoding without managing files themselves.
func loadBytes(data []byte) (config.Config, error) {
	if data == nil {
		return config.Config{}, os.ErrNotExist
	}
	tmp, err := os.CreateTemp("", "archie-config-*.toml")
	if err != nil {
		return config.Config{}, err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return config.Config{}, err
	}
	_ = tmp.Close()
	return loadFile(tmp.Name())
}
