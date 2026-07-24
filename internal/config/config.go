// Package config loads archied's TOML configuration. The forge API token is
// deliberately not part of the file: it comes from a configurable env var.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/url"
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

// MarshalJSON encodes a Duration using the same string representation as TOML.
func (d Duration) MarshalJSON() ([]byte, error) {
	return json.Marshal(d.Std().String())
}

// UnmarshalJSON decodes a duration string such as "60s".
func (d *Duration) UnmarshalJSON(data []byte) error {
	var value string
	if err := json.Unmarshal(data, &value); err != nil {
		return fmt.Errorf("duration must be a string: %w", err)
	}
	return d.UnmarshalText([]byte(value))
}

func (d Duration) Std() time.Duration { return time.Duration(d) }

// Repo is one managed repository.
type Repo struct {
	Owner string `toml:"owner" json:"owner"`
	Name  string `toml:"name" json:"name"`
	// Base is the branch PRs target. Defaults to "main".
	Base string `toml:"base" json:"base"`
	// Gate is the quality-gate command list for this repo, e.g.
	// [["go","vet","./..."], ["task","check"]]. By convention the
	// LAST command is the test runner — TDD workflows invert only
	// that one with ExpectFailure during the repro stage and re-run
	// it in capture-proof. Workflow stages may extend or override
	// Gate (a TDD repro stage inverts the test command).
	Gate [][]string `toml:"gate" json:"gate"`
	// Protect lists path suffixes agents must never write directly —
	// generated files (e.g. "_templ.go") whose sources they should edit
	// instead. Enforced environmentally via agentloop.ProtectPaths.
	Protect []string `toml:"protect" json:"protect"`

	// Ecosystem selects a default preflight check and test-file glob
	// ("go", "python", "node", "rust", or "custom"). Empty defaults
	// to "go" for backward compatibility.
	Ecosystem string `toml:"ecosystem" json:"ecosystem"`
	// Preflight is an explicit override for the ecosystem's default
	// preflight commands. Empty inherits the ecosystem default.
	Preflight [][]string `toml:"preflight" json:"preflight"`
	// TestGlob is an explicit override for the ecosystem's default
	// test-file pattern (e.g. "*_test.go"). Empty inherits the
	// ecosystem default.
	TestGlob string `toml:"test_glob" json:"test_glob"`
	// PersistentStorage creates a named Docker volume for this repo
	// (archie-repo-<owner>-<repo>) mounted at /data/repo. The volume
	// survives task completion and retains data across tasks. Use for
	// repos with expensive build artifacts or large dependency trees.
	PersistentStorage bool `toml:"persistent_storage" json:"persistent_storage"`
	// MaxRetries caps how many times a parked task is retried before
	// being permanently parked (status "dead"). 0 means use the
	// global Config.MaxRetries.
	MaxRetries int `toml:"max_retries" json:"max_retries"`
	// AllowConcurrent lets the daemon dispatch multiple tasks for this
	// repo at once instead of the default FIFO one-task-per-repo
	// serialization. Only safe for repos where concurrent worktrees
	// won't collide (e.g. different base branches or packages per
	// task). Still bounded by [containers].max_concurrency globally.
	AllowConcurrent bool `toml:"allow_concurrent" json:"allow_concurrent"`
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

// effectiveEcosystem returns the ecosystem to use, defaulting to "go"
// when unset (backward compatibility: pre-ecosystem configs are Go).
func (r Repo) effectiveEcosystem() string {
	if r.Ecosystem != "" {
		return r.Ecosystem
	}
	return "go"
}

// ResolvedPreflight returns the preflight commands for this repo.
// Explicit Preflight wins; otherwise the ecosystem default; empty
// for unknown ecosystems and "custom".
func (r Repo) ResolvedPreflight() [][]string {
	if len(r.Preflight) > 0 {
		return r.Preflight
	}
	if eco, ok := ecosystems[r.effectiveEcosystem()]; ok {
		return eco.Preflight
	}
	return nil
}

// ResolvedTestGlob returns the test-file glob pattern for this repo.
// Explicit TestGlob wins; otherwise the ecosystem default; empty
// for unknown ecosystems and "custom" (protection no-ops).
func (r Repo) ResolvedTestGlob() string {
	if r.TestGlob != "" {
		return r.TestGlob
	}
	if eco, ok := ecosystems[r.effectiveEcosystem()]; ok {
		return eco.TestGlob
	}
	return ""
}

func (r Repo) FullName() string { return r.Owner + "/" + r.Name }

func (r Repo) BaseBranch() string {
	if r.Base == "" {
		return "main"
	}
	return r.Base
}

// EffectiveMaxRetries returns the per-repo override when set (>0),
// otherwise the global default.
func (r Repo) EffectiveMaxRetries(global int) int {
	if r.MaxRetries > 0 {
		return r.MaxRetries
	}
	return global
}

// Budgets bound every agent stage. Zero disables a limit.
type Budgets struct {
	MaxSteps        int      `toml:"max_steps" json:"max_steps"`
	MaxTokens       int      `toml:"max_tokens" json:"max_tokens"`
	WallClock       Duration `toml:"wall_clock" json:"wall_clock"`
	GateMaxFailures int      `toml:"gate_max_failures" json:"gate_max_failures"`
}

// Provider configures one LLM provider for the runtime catalog.
type Provider struct {
	Class     string `toml:"class"`
	APIKeyEnv string `toml:"api_key_env"`
	BaseURL   string `toml:"base_url"`
}

// Agent configures how archied executes autonomous stages.
type Agent struct {
	// Mode is "inprocess" during migration or "subprocess" to invoke the
	// standalone archie-agent worker for every autonomous stage.
	Mode string `toml:"mode"`
	// Command is the archie-agent executable path used in subprocess mode.
	Command string `toml:"command"`
	// Env adds environment variable names to the worker allowlist.
	Env []string `toml:"env"`
}

// Forge configures the code forge integration.
type Forge struct {
	Type     string `toml:"type"`
	Host     string `toml:"host"`
	TokenEnv string `toml:"token_env"`
}

// Dispatch configures how archied discovers work and reflects task state
// onto the forge via labels and reactions.
type Dispatch struct {
	// Trigger is how tasks are discovered: "assignee" (poll assigned
	// issues), "label" (poll labelled issues), or "either" (both).
	Trigger string `toml:"trigger" json:"trigger"`
	// AckReaction is the emoji reaction posted on issue pickup. Set
	// ack_reaction = "off" in TOML to disable it; Load normalizes that
	// sentinel to an empty string for callers.
	AckReaction string `toml:"ack_reaction" json:"ack_reaction"`
	// Labels maps state names ("queued", "working", "waiting", "pr",
	// "parked") to their forge label strings. Each key is defaulted
	// independently; missing keys fall back to the legacy archie:*
	// constants at call time via StateLabel().
	Labels map[string]string `toml:"labels" json:"labels"`
}

// dispatchLabelDefaults is the fallback label set used when the user
// hasn't configured explicit [dispatch.labels] entries.
var dispatchLabelDefaults = map[string]string{
	"queued":  "agent:queued",
	"working": "agent:working",
	"waiting": "agent:waiting",
	"pr":      "agent:pr",
	"parked":  "agent:parked",
	"dead":    "agent:dead",
}

// StateLabel returns the configured label for a state name. Falls back
// to the legacy archie:* constant when the key is missing or empty.
func (d Dispatch) StateLabel(state string) string {
	if s, ok := d.Labels[state]; ok && s != "" {
		return s
	}
	if s, ok := dispatchLabelDefaults[state]; ok {
		return s
	}
	return ""
}

// LabelValues returns the set of all configured state label strings.
// Used by SetStateLabel to detect and remove old state labels regardless
// of naming convention.
func (d Dispatch) LabelValues() []string {
	seen := map[string]bool{}
	var out []string
	for _, k := range []string{"queued", "working", "waiting", "pr", "parked", "dead"} {
		v := d.StateLabel(k)
		if v != "" && !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}

// Config is the daemon configuration.
type Config struct {
	WorkDir string `toml:"work_dir"`
	// SkillsDir is an optional path to a shared skills directory
	// containing .agents/skills/*/SKILL.md files. When set, the
	// daemon builds its workflow registry from the skill catalog
	// (plugin-defined workflows override built-ins). When empty,
	// only built-in workflows are available.
	SkillsDir string `toml:"skills_dir"`
	// MaxRetries caps how many times a parked task is retried before
	// being permanently parked (status "dead"). Defaults to 3.
	MaxRetries int `toml:"max_retries"`
	// PluginDir is an optional path to a directory of Yaegi-interpreted
	// daemon plugins (*.go files). Each file must export a "Plugin"
	// variable satisfying the plugin.Plugin interface. Failed plugins
	// are skipped — the daemon starts with the remaining set. When
	// empty, no daemon plugins are loaded.
	PluginDir    string   `toml:"plugin_dir"`
	DBPath       string   `toml:"db_path"`
	PollInterval Duration `toml:"poll_interval"`
	// Label marks issues archie should pick up.
	Label   string `toml:"label"`
	BotUser string `toml:"bot_user"`
	// BotEmail is the git author email; defaults to the GitHub noreply
	// address for BotUser.
	BotEmail     string `toml:"bot_email"`
	DiffCapLines int    `toml:"diff_cap_lines"`

	Forge    Forge    `toml:"forge"`
	Dispatch Dispatch `toml:"dispatch"`

	// Models maps a role ("planner", "builder", "triage") to a runtime
	// model ref ("provider/model").
	Models map[string]string `toml:"models"`

	Providers map[string]Provider `toml:"providers"`
	Agent     Agent               `toml:"agent"`

	Budgets    Budgets         `toml:"budgets"`
	Web        Web             `toml:"web"`
	Notify     Notify          `toml:"notify"`
	NATS       NATSConfig      `toml:"nats"`
	Containers ContainerConfig `toml:"containers"`
	Repos      []Repo          `toml:"repos"`
}

// TaskConfig is the non-secret subset of Config needed to run workflow
// stages. It is safe to send to an archie-agent container.
type TaskConfig struct {
	Models       map[string]string `json:"models"`
	Budgets      Budgets           `json:"budgets"`
	Dispatch     Dispatch          `json:"dispatch"`
	DiffCapLines int               `json:"diff_cap_lines"`
	Notify       Notify            `json:"notify"`
	Forge        TaskForge         `json:"forge"`
}

// TaskForge is the non-secret forge configuration needed by workflow stages.
type TaskForge struct {
	Host string `json:"host"`
}

// ForTask returns a detached, non-secret snapshot of the configuration fields
// needed to run a workflow.
func (c Config) ForTask() TaskConfig {
	return TaskConfig{
		Models:       cloneStringMap(c.Models),
		Budgets:      c.Budgets,
		Dispatch:     Dispatch{Trigger: c.Dispatch.Trigger, AckReaction: c.Dispatch.AckReaction, Labels: cloneStringMap(c.Dispatch.Labels)},
		DiffCapLines: c.DiffCapLines,
		Notify:       c.Notify,
		Forge:        TaskForge{Host: c.Forge.Host},
	}
}

// ToConfig expands a TaskConfig back into a Config with only the carried
// fields populated — everything else (Repos, NATS, Containers, Providers,
// secrets) is zero. Used by archie-agent to reconstruct the Config value
// workflow stages read via TaskContext.Cfg.
func (tc TaskConfig) ToConfig() Config {
	return Config{
		Models:       cloneStringMap(tc.Models),
		Budgets:      tc.Budgets,
		Dispatch:     Dispatch{Trigger: tc.Dispatch.Trigger, AckReaction: tc.Dispatch.AckReaction, Labels: cloneStringMap(tc.Dispatch.Labels)},
		DiffCapLines: tc.DiffCapLines,
		Notify:       tc.Notify,
		Forge:        Forge{Host: tc.Forge.Host},
	}
}

func cloneStringMap(src map[string]string) map[string]string {
	if src == nil {
		return nil
	}
	dst := make(map[string]string, len(src))
	maps.Copy(dst, src)
	return dst
}

// NATSConfig configures NATS JetStream for task distribution. When URL is
// empty the existing SQLite ClaimNext flow is used unchanged.
type NATSConfig struct {
	// URL is the NATS server address, e.g. "nats://localhost:4222".
	// Empty means NATS is not configured.
	URL string `toml:"url"`
	// TokenEnv optionally names an env var holding a NATS auth token.
	// Empty means no authentication.
	TokenEnv string `toml:"token_env"`
}

// ContainerConfig configures Docker sandbox execution of archie-agent.
type ContainerConfig struct {
	// Enabled activates container execution. Requires agent.mode = "nats".
	Enabled bool `toml:"enabled"`
	// Image is the Docker image to run (e.g. "ghcr.io/sam/archie-agent:latest").
	Image string `toml:"image"`
	// MaxConcurrency limits simultaneous daemon tasks and containers.
	// Tasks from the same repository remain serialized. 0 = no limit.
	MaxConcurrency int `toml:"max_concurrency"`
	// MaxUptime caps a container's lifetime before recycling.
	MaxUptime Duration `toml:"max_uptime"`
	// VolumeTTL is the maximum age of persistent per-repo storage before
	// automatic cleanup. It also bounds the host-side Git object cache used
	// to seed isolated task worktrees.
	VolumeTTL Duration `toml:"volume_ttl"`
	// PullPolicy controls image pulling: "missing" (default) or "always".
	PullPolicy string `toml:"pull_policy"`
}

// Notify configures outbound notifications (n8n webhook → email etc.).
type Notify struct {
	// Webhook receives JSON POSTs for events that need a human (e.g.
	// feasibility PRDs awaiting go/no-go). Empty disables.
	Webhook string `toml:"webhook" json:"webhook"`
}

// Web configures the observability dashboard.
type Web struct {
	// Listen is the dashboard address; "off" disables the web UI.
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
		cfg.Label = ""
	}
	if cfg.DiffCapLines == 0 {
		cfg.DiffCapLines = 400
	}
	if cfg.Web.Listen == "" {
		cfg.Web.Listen = "127.0.0.1:8484" // "off" disables
	}
	if cfg.Forge.Type == "" {
		cfg.Forge.Type = "github"
	}
	if cfg.Forge.Host == "" {
		cfg.Forge.Host = "https://github.com"
	}
	if cfg.Forge.TokenEnv == "" {
		cfg.Forge.TokenEnv = "ARCHIE_GITHUB_TOKEN"
	}
	if cfg.Agent.Mode == "" {
		cfg.Agent.Mode = "inprocess"
	}
	if cfg.Agent.Command == "" {
		cfg.Agent.Command = "archie-agent"
	}
	switch cfg.Agent.Mode {
	case "inprocess", "subprocess", "nats":
	default:
		return cfg, fmt.Errorf("config: agent.mode %q is invalid (want inprocess, subprocess, or nats)", cfg.Agent.Mode)
	}
	if cfg.Agent.Mode == "subprocess" && strings.TrimSpace(cfg.Agent.Command) == "" {
		return cfg, fmt.Errorf("config: agent.command is required in subprocess mode")
	}
	for i, name := range cfg.Agent.Env {
		if strings.TrimSpace(name) == "" || strings.Contains(name, "=") {
			return cfg, fmt.Errorf("config: agent.env[%d] %q is not an environment variable name", i, name)
		}
	}
	for name, provider := range cfg.Providers {
		if provider.BaseURL == "" {
			continue
		}
		u, err := url.Parse(provider.BaseURL)
		if err != nil {
			return cfg, fmt.Errorf("config: providers.%s.base_url is invalid: %w", name, err)
		}
		if u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			return cfg, fmt.Errorf("config: providers.%s.base_url must not contain userinfo, query parameters, or a fragment", name)
		}
	}
	if cfg.Dispatch.Trigger == "" {
		cfg.Dispatch.Trigger = "assignee"
	}
	switch cfg.Dispatch.Trigger {
	case "assignee", "label", "either":
	default:
		return cfg, fmt.Errorf("config: dispatch.trigger %q is invalid (want assignee, label, or either)", cfg.Dispatch.Trigger)
	}
	if cfg.Dispatch.AckReaction == "" {
		cfg.Dispatch.AckReaction = "eyes"
	} else if strings.EqualFold(cfg.Dispatch.AckReaction, "off") {
		cfg.Dispatch.AckReaction = ""
	}
	if cfg.Dispatch.Labels == nil {
		cfg.Dispatch.Labels = map[string]string{}
	}
	for k, v := range dispatchLabelDefaults {
		if cfg.Dispatch.Labels[k] == "" {
			cfg.Dispatch.Labels[k] = v
		}
	}
	if cfg.MaxRetries == 0 {
		cfg.MaxRetries = 3
	}
	if cfg.BotUser == "" {
		return cfg, errors.New("config: bot_user is required")
	}
	if cfg.BotEmail == "" {
		switch cfg.Forge.Type {
		case "github":
			cfg.BotEmail = cfg.BotUser + "@users.noreply.github.com"
		case "gitea":
			cfg.BotEmail = cfg.BotUser + "@gitea.local"
		}
	}
	if len(cfg.Repos) == 0 {
		return cfg, errors.New("config: at least one [[repos]] entry is required")
	}
	switch cfg.Forge.Type {
	case "github", "gitea":
	default:
		return cfg, fmt.Errorf("config: forge.type %q is not supported (want github or gitea)", cfg.Forge.Type)
	}
	for i, r := range cfg.Repos {
		if r.Owner == "" || r.Name == "" {
			return cfg, fmt.Errorf("config: repos[%d] needs owner and name", i)
		}
		if glob := r.ResolvedTestGlob(); glob != "" {
			if _, err := filepath.Match(glob, ""); err != nil {
				return cfg, fmt.Errorf("config: repos[%d] test_glob %q is invalid: %w", i, glob, err)
			}
		}
	}
	if cfg.Containers.Enabled {
		if cfg.Containers.Image == "" {
			return cfg, errors.New("config: containers.image is required when containers.enabled is true")
		}
		if cfg.Agent.Mode != "nats" {
			return cfg, errors.New("config: agent.mode must be \"nats\" when containers.enabled is true")
		}
		if cfg.NATS.URL == "" {
			return cfg, errors.New("config: [nats] url is required when containers.enabled is true")
		}
		if cfg.Containers.MaxUptime == 0 {
			cfg.Containers.MaxUptime = Duration(60 * time.Minute)
		}
		if cfg.Containers.VolumeTTL < 0 {
			return cfg, errors.New("config: containers.volume_ttl must not be negative")
		}
		if cfg.Containers.VolumeTTL == 0 {
			cfg.Containers.VolumeTTL = Duration(72 * time.Hour)
		}
		if cfg.Containers.PullPolicy == "" {
			cfg.Containers.PullPolicy = "missing"
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
