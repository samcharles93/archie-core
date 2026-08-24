// Package config holds archied's configuration types.
//
// Loading them -- locating files, decoding TOML and YAML, applying overlays,
// defaulting and validating -- belongs to
// internal/infrastructure/configuration. This package no longer performs any
// I/O, so importing a settings type no longer drags in a file decoder.
//
// The forge API token is deliberately not part of the file: it comes from a
// configurable env var.
//
// Per docs/architecture/configuration.md this package is scheduled for
// dissolution: its types and methods are to be reassigned to the domains
// whose behaviour they describe. Do not add new fields here that a single
// domain could own.
package config

import (
	"encoding/json"
	"fmt"
	"maps"
	"strings"
	"time"

	"github.com/samcharles93/archie-core/internal/secret"
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
	Owner string `toml:"owner" json:"owner" yaml:"owner"`
	Name  string `toml:"name" json:"name" yaml:"name"`
	// Base is the branch PRs target. Defaults to "main".
	Base string `toml:"base" json:"base" yaml:"base"`
	// Gate is the quality-gate command list for this repo, e.g.
	// [["go","vet","./..."], ["task","check"]]. By convention the
	// LAST command is the test runner  --  TDD workflows invert only
	// that one with ExpectFailure during the repro stage and re-run
	// it in capture-proof. Workflow stages may extend or override
	// Gate (a TDD repro stage inverts the test command).
	Gate [][]string `toml:"gate" json:"gate" yaml:"gate"`
	// Protect lists path suffixes agents must never write directly  --
	// generated files (e.g. "_templ.go") whose sources they should edit
	// instead. Enforced environmentally via agentloop.ProtectPaths.
	Protect []string `toml:"protect" json:"protect" yaml:"protect"`

	// Ecosystem selects a default preflight check and test-file glob
	// ("go", "python", "node", "rust", or "custom"). Empty defaults
	// to "go" for backward compatibility.
	Ecosystem string `toml:"ecosystem" json:"ecosystem" yaml:"ecosystem"`
	// Preflight is an explicit override for the ecosystem's default
	// preflight commands. Empty inherits the ecosystem default.
	Preflight [][]string `toml:"preflight" json:"preflight" yaml:"preflight"`
	// TestGlob is an explicit override for the ecosystem's default
	// test-file pattern (e.g. "*_test.go"). Empty inherits the
	// ecosystem default.
	TestGlob string `toml:"test_glob" json:"test_glob" yaml:"test_glob"`
	// PersistentStorage creates a named Docker volume for this repo
	// (archie-repo-<owner>-<repo>) mounted at /data/repo. The volume
	// survives task completion and retains data across tasks. Use for
	// repos with expensive build artifacts or large dependency trees.
	PersistentStorage bool `toml:"persistent_storage" json:"persistent_storage" yaml:"persistent_storage"`
	// MaxRetries caps how many times a parked task is retried before
	// being permanently parked (status "dead"). 0 means use the
	// global Config.MaxRetries.
	MaxRetries int `toml:"max_retries" json:"max_retries" yaml:"max_retries"`
	// AllowConcurrent lets the daemon dispatch multiple tasks for this
	// repo at once instead of the default FIFO one-task-per-repo
	// serialization. Only safe for repos where concurrent worktrees
	// won't collide (e.g. different base branches or packages per
	// task). Still bounded by [containers].max_concurrency globally.
	AllowConcurrent bool `toml:"allow_concurrent" json:"allow_concurrent" yaml:"allow_concurrent"`
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
	MaxSteps        int      `toml:"max_steps" json:"max_steps" yaml:"max_steps"`
	WallClock       Duration `toml:"wall_clock" json:"wall_clock" yaml:"wall_clock"`
	GateMaxFailures int      `toml:"gate_max_failures" json:"gate_max_failures" yaml:"gate_max_failures"`
}

// Provider configures one LLM provider for the runtime catalog.
type Provider struct {
	Class     string           `toml:"class" yaml:"class" json:"class"`
	APIKeyEnv string           `toml:"api_key_env" yaml:"api_key_env" json:"api_key_env"`
	APIKey    secret.SecretRef `toml:"api_key" yaml:"api_key" json:"-"`
	BaseURL   string           `toml:"base_url" yaml:"base_url" json:"base_url"`
}

