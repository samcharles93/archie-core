// Package config loads archied's TOML configuration. The GitHub token is
// deliberately not part of the file: it comes from ARCHIE_GITHUB_TOKEN.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

// Duration is a time.Duration that unmarshals from TOML strings ("60s").
type Duration time.Duration

func (d *Duration) UnmarshalText(b []byte) error {
	v, err := time.ParseDuration(string(b))
	if err != nil {
		return err
	}
	*d = Duration(v)
	return nil
}

func (d Duration) Std() time.Duration { return time.Duration(d) }

// Repo is one managed repository.
type Repo struct {
	Owner string `toml:"owner"`
	Name  string `toml:"name"`
	// Base is the branch PRs target. Defaults to "main".
	Base string `toml:"base"`
	// Gate is the quality-gate command list for this repo, e.g.
	// [["go","vet","./..."], ["task","check"]]. Workflow stages may
	// extend or override it (a TDD repro stage inverts the test command).
	Gate [][]string `toml:"gate"`
	// Protect lists path suffixes agents must never write directly —
	// generated files (e.g. "_templ.go") whose sources they should edit
	// instead. Enforced environmentally via agentloop.ProtectPaths.
	Protect []string `toml:"protect"`
}

// Protected reports whether path matches a protected suffix.
func (r Repo) Protected(path string) bool {
	for _, suf := range r.Protect {
		if strings.HasSuffix(path, suf) {
			return true
		}
	}
	return false
}

func (r Repo) FullName() string { return r.Owner + "/" + r.Name }

func (r Repo) BaseBranch() string {
	if r.Base == "" {
		return "main"
	}
	return r.Base
}

// Budgets bound every agent stage. Zero disables a limit.
type Budgets struct {
	MaxSteps        int      `toml:"max_steps"`
	MaxTokens       int      `toml:"max_tokens"`
	WallClock       Duration `toml:"wall_clock"`
	GateMaxFailures int      `toml:"gate_max_failures"`
}

// Provider configures one LLM provider for the runtime catalog.
type Provider struct {
	Class     string `toml:"class"`
	APIKeyEnv string `toml:"api_key_env"`
	BaseURL   string `toml:"base_url"`
}

// Config is the daemon configuration.
type Config struct {
	WorkDir      string   `toml:"work_dir"`
	DBPath       string   `toml:"db_path"`
	PollInterval Duration `toml:"poll_interval"`
	// Label marks issues archie should pick up.
	Label   string `toml:"label"`
	BotUser string `toml:"bot_user"`
	// BotEmail is the git author email; defaults to the GitHub noreply
	// address for BotUser.
	BotEmail     string `toml:"bot_email"`
	DiffCapLines int    `toml:"diff_cap_lines"`

	// Models maps a role ("planner", "builder", "triage") to a runtime
	// model ref ("provider/model").
	Models map[string]string `toml:"models"`

	Providers map[string]Provider `toml:"providers"`

	Budgets Budgets `toml:"budgets"`
	Web     Web     `toml:"web"`
	Notify  Notify  `toml:"notify"`
	Repos   []Repo  `toml:"repos"`
}

// Notify configures outbound notifications (n8n webhook → email etc.).
type Notify struct {
	// Webhook receives JSON POSTs for events that need a human (e.g.
	// feasibility PRDs awaiting go/no-go). Empty disables.
	Webhook string `toml:"webhook"`
}

// Web configures the observability dashboard.
type Web struct {
	// Listen is the dashboard address; empty disables the web UI.
	// Bind localhost (or a LAN/tailnet address) — there is no auth.
	Listen string `toml:"listen"`
}

// Load reads, parses, and defaults the configuration.
func Load(path string) (Config, error) {
	var cfg Config
	if _, err := toml.DecodeFile(path, &cfg); err != nil {
		return cfg, fmt.Errorf("config %s: %w", path, err)
	}
	if cfg.WorkDir == "" {
		cfg.WorkDir = filepath.Join(dataHome(), "archie", "work")
	}
	if cfg.DBPath == "" {
		cfg.DBPath = filepath.Join(dataHome(), "archie", "archie.db")
	}
	if cfg.PollInterval == 0 {
		cfg.PollInterval = Duration(60 * time.Second)
	}
	if cfg.Label == "" {
		cfg.Label = "archie"
	}
	if cfg.DiffCapLines == 0 {
		cfg.DiffCapLines = 400
	}
	if cfg.Web.Listen == "" {
		cfg.Web.Listen = "127.0.0.1:8484" // "off" disables
	}
	if cfg.BotUser == "" {
		return cfg, errors.New("config: bot_user is required")
	}
	if cfg.BotEmail == "" {
		cfg.BotEmail = cfg.BotUser + "@users.noreply.github.com"
	}
	if len(cfg.Repos) == 0 {
		return cfg, errors.New("config: at least one [[repos]] entry is required")
	}
	for i, r := range cfg.Repos {
		if r.Owner == "" || r.Name == "" {
			return cfg, fmt.Errorf("config: repos[%d] needs owner and name", i)
		}
	}
	return cfg, nil
}

func dataHome() string {
	if x := os.Getenv("XDG_DATA_HOME"); x != "" {
		return x
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "share")
}
