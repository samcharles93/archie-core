package configuration

import (
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/secret"
	"github.com/samcharles93/archie-core/internal/tools"
)

// Defaults applied to absent input. Named so a reader can find the value
// without reading the code that applies it.
const (
	defaultPollInterval    = 60 * time.Second
	defaultDiffCapLines    = 400
	defaultWebListen       = "127.0.0.1:8484" // "off" disables
	defaultMaxRetries      = 3
	defaultForgeType       = "github"
	defaultForgeHost       = "https://github.com"
	defaultAgentCommand    = "archie-agent"
	defaultDispatchTrigger = "assignee"
	defaultAckReaction     = "eyes"
	defaultContainerUptime = 60 * time.Minute
	defaultVolumeTTL       = 72 * time.Hour
	defaultPullPolicy      = "missing"

	// defaultMaxResultChars bounds one tool result. It matches the 50KB the
	// lifted workspace tools already truncate themselves at
	// (tools/builtin.DefaultMaxBytes), so those stay the binding limit and
	// only tools with no truncation of their own -- skill bodies, MCP
	// results -- see a change.
	defaultMaxResultChars = 50_000

	defaultWebFetchTimeout = 30 * time.Second
	// defaultWebFetchMaxBytes bounds a fetched body before the result limit
	// above trims it further. Generous enough for a documentation page,
	// small enough that a large download cannot occupy the daemon.
	defaultWebFetchMaxBytes = 2_000_000

	// Capture defaults, per docs/prds/event-capture-storage.md's disk-bound
	// mechanics: retention + row cap + per-request size cap + per-source
	// rate limit, composed.
	defaultCaptureRetention     = 7 * 24 * time.Hour
	defaultCaptureMaxEvents     = 5000
	defaultCaptureMaxBodyBytes  = 256 * 1024
	defaultCaptureRatePerSecond = 1.0
	defaultCaptureRateBurst     = 5
)

// applyDefaults fills in every absent value. It never reports an error and
// never rejects input.
//
// Defaulting and validation were previously interleaved: functions named
// applyXDefaults also returned validation errors, and functions named
// validateX quietly mutated the config. That made it impossible to answer
// "what did the operator actually set?" -- the only way to see the supplied
// input was to not call them. Splitting them means defaults can be applied,
// inspected, and reasoned about independently of whether the result is valid.
func (l *Loader) applyDefaults(cfg *config.Config) {
	l.applyGeneralDefaults(cfg)
	applyForgeDefaults(cfg)
	applyAgentDefaults(cfg)
	applyDispatchDefaults(cfg)
	applyIdentityDefaults(cfg)
	applyContainerDefaults(cfg)
	applyToolPolicyDefaults(cfg)
	applyCaptureDefaults(cfg)
}

// applyCaptureDefaults fills the webhook capture endpoint's settings. See
// [config.CaptureConfig].
func applyCaptureDefaults(cfg *config.Config) {
	if cfg.Capture.Retention == 0 {
		cfg.Capture.Retention = config.Duration(defaultCaptureRetention)
	}
	if cfg.Capture.MaxEvents == 0 {
		cfg.Capture.MaxEvents = defaultCaptureMaxEvents
	}
	if cfg.Capture.MaxBodyBytes == 0 {
		cfg.Capture.MaxBodyBytes = defaultCaptureMaxBodyBytes
	}
	if cfg.Capture.RatePerSecond == 0 {
		cfg.Capture.RatePerSecond = defaultCaptureRatePerSecond
	}
	if cfg.Capture.RateBurst == 0 {
		cfg.Capture.RateBurst = defaultCaptureRateBurst
	}
}

// applyToolPolicyDefaults fills the tool result limits.
//
// Zero means "unset" and takes the default; a negative value is the operator
// saying "no limit" and is left alone. See [config.ToolPolicy].
func applyToolPolicyDefaults(cfg *config.Config) {
	if cfg.Tools.Policy.MaxResultChars == 0 {
		cfg.Tools.Policy.MaxResultChars = defaultMaxResultChars
	}
	if cfg.Tools.Policy.TurnBudgetChars == 0 {
		cfg.Tools.Policy.TurnBudgetChars = tools.DefaultTurnBudgetChars
	}
	// SpillDir is deliberately NOT defaulted.
	//
	// Spilling hands the model a file path to read back, and the read tool is
	// confined to chat.workspace. A default under work_dir is outside that for
	// any workspace narrower than the whole home directory, and always outside
	// a task agent's worktree -- so the model would be given a path it cannot
	// open and the result would be lost rather than displaced. Truncating
	// inline at least shows the first part of the result and says so.
	//
	// An operator who wants spilling points this at a directory inside the
	// workspace; startup warns when it is not (see cmd/archied).
	applyWebFetchDefaults(cfg)
}

// applyWebFetchDefaults fills the fetch tool's settings. Enabled is left alone
// when the operator set it either way; see [config.WebFetchConfig].
func applyWebFetchDefaults(cfg *config.Config) {
	if cfg.Tools.WebFetch.Timeout == 0 {
		cfg.Tools.WebFetch.Timeout = config.Duration(defaultWebFetchTimeout)
	}
	if cfg.Tools.WebFetch.MaxBytes == 0 {
		cfg.Tools.WebFetch.MaxBytes = defaultWebFetchMaxBytes
	}
}