// LegacyAgent decodes the removed [agent] section so existing operator files
// continue to load during migration. Its fields have no runtime consumers:
// autonomous work always uses a task-scoped archie-agent container over NATS.
// Remove the section from new and updated configurations.
type LegacyAgent struct {
	Mode    string   `toml:"mode" yaml:"mode"`
	Command string   `toml:"command" yaml:"command"`
	Env     []string `toml:"env" yaml:"env"`
}

// Forge intake modes for Forge.Intake. Poll is the default; webhook reacts to
// forge events as they arrive instead of (or in addition to) polling.
const (
	ForgeIntakePoll    = "poll"
	ForgeIntakeWebhook = "webhook"
	ForgeIntakeBoth    = "both"
)

// Forge configures the code forge integration.
type Forge struct {
	Type     string           `toml:"type" yaml:"type"`
	Host     string           `toml:"host" yaml:"host"`
	Token    secret.SecretRef `toml:"token" yaml:"token"`
	TokenEnv string           `toml:"token_env" yaml:"token_env"`
	// Intake selects how forge issues become work: "poll" (default),
	// "webhook", or "both". Webhook intake reacts to forge events the moment
	// they arrive instead of up to poll_interval later.
	Intake string `toml:"intake" yaml:"intake"`
	// WebhookSecret is the shared secret used to verify GitHub webhook
	// signatures (X-Hub-Signature-256). Required when Intake is "webhook" or
	// "both".
	WebhookSecret secret.SecretRef `toml:"webhook_secret" yaml:"webhook_secret"`
	// WebhookAddr is the host:port the webhook receiver listens on. Required
	// when Intake is "webhook" or "both".
	WebhookAddr string `toml:"webhook_addr" yaml:"webhook_addr"`
}

// Dispatch configures how archied discovers work and reflects task state
// onto the forge via labels and reactions.
type Dispatch struct {
	// Trigger is how tasks are discovered: "assignee" (poll assigned
	// issues), "label" (poll labelled issues), or "either" (both).
	Trigger string `toml:"trigger" json:"trigger" yaml:"trigger"`
	// AckReaction is the emoji reaction posted on issue pickup. Set
	// ack_reaction = "off" in TOML to disable it; Load normalizes that
	// sentinel to an empty string for callers.
	AckReaction string `toml:"ack_reaction" json:"ack_reaction" yaml:"ack_reaction"`
	// Labels maps state names ("queued", "working", "waiting", "pr",
	// "parked") to their forge label strings. Each key is defaulted
	// independently; missing keys fall back to the archie:*
	// constants at call time via StateLabel().
	Labels map[string]string `toml:"labels" json:"labels" yaml:"labels"`
}

