// Package config holds archied's configuration types. Loading lives in
// internal/infrastructure/configuration.
package config

import (
	"encoding/json"
	"fmt"
	"maps"
	"net"
	"strings"
	"time"
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
	// Gate is the repo's gate command list, e.g. [["go","vet","./..."],
	// ["task","check"]]. The last command is the test runner, which TDD's repro
	// stage expects to fail.
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
	// AllowConcurrent lets several tasks run for this repo at once instead of
	// one at a time.
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

// Budgets bound every agent stage, and TaskWallClock the whole task run. Zero
// disables a limit.
type Budgets struct {
	MaxSteps        int      `toml:"max_steps" json:"max_steps" yaml:"max_steps"`
	WallClock       Duration `toml:"wall_clock" json:"wall_clock" yaml:"wall_clock"`
	GateMaxFailures int      `toml:"gate_max_failures" json:"gate_max_failures" yaml:"gate_max_failures"`
	TaskWallClock   Duration `toml:"task_wall_clock" json:"task_wall_clock" yaml:"task_wall_clock"`
}

// Provider configures one LLM provider for the runtime catalog.
type Provider struct {
	Class     string    `toml:"class" yaml:"class" json:"class"`
	APIKeyEnv string    `toml:"api_key_env" yaml:"api_key_env" json:"api_key_env"`
	APIKey    SecretRef `toml:"api_key" yaml:"api_key" json:"-"`
	BaseURL   string    `toml:"base_url" yaml:"base_url" json:"base_url"`
	// Disabled marks a configured provider whose api_key could not be
	// resolved at boot. It is runtime state, not a setting: without it a
	// disabled provider is indistinguishable from one the operator never
	// configured, and the model catalog would serve it from an ambient
	// environment variable.
	Disabled bool `toml:"-" yaml:"-" json:"-"`
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
	Type  string    `toml:"type" yaml:"type"`
	Host  string    `toml:"host" yaml:"host"`
	Token SecretRef `toml:"token" yaml:"token"`
	// Intake selects how forge issues become work: "poll" (default),
	// "webhook", or "both". Webhook intake reacts to forge events the moment
	// they arrive instead of up to poll_interval later.
	Intake string `toml:"intake" yaml:"intake"`
	// WebhookSecret is the shared secret used to verify GitHub webhook
	// signatures (X-Hub-Signature-256). Required when Intake is "webhook" or
	// "both".
	WebhookSecret SecretRef `toml:"webhook_secret" yaml:"webhook_secret"`
	// WebhookAddr is the host:port the webhook receiver listens on. Required
	// when Intake is "webhook" or "both".
	WebhookAddr string `toml:"webhook_addr" yaml:"webhook_addr"`
}

// Review configures the pr-review workflow.
// Both fields are off by default: the pipeline is recall-first, and posts
// automatically, unless an operator opts into either dial.
type Review struct {
	// It trades recall for precision and cuts verification cost by filtering
	// early.
	PrecisionGate bool `toml:"precision_gate" yaml:"precision_gate"`
	// ApproveBeforePost ends the pipeline in waiting_human right after
	// synthesis, before the merge gate runs, so an operator can choose which
	// findings to post, reject the review, or ask for a re-review with
	// instructions. A review with no findings never waits.
	ApproveBeforePost bool `toml:"approve_before_post" yaml:"approve_before_post"`
}

// Dispatch configures how archied discovers work and reacts on pickup.
type Dispatch struct {
	// Trigger is how tasks are discovered: "assignee" (poll assigned
	// issues), "label" (poll labelled issues), or "either" (both).
	Trigger string `toml:"trigger" json:"trigger" yaml:"trigger"`
	// AckReaction is the emoji reaction posted on issue pickup. Set
	// ack_reaction = "off" in TOML to disable it; Load normalizes that
	// sentinel to an empty string for callers.
	AckReaction string `toml:"ack_reaction" json:"ack_reaction" yaml:"ack_reaction"`
}

// MemoryConfig holds memory engine configuration.
type MemoryConfig struct {
	// Engine selects among the domain/memory engine family
	// (internal/domain/memory). Empty resolves to "builtin", the
	// file-writing store under work_dir -- the same behaviour as an
	// absent [memory] section entirely.
	Engine string `toml:"engine" yaml:"engine" json:"engine"`
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

	// ParallelToolCalls allows concurrent tool calls to this server. Default
	// false: one call at a time.
	ParallelToolCalls bool `toml:"parallel_tool_calls" yaml:"parallel_tool_calls" json:"parallel_tool_calls"`
}

// ToolPolicy holds tool execution limits. A negative size limit means no
// limit; zero means the default.
type ToolPolicy struct {
	// MaxResultChars caps a single tool result before it reaches the model.
	// Tools that set ToolEntry.MaxResultSizeChars override it.
	MaxResultChars int `toml:"max_result_chars" yaml:"max_result_chars" json:"max_result_chars"`

	// SpillDir is where results too large to inline are written so the model
	// can be handed a path instead of losing the content. Empty disables
	// spilling, leaving inline truncation as the only option.
	SpillDir string `toml:"spill_dir" yaml:"spill_dir" json:"spill_dir"`
}

// WebFetchConfig controls the web_fetch tool.
type WebFetchConfig struct {
	// Enabled advertises the tool to the model; nil means true. Read it with
	// IsEnabled.
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

// MinimaxConfig controls the generate_video tool. Disabled unless configured.
type MinimaxConfig struct {
	// Enabled advertises the tool. Defaults to false (see the type doc).
	Enabled bool `toml:"enabled" yaml:"enabled" json:"enabled"`

	// APIKey authenticates against the MiniMax API.
	APIKey SecretRef `toml:"api_key" yaml:"api_key" json:"-"`

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
	Services Services `toml:"services" yaml:"services"`
	WorkDir  string   `toml:"work_dir" yaml:"work_dir"`
	// SkillsDir is an optional directory of */SKILL.md skills. Its workflows
	// override built-ins, and Kits mount it read-only.
	SkillsDir string `toml:"skills_dir" yaml:"skills_dir"`
	// WorkflowRoutingFile is an optional path to a YAML file rebinding which
	// registered workflow an intake Kind (bug/feature) prefers.
	WorkflowRoutingFile string `toml:"workflow_routing_file" yaml:"workflow_routing_file"`
	// WorkflowLabelsFile is an optional path to a YAML file binding forge issue
	// labels outside the closed bug/feature kind set to registered
	// workflows (e.g. A label already owned by the kind set, or a duplicate
	// binding, is a load failure per the design doc's collision rule.
	WorkflowLabelsFile string `toml:"workflow_labels_file" yaml:"workflow_labels_file"`
	// PlaybookDirs are optional directories of kind/label-to-workflow binding
	// files. A key bound in more than one file is a load error.
	PlaybookDirs []string `toml:"playbook_dirs" yaml:"playbook_dirs"`
	// MaxRetries caps how many times a parked task is retried before
	// being permanently parked (status "dead"). Defaults to 3.
	MaxRetries int `toml:"max_retries" yaml:"max_retries"`
	// StateDir is the host directory archie keeps non-database state in: the
	// embedded NATS store and its endpoint file, the task-log registry, and
	// the readiness disk probe's data target. Bootstrap-only.
	StateDir string `toml:"state_dir" yaml:"state_dir"`
	// DatabaseURL is the PostgreSQL URL the State Store opens at boot. Required;
	// bootstrap-only.
	DatabaseURL  string   `toml:"database_url" yaml:"database_url"`
	PollInterval Duration `toml:"poll_interval" yaml:"poll_interval"`
	// Label marks issues archie should pick up.
	Label   string `toml:"label" yaml:"label"`
	BotUser string `toml:"bot_user" yaml:"bot_user"`
	// Org is this identity's org. Empty means
	// "default": the org a single-operator install's one identity belongs to.
	// A credential binding whose Org does not match a run's identity never
	// resolves for it, regardless of what the identity is granted.
	Org string `toml:"org" yaml:"org"`
	// GrantedCredentials names the credential services this identity may use.
	// Empty grants none.
	GrantedCredentials []string `toml:"granted_credentials" yaml:"granted_credentials"`
	// BotEmail is the git author email; defaults to the GitHub noreply
	// address for BotUser.
	BotEmail string `toml:"bot_email" yaml:"bot_email"`
	// DiffCapLines parks a task whose diff exceeds this many changed lines. Nil
	// takes the default; 0 means no cap. Read it with DiffCap.
	DiffCapLines *int `toml:"diff_cap_lines" yaml:"diff_cap_lines"`

	Forge    Forge    `toml:"forge" yaml:"forge"`
	Dispatch Dispatch `toml:"dispatch" yaml:"dispatch"`
	Review   Review   `toml:"review" yaml:"review"`

	// Models maps a role ("planner", "builder", "triage") to a runtime
	// model ref ("provider/model").
	Models map[string]string `toml:"models" yaml:"models"`

	Providers map[string]Provider `toml:"providers" yaml:"providers"`

	Budgets    Budgets         `toml:"budgets" yaml:"budgets"`
	Web        Web             `toml:"web" yaml:"web"`
	Health     Health          `toml:"health" yaml:"health"`
	Log        Log             `toml:"log" yaml:"log"`
	Notify     Notify          `toml:"notify" yaml:"notify"`
	NATS       NATSConfig      `toml:"nats" yaml:"nats"`
	Containers ContainerConfig `toml:"containers" yaml:"containers"`
	Chat       ChatConfig      `toml:"chat" yaml:"chat"`
	Capture    CaptureConfig   `toml:"capture" yaml:"capture"`
	Bindings   BindingsConfig  `toml:"bindings" yaml:"bindings"`

	// Memory holds memory provider configuration (from config.memory.yaml).
	Memory MemoryConfig `toml:"memory" yaml:"memory"`

	// Tools holds MCP server and tool policy configuration (from config.tools.yaml).
	Tools ToolsConfig `toml:"tools" yaml:"tools"`

	// Identities declares multiple identities, each with its own forge, repos,
	// models and poll loop. Empty uses the single-identity fields.
	Identities  []IdentityConfig       `toml:"identities" yaml:"identities"`
	Repos       []Repo                 `toml:"repos" yaml:"repos"`
	ModelLimits map[string]ModelLimits `toml:"-" yaml:"-" json:"-"`

	// Curators are curator definitions seeded from [[curators]].
	Curators []CuratorDefinition `toml:"curators" yaml:"curators" json:"curators,omitempty"`
}

// IdentityConfig is a per-identity configuration subset. Each identity
// gets its own forge account and repository membership. Runtime settings
// come from the shared control plane.
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
	BotEmail string `toml:"bot_email" yaml:"bot_email"`
	// Org overrides the shared Org for this identity. Empty inherits it.
	Org string `toml:"org" yaml:"org"`
	// GrantedCredentials overrides the shared GrantedCredentials for this
	// identity. Nil (absent) inherits it; a present, empty list grants none
	// -- an explicit empty list turns grants off, needed
	// because a plain nil already means "not set" here.
	GrantedCredentials *[]string `toml:"granted_credentials" yaml:"granted_credentials"`
	Forge              Forge     `toml:"forge" yaml:"forge"`
	Repos              []Repo    `toml:"repos" yaml:"repos"`
}

type ModelLimits struct {
	ContextWindow   int `json:"context_window"`
	MaxOutputTokens int `json:"max_output_tokens"`
	// Reasoning marks a reasoning-class model. It is derived from the model
	// catalog (never operator-authored) and carried to the agent worker so a
	// task-scoped MCP sampling request omits the max-token bound those models
	// reject, exactly as the daemon's chat path does.
	Reasoning bool `json:"reasoning,omitempty"`
}

// TaskConfig is the non-secret subset of Config needed to run workflow
// stages. It is safe to send to an archie-agent container.
type TaskConfig struct {
	// BotUser and BotEmail are non-secret commit attribution carried to the
	// sandbox because the worker owns local deterministic commits.
	BotUser     string                 `json:"bot_user"`
	BotEmail    string                 `json:"bot_email"`
	Models      map[string]string      `json:"models"`
	ModelLimits map[string]ModelLimits `json:"model_limits,omitempty"`
	Budgets     Budgets                `json:"budgets"`
	// MaxRetries is the global retry/remediation cap workflow stages read
	// (remediate's round cap via Repo.EffectiveMaxRetries). It is carried to the
	// worker for the same reason as Budgets: the stage that enforces it runs
	// inside archie-agent, not the daemon.
	MaxRetries   int        `json:"max_retries"`
	Dispatch     Dispatch   `json:"dispatch"`
	DiffCapLines int        `json:"diff_cap_lines"`
	Notify       Notify     `json:"notify"`
	Forge        TaskForge  `json:"forge"`
	ToolPolicy   ToolPolicy `json:"tool_policy"`
	Review       Review     `json:"review"`
}

// DiffCapOf returns a DiffCapLines value for n.
//
//go:fix inline
func DiffCapOf(n int) *int { return new(n) }

// DiffCap returns the changed-line cap; 0 means no cap.
func (c Config) DiffCap() int {
	if c.DiffCapLines == nil {
		return 0
	}
	return *c.DiffCapLines
}

// TaskForge is the non-secret forge configuration needed by workflow stages.
type TaskForge struct {
	Host string `json:"host"`
}

// ForTask returns a detached, non-secret snapshot of the configuration fields
// needed to run a workflow.
func (c Config) ForTask() TaskConfig {
	return TaskConfig{
		BotUser:      c.BotUser,
		BotEmail:     c.BotEmail,
		Models:       cloneStringMap(c.Models),
		ModelLimits:  maps.Clone(c.ModelLimits),
		Budgets:      c.Budgets,
		MaxRetries:   c.MaxRetries,
		Dispatch:     Dispatch{Trigger: c.Dispatch.Trigger, AckReaction: c.Dispatch.AckReaction},
		DiffCapLines: c.DiffCap(),
		Notify:       c.Notify,
		Forge:        TaskForge{Host: c.Forge.Host},
		ToolPolicy:   c.Tools.Policy,
		Review:       c.Review,
	}
}

// ToConfig expands a TaskConfig back into a Config with only the carried
// fields populated  --  everything else (Repos, NATS, Containers, Providers,
// secrets) is zero. Used by archie-agent to reconstruct the Config value
// workflow stages read via TaskContext.Cfg.
func (tc TaskConfig) ToConfig() Config {
	return Config{
		BotUser:      tc.BotUser,
		BotEmail:     tc.BotEmail,
		Models:       cloneStringMap(tc.Models),
		ModelLimits:  maps.Clone(tc.ModelLimits),
		Budgets:      tc.Budgets,
		MaxRetries:   tc.MaxRetries,
		Dispatch:     Dispatch{Trigger: tc.Dispatch.Trigger, AckReaction: tc.Dispatch.AckReaction},
		DiffCapLines: &tc.DiffCapLines,
		Notify:       tc.Notify,
		Forge:        Forge{Host: tc.Forge.Host},
		Tools:        ToolsConfig{Policy: tc.ToolPolicy},
		Review:       tc.Review,
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

// Clone returns a deep copy of c.
func (c Config) Clone() Config {
	c.Models = cloneStringMap(c.Models)
	c.ModelLimits = maps.Clone(c.ModelLimits)
	c.Providers = maps.Clone(c.Providers)
	c.Repos = cloneRepos(c.Repos)
	c.Identities = cloneIdentities(c.Identities)
	c.Chat.Models = append([]string(nil), c.Chat.Models...)
	c.Chat.Telegram.AllowedUserIDs = append([]int64(nil), c.Chat.Telegram.AllowedUserIDs...)
	c.Bindings.PreviousEncryptionKeys = append([]SecretRef(nil), c.Bindings.PreviousEncryptionKeys...)
	c.Tools.MCPServers = cloneMCPServers(c.Tools.MCPServers)
	// Services is a map of structs, so the header is shared by the value copy
	// above and yaml.Unmarshal writes keys into whatever map it is handed. Without
	// this line an overlay -- or a dashboard PATCH -- rewrites the PUBLISHED
	// snapshot in place, which is the mutation Clone exists to prevent.
	c.Services = maps.Clone(c.Services)
	if c.Tools.WebFetch.Enabled != nil {
		v := *c.Tools.WebFetch.Enabled
		c.Tools.WebFetch.Enabled = &v
	}
	c.DiffCapLines = cloneIntPtr(c.DiffCapLines)
	return c
}

// cloneIntPtr copies an optional int so a clone cannot write through to the
// original. Optional ints carry a real meaning in their zero value (see
// DiffCapLines), which is why they are pointers in the first place.
func cloneIntPtr(v *int) *int {
	if v == nil {
		return nil
	}
	n := *v
	return &n
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

// NATSConfig configures NATS JetStream. Mode is "embedded" (default) or
// "external", which requires URL; empty Mode is external when URL is set.
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

// CaptureConfig configures the unbound webhook capture endpoint.
// All fields have defaults; an empty
// [capture] section is valid and produces them -- capture is on by default.
type CaptureConfig struct {
	// Retention is how long a captured event is kept. Zero means 7 days.
	Retention Duration `toml:"retention" yaml:"retention"`
	// MaxEvents caps the table at this many newest rows; older rows are
	// pruned on every insert regardless of age. Zero means the 5000 default.
	MaxEvents int `toml:"max_events" yaml:"max_events"`
	// MaxBodyBytes rejects (413) any single POST body larger than this
	// before it is read into memory. Zero means the 256 KiB default.
	MaxBodyBytes int `toml:"max_body_bytes" yaml:"max_body_bytes"`
	// RatePerSecond and RateBurst configure the per-remote-address rate limit on
	// capture requests. Zero means 1 req/s, burst 5.
	RatePerSecond float64 `toml:"rate_per_second" yaml:"rate_per_second"`
	RateBurst     int     `toml:"rate_burst" yaml:"rate_burst"`
}

// BindingsConfig configures the playbook-binding store (t2db). When
// EncryptionKey is unset, binding secrets are persisted as plaintext
// (legacy behaviour); when set, they are AES-256-GCM encrypted at rest
type BindingsConfig struct {
	// EncryptionKey is the active key that seals new binding secrets. It must
	// name the env engine: the State Store resolves it before extension engines
	// exist.
	// Resolved through the secret registry (engine + key) at startup; the
	// resolved material must be 32 bytes of high-entropy randomness.
	// A nil/empty ref disables encryption and keeps current behaviour.
	EncryptionKey SecretRef `toml:"encryption_key" yaml:"encryption_key"`
	// PreviousEncryptionKeys retains older key material so rows sealed
	// under a previous rotation still decrypt. Each is resolved the same
	// way as EncryptionKey; entries are decrypt-only.
	PreviousEncryptionKeys []SecretRef `toml:"previous_encryption_keys" yaml:"previous_encryption_keys"`
}

// ContainerConfig configures Docker sandbox execution of archie-agent.
type ContainerConfig struct {
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
	// Network is the Docker network agent containers join. Empty uses the
	// daemon's own network, then the default bridge.
	Network string `toml:"network" yaml:"network"`
	// Profiles are named agent profiles a workflow selects with `profile:`.
	// Stored as their own control-plane resource.
	Profiles map[string]AgentProfile `toml:"profiles" yaml:"profiles" json:"-"`
	// Credentials binds Kit credential service names to org secrets. Stored as
	// their own control-plane resource.
	Credentials []CredentialBinding `toml:"credentials" yaml:"credentials" json:"-"`
	// RegistryAuth resolves to a Docker registry.AuthConfig JSON document for
	// pulling from a private registry. Zero means anonymous pulls. File-only;
	// needs a restart.
	RegistryAuth SecretRef `toml:"registry_auth" yaml:"registry_auth" json:"-"`
}

// CredentialBinding maps one Kit credential@1 service name to an org secret.
type CredentialBinding struct {
	// Service names the credential@1 service a Kit's descriptor declares
	// (docker/sandbox-kit-spec spec.CredentialCapability.Service).
	Service string `toml:"service" yaml:"service"`
	// Org is the org this binding belongs to. Empty means the default org. A
	// run whose identity's Org does not match never resolves this binding,
	// however it is granted.
	Org string `toml:"org" yaml:"org"`
	// Secret is where an API key's real value is resolved from. A service
	// the Kit declares OAuth-managed leaves it empty: its tokens are the
	// org's captured harness secret in the State Store.
	Secret SecretRef `toml:"secret" yaml:"secret"`
}

// BoundCredentials returns the bindings a run may use: services that are
// declared, granted, and belong to org.
func (c ContainerConfig) BoundCredentials(org string, granted, declared []string) map[string]CredentialBinding {
	grantedSet := make(map[string]bool, len(granted))
	for _, g := range granted {
		grantedSet[g] = true
	}
	declaredSet := make(map[string]bool, len(declared))
	for _, d := range declared {
		declaredSet[d] = true
	}
	out := make(map[string]CredentialBinding)
	for _, b := range c.Credentials {
		if b.Org != org || !grantedSet[b.Service] || !declaredSet[b.Service] {
			continue
		}
		out[b.Service] = b
	}
	return out
}

// CredentialAccess returns the org and granted credential services of the
// identity named name: the root's, each overridden by the identity's own when
// it sets one. An unknown name is the root.
func (c Config) CredentialAccess(name string) (org string, granted []string) {
	org, granted = c.Org, c.GrantedCredentials
	for _, id := range c.Identities {
		if id.Name != name {
			continue
		}
		if id.Org != "" {
			org = id.Org
		}
		if id.GrantedCredentials != nil {
			granted = *id.GrantedCredentials
		}
	}
	return org, granted
}

// ValidateCredentialBindings rejects a binding with an empty service name or
// duplicated (service, org) pair: two bindings resolving the same Kit
// credential in the same org is ambiguous, not a fallback chain.
func (c ContainerConfig) ValidateCredentialBindings() error {
	seen := make(map[string]bool, len(c.Credentials))
	for _, b := range c.Credentials {
		if strings.TrimSpace(b.Service) == "" {
			return fmt.Errorf("containers.credentials: a service name must not be empty")
		}
		key := b.Org + "\x00" + b.Service
		if seen[key] {
			return fmt.Errorf("containers.credentials: service %q is bound more than once for org %q", b.Service, b.Org)
		}
		seen[key] = true
	}
	return nil
}

// AgentProfile is a named agent execution environment. A workflow naming an
// unknown profile parks at dispatch.
type AgentProfile struct {
	// Image is the container image; empty means [containers].image.
	Image string `toml:"image" yaml:"image"`
	// Kit makes this a harness profile: one workload Kit or published Kit
	// set, pinned by digest. Mixins are composed by publishing a set, which
	// merges their layers into one image. The task container runs that
	// image and every agent stage runs on its CLI. Exclusive with Image.
	Kit string `toml:"kit" yaml:"kit"`
	// Adapter names the output adapter reading the Kit's CLI; empty runs it
	// with no capture tools and no usage.
	Adapter string `toml:"adapter" yaml:"adapter"`
	// Tools allowlists the tools archie adds to the agent (MCP servers,
	// repository scripts and skill plugins) by name. Empty allows them all.
	// The agent loop's own file tools are always present, read-only when a
	// step asks for that.
	Tools []string `toml:"tools" yaml:"tools"`
}

// Profile resolves the profile a workflow names: "" is the default profile,
// and an unconfigured name is an error.
func (c ContainerConfig) Profile(name string) (AgentProfile, error) {
	if name == "" {
		return AgentProfile{Image: c.Image}, nil
	}
	p, ok := c.Profiles[name]
	if !ok {
		return AgentProfile{}, fmt.Errorf("agent profile %q is not configured", name)
	}
	if p.Image == "" && !p.IsKit() {
		p.Image = c.Image
	}
	return p, nil
}

// IsKit reports whether the profile runs a Kit harness.
func (p AgentProfile) IsKit() bool { return p.Kit != "" }

// ValidateProfiles rejects a profile with an empty name, an empty tool
// name, or a Kit that is not pinned by digest or also names an image.
func (c ContainerConfig) ValidateProfiles() error {
	return ValidateAgentProfiles(c.Profiles)
}

// ValidateAgentProfiles validates agent profiles.
func ValidateAgentProfiles(profiles map[string]AgentProfile) error {
	for name, p := range profiles {
		if strings.TrimSpace(name) == "" {
			return fmt.Errorf("containers.profiles: a profile name must not be empty")
		}
		switch {
		case p.IsKit() && p.Image != "":
			return fmt.Errorf("containers.profiles.%s: image and kit are exclusive; a Kit profile runs the Kit's image", name)
		case !p.IsKit() && p.Adapter != "":
			return fmt.Errorf("containers.profiles.%s: adapter needs a kit", name)
		}
		if p.IsKit() && !strings.Contains(p.Kit, "@sha256:") {
			return fmt.Errorf("containers.profiles.%s.kit: %q must be pinned by digest", name, p.Kit)
		}
		for _, tool := range p.Tools {
			if strings.TrimSpace(tool) == "" {
				return fmt.Errorf("containers.profiles.%s.tools: a tool name must not be empty", name)
			}
		}
	}
	return nil
}

// Log configures archied's log output. Without File, logs go to stderr only.
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

// ChatConfig configures conversational front-ends. Empty (Telegram.Token.Key ==
// "") disables chat entirely.
type ChatConfig struct {
	// Operator is the name of the person this deployment assists, shown to the
	// chat agent.
	Operator string `toml:"operator" yaml:"operator"`
	// Workspace is the directory the chat agent's file and shell tools are
	// rooted at. Empty disables those tools entirely, which is the default:
	// they read, write and execute, so the directory must be a deliberate
	// choice rather than whatever the daemon happens to start in.
	Workspace string `toml:"workspace" yaml:"workspace"`
	// UnrestrictedFilesystem lets the chat agent's file tools reach any absolute
	// path. Relative paths still resolve against Workspace. Off by default.
	UnrestrictedFilesystem bool `toml:"unrestricted_filesystem" yaml:"unrestricted_filesystem"`
	// ShowToolCalls shows each completed tool call in chat replies, on every
	// channel. Off by default.
	ShowToolCalls bool `toml:"show_tool_calls" yaml:"show_tool_calls"`
	// MaxSteps caps model/tool round-trips per chat turn. Zero uses the default.
	MaxSteps int `toml:"max_steps" yaml:"max_steps"`
	// Models is the optional interactive-chat model catalog. When empty,
	// chat falls back to the distinct model references assigned to workflow
	// roles in the top-level [models] table.
	Models   []string       `toml:"models" yaml:"models"`
	Telegram TelegramConfig `toml:"telegram" yaml:"telegram"`
	// RateLimit budgets inbound messages per channel and sender. Off unless set.
	// Seeds the channel-settings resource.
	RateLimit RateLimitConfig `toml:"rate_limit" yaml:"rate_limit"`
}

// RateLimitConfig configures the inbound chat rate limiter. Disabled unless
// both fields are positive.
type RateLimitConfig struct {
	// Window is the rolling interval MaxRequests is budgeted over.
	Window time.Duration `toml:"window" yaml:"window"`
	// MaxRequests is the most inbound messages one sender may send in
	// Window. Zero (the default) disables rate limiting.
	MaxRequests int `toml:"max_requests" yaml:"max_requests"`
}

// Enabled reports whether RateLimit is configured to actually limit
// anything.
func (c RateLimitConfig) Enabled() bool {
	return c.Window > 0 && c.MaxRequests > 0
}

// TelegramConfig configures the Telegram channel. An empty AllowedUserIDs
// answers nobody.
type TelegramConfig struct {
	// AllowedUserIDs lists the Telegram user IDs permitted to talk to the
	// bot. It matches the sender (from.id), not the chat, so adding the
	// bot to a group does not grant that group's members access. Empty
	// denies everyone.
	AllowedUserIDs []int64 `toml:"allowed_user_ids" yaml:"allowed_user_ids"`
	// Token references the bot token from @BotFather through the configured
	// secret engine. Empty disables the Telegram channel.
	Token SecretRef `toml:"token" yaml:"token"`
}

// Notify configures outbound notifications (n8n webhook → email etc.).
type Notify struct {
	// Webhook receives JSON POSTs for events that need a human. Empty disables.
	Webhook string `toml:"webhook" json:"webhook" yaml:"webhook"`
}

// Web configures the observability dashboard. Health is archied's own liveness
// surface, always on.
type Health struct {
	// Listen is the address the daemon serves /healthz, /health and
	// /health/detailed on. Defaulted, never empty in a loaded config.
	Listen string `toml:"listen" yaml:"listen"`
	// DependencyTimeout bounds one readiness probe's call to a dependency.
	DependencyTimeout Duration `toml:"dependency_timeout" yaml:"dependency_timeout"`
}

// URL renders Listen as an address a local caller can dial, which is what
// the daemon hands its update tooling (see releaseupdate.CommandInstaller).
// A wildcard bind is a valid thing to listen on but not to dial, so it
// resolves to localhost, the same substitution the dashboard link makes.
func (h Health) URL() string {
	listen := strings.TrimSpace(h.Listen)
	if listen == "" {
		return ""
	}
	host, port, err := net.SplitHostPort(listen)
	if err != nil {
		host, port = "", strings.TrimPrefix(listen, ":")
	}
	switch host {
	case "", "0.0.0.0", "::", "[::]":
		host = "localhost"
	}
	return "http://" + net.JoinHostPort(host, port)
}

type Web struct {
	// Listen is the dashboard address; "off" disables the web UI.
	// Bind localhost (or a LAN/tailnet address)  --  there is no auth.
	Listen string `toml:"listen" yaml:"listen"`
	// TrustForwardedHeaders uses X-Forwarded-Proto and X-Forwarded-Host for
	// Origin checks. Enable only behind a trusted reverse proxy.
	TrustForwardedHeaders bool `toml:"trust_forwarded_headers" yaml:"trust_forwarded_headers"`
}