// DefaultWorkDir returns the work directory an unset work_dir would have been
// given. Callers that must fall back when a configured path turns out to be
// unusable use this so the fallback is the same location the operator would
// have got by saying nothing, rather than a second guess that only this
// caller knows about.
func DefaultWorkDir() string {
	return filepath.Join(xdgDataHome(), "archie", "work")
}

// applyGeneralDefaults derives the paths and limits that have no owning
// section.
func (l *Loader) applyGeneralDefaults(cfg *config.Config) {
	cfg.SecretEngineDir = expandHomePath(cfg.SecretEngineDir)
	cfg.WorkDir = expandHomePath(cfg.WorkDir)
	cfg.DBPath = expandHomePath(cfg.DBPath)
	cfg.Chat.Workspace = expandHomePath(cfg.Chat.Workspace)
	if cfg.WorkDir == "" {
		cfg.WorkDir = filepath.Join(l.dataHome(), "archie", "work")
	}
	if cfg.DBPath == "" {
		cfg.DBPath = filepath.Join(l.dataHome(), "archie", "archie.db")
	}
	cfg.Indexing = cfg.Indexing.WithDefaults(cfg.WorkDir)
	if cfg.PollInterval == 0 {
		cfg.PollInterval = config.Duration(defaultPollInterval)
	}
	if cfg.DiffCapLines == 0 {
		cfg.DiffCapLines = defaultDiffCapLines
	}
	if cfg.Web.Listen == "" {
		cfg.Web.Listen = defaultWebListen
	}
	if cfg.MaxRetries == 0 {
		cfg.MaxRetries = defaultMaxRetries
	}
}

func expandHomePath(path string) string {
	if path != "~" && !strings.HasPrefix(path, "~/") {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	if path == "~" {
		return home
	}
	return filepath.Join(home, strings.TrimPrefix(path, "~/"))
}

// applyForgeDefaults fills the forge section, promoting the legacy
// token_env field to a secret reference when no explicit one was given.
func applyForgeDefaults(cfg *config.Config) {
	if cfg.Forge.Type == "" {
		cfg.Forge.Type = defaultForgeType
	}
	if cfg.Forge.Host == "" {
		cfg.Forge.Host = defaultForgeHost
	}
	if cfg.Forge.Token.Engine == "" && cfg.Forge.Token.Key == "" && cfg.Forge.TokenEnv != "" {
		cfg.Forge.Token = secret.SecretRef{Engine: "env", Key: cfg.Forge.TokenEnv}
	}
}

// applyAgentDefaults picks the execution mode. Containers require the NATS
// transport, so enabling them selects it unless the operator said otherwise.
func applyAgentDefaults(cfg *config.Config) {
	if cfg.Agent.Mode == "" {
		if cfg.Containers.Enabled {
			cfg.Agent.Mode = agentModeNATS
		} else {
			cfg.Agent.Mode = agentModeInProcess
		}
	}
	if cfg.Agent.Command == "" {
		cfg.Agent.Command = defaultAgentCommand
	}
}

// applyDispatchDefaults fills the dispatch section. "off" is the documented
// way to disable the acknowledgement reaction, and normalises to empty.
func applyDispatchDefaults(cfg *config.Config) {
	if cfg.Dispatch.Trigger == "" {
		cfg.Dispatch.Trigger = defaultDispatchTrigger
	}
	switch {
	case cfg.Dispatch.AckReaction == "":
		cfg.Dispatch.AckReaction = defaultAckReaction
	case isOff(cfg.Dispatch.AckReaction):
		cfg.Dispatch.AckReaction = ""
	}
	if cfg.Dispatch.Labels == nil {
		cfg.Dispatch.Labels = map[string]string{}
	}
	for k, v := range config.DispatchLabelDefaults() {
		if cfg.Dispatch.Labels[k] == "" {
			cfg.Dispatch.Labels[k] = v
		}
	}
}

// applyIdentityDefaults derives the commit email for a single-identity
// deployment. Multi-identity deployments configure each identity explicitly.
func applyIdentityDefaults(cfg *config.Config) {
	if len(cfg.Identities) > 0 || cfg.BotUser == "" || cfg.BotEmail != "" {
		return
	}
	switch cfg.Forge.Type {
	case forgeTypeGitHub:
		cfg.BotEmail = cfg.BotUser + "@users.noreply.github.com"
	case forgeTypeGitea:
		cfg.BotEmail = cfg.BotUser + "@gitea.local"
	}
}

// applyContainerDefaults fills the container lifetimes. A negative VolumeTTL
// is left alone for validation to reject rather than silently corrected.
func applyContainerDefaults(cfg *config.Config) {
	if !cfg.Containers.Enabled {
		return
	}
	if cfg.Containers.MaxUptime == 0 {
		cfg.Containers.MaxUptime = config.Duration(defaultContainerUptime)
	}
	if cfg.Containers.VolumeTTL == 0 {
		cfg.Containers.VolumeTTL = config.Duration(defaultVolumeTTL)
	}
	if cfg.Containers.PullPolicy == "" {
		cfg.Containers.PullPolicy = defaultPullPolicy
	}
}
