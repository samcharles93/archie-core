package configuration

import (
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

func TestLoadRejectsInvalidConfigEnumsAndGlobs(t *testing.T) {
	tests := []struct {
		name  string
		extra string
	}{
		{name: "dispatch trigger", extra: "\n[dispatch]\ntrigger = \"labels\"\n"},
		{name: "agent mode", extra: "\n[agent]\nmode = \"remote\"\n"},
		{name: "agent command", extra: "\n[agent]\nmode = \"subprocess\"\ncommand = \"   \"\n"},
		{name: "agent env", extra: "\n[agent]\nenv = [\"TOKEN=value\"]\n"},
		{name: "provider userinfo", extra: "\n[providers.openai]\nclass = \"openai\"\nbase_url = \"https://token@example.com/v1\"\n"},
		{name: "provider query secret", extra: "\n[providers.openai]\nclass = \"openai\"\nbase_url = \"https://example.com/v1?api_key=secret\"\n"},
		{name: "test glob", extra: "\n[[repos]]\nowner = \"acme\"\nname = \"app\"\ntest_glob = \"[\"\n"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.toml")
			contents := "bot_user = \"widget\"\n"
			if tt.name != "test glob" {
				contents += "\n[[repos]]\nowner = \"acme\"\nname = \"app\"\n"
			}
			contents += tt.extra
			if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
				t.Fatal(err)
			}

			if _, err := loadFile(path); err == nil {
				t.Fatal("loadFile() succeeded, want validation error")
			}
		})
	}
}

func TestLoadAgentDefaultsAndOverrides(t *testing.T) {
	tests := []struct {
		name        string
		agent       string
		wantMode    string
		wantCommand string
		wantEnv     []string
	}{
		{name: "defaults", wantMode: "inprocess", wantCommand: "archie-agent"},
		{
			name: "subprocess", agent: "\n[agent]\nmode = \"subprocess\"\ncommand = \"/opt/archie-agent\"\nenv = [\"GOCACHE\"]\n",
			wantMode: "subprocess", wantCommand: "/opt/archie-agent", wantEnv: []string{"GOCACHE"},
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
			if cfg.Agent.Mode != tt.wantMode || cfg.Agent.Command != tt.wantCommand || strings.Join(cfg.Agent.Env, ",") != strings.Join(tt.wantEnv, ",") {
				t.Fatalf("agent config = %#v", cfg.Agent)
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
		name           string
		policy         string
		wantMaxResult  int
		wantTurnBudget int
		wantSpillDir   string
	}{
		{
			// SpillDir has no default on purpose: a path outside
			// chat.workspace is one the read tool refuses, so the model would
			// be handed a reference it cannot open. Absent means truncate
			// inline.
			name:           "defaults",
			wantMaxResult:  50_000,
			wantTurnBudget: 200_000,
			wantSpillDir:   "",
		},
		{
			name:           "explicit values",
			policy:         "[tools.tool_policy]\nmax_result_chars = 1234\nturn_budget_chars = 5678\nspill_dir = \"/var/spool/archie\"\n",
			wantMaxResult:  1234,
			wantTurnBudget: 5678,
			wantSpillDir:   "/var/spool/archie",
		},
		{
			name:           "negative disables both caps",
			policy:         "[tools.tool_policy]\nmax_result_chars = -1\nturn_budget_chars = -1\n",
			wantMaxResult:  -1,
			wantTurnBudget: -1,
			wantSpillDir:   "",
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
			if policy.TurnBudgetChars != tt.wantTurnBudget {
				t.Errorf("TurnBudgetChars = %d, want %d", policy.TurnBudgetChars, tt.wantTurnBudget)
			}
			if policy.SpillDir != tt.wantSpillDir {
				t.Errorf("SpillDir = %q, want %q", policy.SpillDir, tt.wantSpillDir)
			}
		})
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
				"[agent]\nmode = \"nats\"\n" +
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

func TestLoadRejectsNegativeContainerVolumeTTL(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	contents := "bot_user = \"widget\"\n" +
		"[agent]\nmode = \"nats\"\n" +
		"[nats]\nurl = \"nats://localhost:4222\"\n" +
		"[containers]\nenabled = true\nimage = \"archie-agent:test\"\nvolume_ttl = \"-1h\"\n" +
		"[[repos]]\nowner = \"acme\"\nname = \"app\"\npersistent_storage = true\n"
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := loadFile(path); err == nil {
		t.Fatal("loadFile() accepted a negative containers.volume_ttl")
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
	if cfg.Agent.Mode != "nats" {
		t.Errorf("Agent.Mode: got %q, want %q", cfg.Agent.Mode, "nats")
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
	if baseOnly.Agent.Mode != "inprocess" {
		t.Errorf("Agent.Mode with empty overlay: got %q, want %q", baseOnly.Agent.Mode, "inprocess")
	}
}

func TestLoadBytesDoesNotExist(t *testing.T) {
	_, err := loadBytes(nil)
	if err == nil {
		t.Error("expected error from nil LoadBytes")
	}
}

func TestFinalizeIndexingDefaults(t *testing.T) {
	tests := []struct {
		name        string
		extra       string
		wantIndex   string
		wantDBIsSet bool
	}{
		{
			name: "defaults derive from work_dir",
			// Both paths must land under the daemon's own work_dir so two
			// archied instances on one host cannot share an index.
			extra:       "\nwork_dir = \"/srv/archie/work\"\n",
			wantIndex:   filepath.Join("/srv/archie/work", "indexes"),
			wantDBIsSet: true,
		},
		{
			name: "explicit index_dir is preserved",
			extra: "\nwork_dir = \"/srv/archie/work\"\n" +
				"[indexing]\nindex_dir = \"/mnt/fast/idx\"\n",
			wantIndex:   "/mnt/fast/idx",
			wantDBIsSet: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.toml")
			// tc.extra precedes the table sections: a top-level key written
			// after [[repos]] would be parsed as a member of that table.
			contents := "bot_user = \"widget\"\n" + tc.extra +
				"[forge]\ntype = \"gitea\"\nhost = \"https://git.example.test\"\ntoken_env = \"T\"\n" +
				"[[repos]]\nowner = \"acme\"\nname = \"app\"\n"
			if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
				t.Fatal(err)
			}
			cfg, err := loadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if cfg.Indexing.IndexDir != tc.wantIndex {
				t.Errorf("Indexing.IndexDir = %q, want %q", cfg.Indexing.IndexDir, tc.wantIndex)
			}
			if tc.wantDBIsSet && cfg.Indexing.DBPath == "" {
				t.Error("Indexing.DBPath is empty; want a default derived from work_dir")
			}
		})
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