// DispatchLabelDefaults returns a copy of the fallback label set, for the
// configuration loader to fill absent entries with. A copy is returned so a
// caller filling gaps cannot mutate the defaults for everyone else.
func DispatchLabelDefaults() map[string]string {
	return cloneStringMap(dispatchLabelDefaults)
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
// to the archie:* constant when the key is missing or empty.
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

// MemoryConfig holds memory provider configuration.
type MemoryConfig struct {
	Provider       string            `yaml:"provider" json:"provider"`
	ProviderConfig map[string]string `yaml:"provider_config" json:"provider_config"`
	SessionTTL     Duration          `yaml:"session_ttl" json:"session_ttl"`
}

// MCPServer describes one MCP server connection.
type MCPServer struct {
	Name      string   `toml:"name" yaml:"name" json:"name"`
	Transport string   `toml:"transport" yaml:"transport" json:"transport"`
	Command   string   `toml:"command" yaml:"command" json:"command,omitempty"`
	Args      []string `toml:"args" yaml:"args" json:"args,omitempty"`
	WorkDir   string   `toml:"work_dir" yaml:"work_dir" json:"work_dir,omitempty"`
	URL       string   `toml:"url" yaml:"url" json:"url,omitempty"`
	// Headers are additional HTTP headers sent with every request (auth, etc.).
	Headers map[string]string `toml:"headers" yaml:"headers" json:"headers,omitempty"`
	// SSEEndpoint is the GET URL for the server→client event stream.
	SSEEndpoint string `toml:"sse_endpoint" yaml:"sse_endpoint" json:"sse_endpoint,omitempty"`
	// MessageEndpoint is the POST URL for client→server messages. When
	// empty, the client discovers it from the server's "endpoint" SSE event.
	MessageEndpoint string `toml:"message_endpoint" yaml:"message_endpoint" json:"message_endpoint,omitempty"`
}

// ToolPolicy holds tool execution limits.
//
// The size limits use a negative value for "no limit". Defaulting cannot
// distinguish an explicit 0 from an absent key, so zero has to stay available
// as "unset, give me the default" -- which leaves negative as the only way an
// operator can turn a cap off deliberately.
type ToolPolicy struct {
	// MaxResultChars caps a single tool result before it reaches the model.
	// Tools that set ToolEntry.MaxResultSizeChars override it.
	MaxResultChars int `toml:"max_result_chars" yaml:"max_result_chars" json:"max_result_chars"`

	// SpillDir is where results too large to inline are written so the model
	// can be handed a path instead of losing the content. Empty disables
	// spilling, leaving inline truncation as the only option.
	SpillDir          string `toml:"spill_dir" yaml:"spill_dir" json:"spill_dir"`
	ParallelExecution bool   `toml:"parallel_execution" yaml:"parallel_execution" json:"parallel_execution"`
}

// WebFetchConfig controls the web_fetch tool.
type WebFetchConfig struct {
	// Enabled advertises the tool to the model. Nil means unset, which
	// defaults to true.
	//
	// This is a pointer rather than a bool because defaulting cannot tell
	// `enabled = false` from an absent key, and turning the tool off has to
	// be expressible. Use IsEnabled rather than reading it directly.
	Enabled *bool `toml:"enabled" yaml:"enabled" json:"enabled"`

	// Timeout bounds one fetch including redirects.
	Timeout Duration `toml:"timeout" yaml:"timeout" json:"timeout"`

	// MaxBytes bounds how much of a response body is read.
	MaxBytes int64 `toml:"max_bytes" yaml:"max_bytes" json:"max_bytes"`

	// AllowPrivateNetworks permits fetching loopback, private and
	// link-local addresses. Off by default: this daemon's own dashboard,
	// NATS and the Docker API answer on those, and the URL to fetch
	// arrives through chat, which is an untrusted path.
	AllowPrivateNetworks bool `toml:"allow_private_networks" yaml:"allow_private_networks" json:"allow_private_networks"`
}

// IsEnabled reports whether the fetch tool should be registered, treating an
// absent setting as enabled.
func (c WebFetchConfig) IsEnabled() bool { return c.Enabled == nil || *c.Enabled }

// MinimaxConfig controls the generate_video tool
// (internal/tools/minimax). Unlike WebFetchConfig, absent is disabled, not
// enabled: web_fetch needs no credential and is safe as a silent default,
// while this tool spends real API credits per call, so it stays off until
// an operator explicitly turns it on and supplies a key.
type MinimaxConfig struct {
	// Enabled advertises the tool. Defaults to false (see the type doc).
	Enabled bool `toml:"enabled" yaml:"enabled" json:"enabled"`

	// APIKey authenticates against the MiniMax API.
	APIKey secret.SecretRef `toml:"api_key" yaml:"api_key" json:"-"`

	// BaseURL overrides the API host. Empty uses the client's built-in
	// default. Exists for a proxy or a self-hosted-compatible endpoint,
	// not expected to be set in normal operation.
	BaseURL string `toml:"base_url" yaml:"base_url" json:"base_url,omitempty"`
}

// IsEnabled reports whether the video-generation tool should be
// registered.
func (c MinimaxConfig) IsEnabled() bool { return c.Enabled }

// ToolsConfig holds MCP server and tool policy configuration.
type ToolsConfig struct {
	MCPServers []MCPServer    `toml:"mcp_servers" yaml:"mcp_servers" json:"mcp_servers"`
	Policy     ToolPolicy     `toml:"tool_policy" yaml:"tool_policy" json:"tool_policy"`
	WebFetch   WebFetchConfig `toml:"web_fetch" yaml:"web_fetch" json:"web_fetch"`
	Minimax    MinimaxConfig  `toml:"minimax" yaml:"minimax" json:"minimax"`
}

// Config is the daemon configuration.
type Config struct {
	WorkDir string `toml:"work_dir" yaml:"work_dir"`
	// SkillsDir is an optional path to a shared skills directory
	// containing .agents/skills/*/SKILL.md files. When set, the
	// daemon builds its workflow registry from the skill catalog
	// (plugin-defined workflows override built-ins). When empty,
	// only built-in workflows are available.
	SkillsDir string `toml:"skills_dir" yaml:"skills_dir"`
	// MaxRetries caps how many times a parked task is retried before
	// being permanently parked (status "dead"). Defaults to 3.
	MaxRetries int `toml:"max_retries" yaml:"max_retries"`
	// PluginDir is an optional path to a directory of Yaegi-interpreted
	// daemon plugins (*.go files). Each file must export a "Plugin"
	// variable satisfying the plugin.Plugin interface. Failed plugins
	// are skipped  --  the daemon starts with the remaining set. When
	// empty, no daemon plugins are loaded.
	PluginDir string `toml:"plugin_dir" yaml:"plugin_dir"`
	// SecretEngineDir contains Yaegi secret-engine plugins. Built-in env and
	// bws engines remain available when this is empty.
	SecretEngineDir string   `toml:"secret_engine_dir" yaml:"secret_engine_dir"`
	DBPath          string   `toml:"db_path" yaml:"db_path"`
	PollInterval    Duration `toml:"poll_interval" yaml:"poll_interval"`
	// Label marks issues archie should pick up.
	Label   string `toml:"label" yaml:"label"`
	BotUser string `toml:"bot_user" yaml:"bot_user"`
	// BotEmail is the git author email; defaults to the GitHub noreply
	// address for BotUser.
	BotEmail     string `toml:"bot_email" yaml:"bot_email"`
	DiffCapLines int    `toml:"diff_cap_lines" yaml:"diff_cap_lines"`

	Forge    Forge    `toml:"forge" yaml:"forge"`
	Dispatch Dispatch `toml:"dispatch" yaml:"dispatch"`

	// Models maps a role ("planner", "builder", "triage") to a runtime
	// model ref ("provider/model").
	Models map[string]string `toml:"models" yaml:"models"`

	Providers   map[string]Provider `toml:"providers" yaml:"providers"`
	LegacyAgent LegacyAgent         `toml:"agent" yaml:"agent"`

	Budgets    Budgets         `toml:"budgets" yaml:"budgets"`
	Web        Web             `toml:"web" yaml:"web"`
	Log        Log             `toml:"log" yaml:"log"`
	Notify     Notify          `toml:"notify" yaml:"notify"`
	NATS       NATSConfig      `toml:"nats" yaml:"nats"`
	Containers ContainerConfig `toml:"containers" yaml:"containers"`
	Chat       ChatConfig      `toml:"chat" yaml:"chat"`
	Capture    CaptureConfig   `toml:"capture" yaml:"capture"`

	// Memory holds memory provider configuration (from config.memory.yaml).
	Memory MemoryConfig `toml:"memory" yaml:"memory"`

	// Tools holds MCP server and tool policy configuration (from config.tools.yaml).
	Tools ToolsConfig `toml:"tools" yaml:"tools"`
	// Indexing locates the workspace codesearch index.
	Indexing IndexingConfig `toml:"indexing" yaml:"indexing"`

	// Identities declares multi-identity configurations. When non-empty,
	// each identity runs its own poll loop with its own forge client,
	// worktree manager, repo list, model/provider config, and NATS subject
	// namespace. When empty, the single-identity fields (BotUser,
	// Forge, Repos, Models, Providers, Dispatch, PollInterval) are used
	// unchanged for backward compatibility.
	Identities []IdentityConfig `toml:"identities" yaml:"identities"`
	Repos      []Repo           `toml:"repos" yaml:"repos"`

	// Extra holds additional feature configuration from conf.d/ files that
	// don't match a known feature name. Keys are the filename stem (e.g.
	// "custom-tool" for conf.d/custom-tool.yaml).
	Extra map[string]any `toml:"-" yaml:"-" json:"extra,omitempty"`
}

// IdentityConfig is a per-identity configuration subset. Each identity
// gets its own poll loop goroutine with isolated forge, repo, model,
// and dispatch configuration.
type IdentityConfig struct {
	// Name identifies this identity in logs, events, and NATS subject
	// namespaces (archie.<name>.task.<type>). Required when Identities
	// is non-empty.
	Name string `toml:"name" yaml:"name"`
	// BotUser is the forge username for this identity's git commits and
	// API calls. Required.
	BotUser string `toml:"bot_user" yaml:"bot_user"`
	// BotEmail is the git author email. Falls back to a forge-appropriate
	// default from BotUser when empty.
	BotEmail     string `toml:"bot_email" yaml:"bot_email"`
	DiffCapLines int    `toml:"diff_cap_lines" yaml:"diff_cap_lines"`
	// PollInterval overrides the shared PollInterval. When 0, the shared
	// interval is used.
	PollInterval Duration            `toml:"poll_interval" yaml:"poll_interval"`
	Forge        Forge               `toml:"forge" yaml:"forge"`
	Dispatch     Dispatch            `toml:"dispatch" yaml:"dispatch"`
	Models       map[string]string   `toml:"models" yaml:"models"`
	Providers    map[string]Provider `toml:"providers" yaml:"providers"`
	Budgets      Budgets             `toml:"budgets" yaml:"budgets"`
	Notify       Notify              `toml:"notify" yaml:"notify"`
	Repos        []Repo              `toml:"repos" yaml:"repos"`
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
// fields populated  --  everything else (Repos, NATS, Containers, Providers,
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

// Clone returns a deep copy of c: every reference-type field (maps,
// slices, and the reference fields nested inside them) is copied, so
// mutating the returned value cannot touch c or anything sharing memory
// with it. It is used before the runtime overlay (or a dashboard PATCH)
// decodes into a snapshot, so a failed or partial decode cannot mutate
// the published config -- Holder.Get returns a shallow copy whose maps
// are shared with the published snapshot.
func (c Config) Clone() Config {
	c.Models = cloneStringMap(c.Models)
	c.Providers = maps.Clone(c.Providers)
	c.Dispatch.Labels = cloneStringMap(c.Dispatch.Labels)
	c.LegacyAgent.Env = append([]string(nil), c.LegacyAgent.Env...)
	c.Repos = cloneRepos(c.Repos)
	c.Identities = cloneIdentities(c.Identities)
	c.Chat.Models = append([]string(nil), c.Chat.Models...)
	c.Chat.Telegram.AllowedUserIDs = append([]int64(nil), c.Chat.Telegram.AllowedUserIDs...)
	c.Chat.Telegram.UpdateCheckCommand = append([]string(nil), c.Chat.Telegram.UpdateCheckCommand...)
	c.Chat.Telegram.UpdateInstallCommand = append([]string(nil), c.Chat.Telegram.UpdateInstallCommand...)
	c.Tools.MCPServers = cloneMCPServers(c.Tools.MCPServers)
	c.Memory.ProviderConfig = maps.Clone(c.Memory.ProviderConfig)
	if c.Tools.WebFetch.Enabled != nil {
		v := *c.Tools.WebFetch.Enabled
		c.Tools.WebFetch.Enabled = &v
	}
	c.Extra = maps.Clone(c.Extra)
	return c
}

func cloneRepos(repos []Repo) []Repo {
	if repos == nil {
		return nil
	}
	out := make([]Repo, len(repos))
	for i, r := range repos {
		r.Gate = cloneStringSlices(r.Gate)
		r.Protect = append([]string(nil), r.Protect...)
		r.Preflight = cloneStringSlices(r.Preflight)
		out[i] = r
	}
	return out
}

func cloneIdentities(ids []IdentityConfig) []IdentityConfig {
	if ids == nil {
		return nil
	}
	out := make([]IdentityConfig, len(ids))
	for i, id := range ids {
		id.Models = cloneStringMap(id.Models)
		id.Providers = maps.Clone(id.Providers)
		id.Dispatch.Labels = cloneStringMap(id.Dispatch.Labels)
		id.Repos = cloneRepos(id.Repos)
		out[i] = id
	}
	return out
}

func cloneMCPServers(servers []MCPServer) []MCPServer {
	if servers == nil {
		return nil
	}
	out := make([]MCPServer, len(servers))
	for i, s := range servers {
		s.Args = append([]string(nil), s.Args...)
		s.Headers = maps.Clone(s.Headers)
		out[i] = s
	}
	return out
}

func cloneStringSlices(ss [][]string) [][]string {
	if ss == nil {
		return nil
	}
	out := make([][]string, len(ss))
	for i, s := range ss {
		out[i] = append([]string(nil), s...)
	}
	return out
}

// NATS mode values for NATSConfig.Mode. The zero value (empty) resolves from
// URL; these are the explicit spellings, shared with configuration validation
// so the two cannot drift.
const (
	NATSModeEmbedded = "embedded"
	NATSModeExternal = "external"
)

// NATSConfig configures NATS JetStream for task distribution and reaction
// delivery. The mode selects how NATS is provided:
//
//	"embedded"  – an in-process nats-server (default; no external server)
//	"external"  – connect to URL (required)
//
// An empty Mode resolves from URL: URL set means "external", URL empty means
// "embedded". Autonomous workflow handoff always uses NATS; there is no
// broker-off execution mode (docs/prds/embedded-nats.md).
type NATSConfig struct {
	// Mode selects embedded or external. Empty resolves from URL.
	Mode string `toml:"mode" yaml:"mode"`
	// URL is the NATS server address, e.g. "nats://localhost:4222".
	// Required when Mode is "external"; must be empty otherwise.
	URL string `toml:"url" yaml:"url"`
	// TokenEnv optionally names an env var holding an external NATS auth token.
	// Empty means no authentication. Embedded mode generates a per-start token.
	TokenEnv string `toml:"token_env" yaml:"token_env"`
}

// CaptureConfig configures the unbound webhook capture endpoint
// (docs/prds/event-capture-storage.md). All fields have defaults; an empty
// [capture] section is valid and produces them -- capture is on by default.
type CaptureConfig struct {
	// Retention is how long a captured event is kept before prune-on-write
	// deletes it. Zero or absent means the 7-day default; a positive value
	// configured explicitly is used as-is; a deployment that wants no age
	// bound sets it very large rather than using a magic "0 = unlimited"
	// spelling, since 0 already means "use the default" here.
	Retention Duration `toml:"retention" yaml:"retention"`
	// MaxEvents caps the table at this many newest rows; older rows are
	// pruned on every insert regardless of age. Zero means the 5000 default.
	MaxEvents int `toml:"max_events" yaml:"max_events"`
	// MaxBodyBytes rejects (413) any single POST body larger than this
	// before it is read into memory. Zero means the 256 KiB default.
	MaxBodyBytes int `toml:"max_body_bytes" yaml:"max_body_bytes"`
	// RatePerSecond and RateBurst configure the per-remote-address token
	// bucket (webhookguard.RateLimiter) applied before a request's body is
	// read. Keyed by remote address, not the source path segment, since
	// that segment is unregistered and attacker-chosen by design -- see
	// docs/prds/event-capture-storage.md's disk-bound mechanics. Zero means
	// the defaults (1 req/s, burst 5).
	RatePerSecond float64 `toml:"rate_per_second" yaml:"rate_per_second"`
	RateBurst     int     `toml:"rate_burst" yaml:"rate_burst"`
}

// ContainerConfig configures Docker sandbox execution of archie-agent.
type ContainerConfig struct {
	// LegacyEnabled decodes the removed containers.enabled switch so existing
	// operator files keep loading. It has no runtime consumer: autonomous
	// workflows always require managed task containers.
	LegacyEnabled bool `toml:"enabled" yaml:"enabled"`
	// Image is the Docker image to run (e.g. "ghcr.io/sam/archie-agent:latest").
	Image string `toml:"image" yaml:"image"`
	// MaxConcurrency limits simultaneous daemon tasks and containers.
	// Tasks from the same repository remain serialized. 0 = no limit.
	MaxConcurrency int `toml:"max_concurrency" yaml:"max_concurrency"`
	// MaxUptime caps a container's lifetime before recycling.
	MaxUptime Duration `toml:"max_uptime" yaml:"max_uptime"`
	// VolumeTTL is the maximum age of persistent per-repo storage before
	// automatic cleanup. It also bounds the host-side Git object cache used
	// to seed isolated task worktrees.
	VolumeTTL Duration `toml:"volume_ttl" yaml:"volume_ttl"`
	// PullPolicy controls image pulling: "missing" (default) or "always".
	PullPolicy string `toml:"pull_policy" yaml:"pull_policy"`
	// Network is the Docker network spawned agent containers join. Empty first
	// tries the daemon's own network (for containerized composition), then uses
	// Docker's default bridge. Embedded NATS binds the selected bridge's host
	// gateway; external deployments can set a Compose network explicitly when
	// workers must resolve broker service names (e.g. "archie-core_default").
	Network string `toml:"network" yaml:"network"`
}

// Log configures where archied writes its logs.
//
// Without File set, output goes only to stderr -- which under systemd means
// journald holds the sole copy, and running by hand leaves no record at all.
// A file gives a durable copy independent of the supervisor, and is what lets
// the dashboard show history from before it connected.
type Log struct {
	// File is the log file path. Empty disables file logging. The parent
	// directory is created if missing.
	File string `toml:"file" yaml:"file"`
	// MaxSizeMB rotates the file once it exceeds this size. Zero uses the
	// package default.
	MaxSizeMB int `toml:"max_size_mb" yaml:"max_size_mb"`
	// Keep is how many rotated files to retain. Zero uses the package
	// default.
	Keep int `toml:"keep" yaml:"keep"`
	// Level is "debug", "info", "warn" or "error". Empty means info.
	Level string `toml:"level" yaml:"level"`
	// Quiet suppresses stderr output when a file is configured. Off by
	// default so journald and an interactive terminal still see logs.
	Quiet bool `toml:"quiet" yaml:"quiet"`
}

// ChatConfig configures conversational front-ends (multi-agent
// collaboration PRD, phase C  --  docs/prds/multi-agent-collaboration.md).
// Empty (Telegram.Token.Key == "") disables chat entirely.
type ChatConfig struct {
	// Operator is the display name of the person this deployment assists,
	// shown to the chat agent as runtime metadata. Empty tells it nothing.
	//
	// This belongs in configuration, not in a prompt string: the operator's
	// name is deployment data. It was previously compiled into the default
	// persona, so every deployment claimed to work for one particular
	// person, and that name was sent to the model provider on every turn
	// regardless of who was running it.
	Operator string `toml:"operator" yaml:"operator"`
	// Workspace is the directory the chat agent's file and shell tools are
	// rooted at. Empty disables those tools entirely, which is the default:
	// they read, write and execute, so the directory must be a deliberate
	// choice rather than whatever the daemon happens to start in.
	Workspace string `toml:"workspace" yaml:"workspace"`
	// UnrestrictedFilesystem lifts the workspace jail from the chat agent's
	// read, write, edit, find and grep tools, letting them reach any absolute
	// path. Relative paths still resolve against Workspace.
	//
	// Off by default. Turn it on when the agent is a general-purpose operator
	// rather than an assistant scoped to one project: partitioning and
	// mounting a disk, migrating data between filesystems, or editing service
	// units are all cross-filesystem by nature, and a jail makes them
	// impossible.
	//
	// This widens which TOOL can do the work, not what the agent can reach.
	// The shell tool has never been confined, so a jailed deployment only
	// pushed the agent into doing everything through shell -- losing the
	// truncation, line numbering and read-before-write protection the
	// dedicated tools give it. Grant it deliberately all the same: it is the
	// difference between an agent that can edit /etc directly and one that has
	// to be asked to.
	UnrestrictedFilesystem bool `toml:"unrestricted_filesystem" yaml:"unrestricted_filesystem"`
	// ShowToolCalls renders each completed tool call inline in the reply,
	// naming the tool and one line of its result, before the answer built
	// from them. It is off by default: it exists to make a wrong tool
	// choice visible from the chat, and it narrates every internal step
	// to do so, which is a privacy/noise cost on a chat surface anyone
	// with dashboard or bot access can read.
	//
	// One setting, every chat channel: it lived under [chat.telegram]
	// alone until the web dashboard grew its own tool narration with no
	// way to turn it off, contradicting an operator who had already set
	// this to false expecting no tool activity to be shown anywhere.
	ShowToolCalls bool `toml:"show_tool_calls" yaml:"show_tool_calls"`
	// MaxSteps caps how many model/tool round-trips one chat turn may take
	// before the runtime stops and returns what it has. Zero uses the
	// default.
	//
	// This is a real task budget, not a safety limit: a single question
	// about a codebase routinely costs a read, several greps and a handful
	// of edits, and running out mid-turn looks to the user like the agent
	// simply gave up. Use /stop to interrupt, not a small step cap.
	MaxSteps int `toml:"max_steps" yaml:"max_steps"`
	// Models is the optional interactive-chat model catalog. When empty,
	// chat falls back to the distinct model references assigned to workflow
	// roles in the top-level [models] table.
	Models []string `toml:"models" yaml:"models"`
	// Email configures the inbound email gateway via SMTP.
	Email EmailConfig `toml:"email" yaml:"email"`
	// WebhookAddr is the host:port for the inbound webhook gateway.
	// Empty disables the webhook gateway.
	WebhookAddr string         `toml:"webhook_addr" yaml:"webhook_addr"`
	Telegram    TelegramConfig `toml:"telegram" yaml:"telegram"`
}

// TelegramConfig configures the Telegram Bot API channel.
//
// AllowedUserIDs is a deny-by-default allowlist: a Telegram bot is
// reachable by anyone who knows its handle, so an empty list means the
// gateway answers nobody rather than everybody. Chat tools run with the
// daemon's own authority, so an open bot is an open shell.
type TelegramConfig struct {
	// AllowedUserIDs lists the Telegram user IDs permitted to talk to the
	// bot. It matches the sender (from.id), not the chat, so adding the
	// bot to a group does not grant that group's members access. Empty
	// denies everyone.
	AllowedUserIDs []int64 `toml:"allowed_user_ids" yaml:"allowed_user_ids"`
	// TokenEnv names the env var holding the bot token from @BotFather.
	// Empty disables the Telegram channel.
	TokenEnv string `toml:"token_env" yaml:"token_env"`
	// UpdateCheckCommand writes a releaseupdate.Snapshot JSON document to
	// stdout. UpdateInstallCommand applies an already-approved update. Both
	// are argv arrays, never shell snippets.
	UpdateCheckCommand   []string `toml:"update_check_command" yaml:"update_check_command"`
	UpdateInstallCommand []string `toml:"update_install_command" yaml:"update_install_command"`
}

// EmailConfig configures the inbound email channel via SMTP.
type EmailConfig struct {
	// ListenAddr is the host:port for the inbound SMTP server.
	// Empty disables the email channel.
	ListenAddr string `toml:"listen_addr" yaml:"listen_addr"`
	// RelayAddr is the SMTP relay for outbound replies.
	RelayAddr string `toml:"relay_addr" yaml:"relay_addr"`
}

// Notify configures outbound notifications (n8n webhook → email etc.).
type Notify struct {
	// Webhook receives JSON POSTs for events that need a human (e.g.
	// feasibility PRDs awaiting go/no-go). Empty disables.
	Webhook string `toml:"webhook" json:"webhook" yaml:"webhook"`
}

// Web configures the observability dashboard.
type Web struct {
	// Listen is the dashboard address; "off" disables the web UI.
	// Bind localhost (or a LAN/tailnet address)  --  there is no auth.
	Listen string `toml:"listen" yaml:"listen"`
}
