package configuration

import (
	"fmt"
	"net/url"
	"path/filepath"
	"slices"
	"strings"

	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/secret"
)

// Recognised enum values, named so validation and its error message cannot
// disagree about what is allowed.
const (
	agentModeInProcess  = "inprocess"
	agentModeSubprocess = "subprocess"
	agentModeNATS       = "nats"

	forgeTypeGitHub   = "github"
	forgeTypeGitea    = "gitea"
	forgeTypeNone     = "none"
	forgeTypeOff      = "off"
	forgeTypeDisabled = "disabled"

	dispatchTriggerAssignee = "assignee"
	dispatchTriggerLabel    = "label"
	dispatchTriggerEither   = "either"
)

var (
	agentModes       = []string{agentModeInProcess, agentModeSubprocess, agentModeNATS}
	forgeTypes       = []string{forgeTypeGitHub, forgeTypeGitea, forgeTypeNone, forgeTypeOff, forgeTypeDisabled}
	dispatchTriggers = []string{dispatchTriggerAssignee, dispatchTriggerLabel, dispatchTriggerEither}
)

// Validate runs the same checks Loader applies before accepting a config,
// against a config.Config value already built in memory -- e.g. by archied
// setup, before it has written anything to disk.
//
// Like validate, it does not apply defaults. Several checks (agent.mode,
// dispatch.trigger, forge.type) only pass once a default has been filled
// in, and defaulting is currently unexported ((*Loader).applyDefaults). A
// cfg built by hand, with those fields left zero-valued, will fail
// validation that a config loaded through [Loader.File] would pass. Callers
// that build a config directly (rather than decoding one through a Loader)
// must fill in the same defaults themselves, or -- the safer option, since
// it can't drift from what the real load path does -- render the config to
// TOML and load it back through a Loader instead of calling Validate on the
// in-memory value directly.
func Validate(cfg *config.Config) error {
	return validate(cfg)
}

// validate reports the first problem that would stop the daemon running.
// It does not modify cfg -- run applyDefaults first.
func validate(cfg *config.Config) error {
	if err := validateAgent(cfg); err != nil {
		return err
	}
	if err := validateDispatch(cfg); err != nil {
		return err
	}
	if err := validateProviders(cfg.Providers); err != nil {
		return err
	}
	if len(cfg.Identities) > 0 {
		if err := validateIdentities(cfg.Identities); err != nil {
			return err
		}
	} else if err := validateSingleIdentity(cfg); err != nil {
		return err
	}
	if err := validatePollInterval(cfg); err != nil {
		return err
	}
	return validateContainers(cfg)
}

// validatePollInterval rejects a non-positive poll interval.
// applyDefaults fills PollInterval == 0 with the default, so this only
// fires on an explicit negative value -- which would panic
// time.NewTicker/Reset in the daemon's poll loops, at boot and (worse)
// on a SIGHUP reload mid-run, when the edit that broke it is no longer
// fresh in the operator's mind.
func validatePollInterval(cfg *config.Config) error {
	if cfg.PollInterval <= 0 {
		return fmt.Errorf("%w: poll_interval must be positive", ErrInvalidInput)
	}
	return nil
}

func validateAgent(cfg *config.Config) error {
	if !oneOf(cfg.Agent.Mode, agentModes) {
		return fmt.Errorf("%w: agent.mode %q (want %s)", ErrInvalidInput, cfg.Agent.Mode, list(agentModes))
	}
	if cfg.Agent.Mode == agentModeSubprocess && strings.TrimSpace(cfg.Agent.Command) == "" {
		return fmt.Errorf("%w: agent.command is required in subprocess mode", ErrInvalidInput)
	}
	for i, name := range cfg.Agent.Env {
		if strings.TrimSpace(name) == "" || strings.Contains(name, "=") {
			return fmt.Errorf("%w: agent.env[%d] %q is not an environment variable name", ErrInvalidInput, i, name)
		}
	}
	return nil
}

// validateDispatch rejects a "label" or "either" trigger left with no label
// to match. GitHub's issues-list API treats an empty label filter as no
// filter at all and returns every open issue in the repo -- a live incident
// (GH#445) queued 124 unrelated issues in one poll cycle this way, after
// [dispatch.labels] (a different field, mapping task states to their own
// labels) was configured while the actual trigger-match label was left
// blank.
func validateDispatch(cfg *config.Config) error {
	if !oneOf(cfg.Dispatch.Trigger, dispatchTriggers) {
		return fmt.Errorf("%w: dispatch.trigger %q (want %s)", ErrInvalidInput, cfg.Dispatch.Trigger, list(dispatchTriggers))
	}
	if (cfg.Dispatch.Trigger == dispatchTriggerLabel || cfg.Dispatch.Trigger == dispatchTriggerEither) && cfg.Label == "" {
		return fmt.Errorf("%w: label is required when dispatch.trigger is %q (an empty label matches every open issue)", ErrInvalidInput, cfg.Dispatch.Trigger)
	}
	return nil
}

// validateProviders rejects base URLs carrying credentials or query state.
// A provider URL is used to build API requests, so userinfo or a stray query
// string would be silently attached to every call.
func validateProviders(providers map[string]config.Provider) error {
	for name, provider := range providers {
		if provider.BaseURL == "" {
			continue
		}
		u, err := url.Parse(provider.BaseURL)
		if err != nil {
			return fmt.Errorf("%w: providers.%s.base_url: %w", ErrInvalidInput, name, err)
		}
		if u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			return fmt.Errorf("%w: providers.%s.base_url must not contain userinfo, query parameters, or a fragment", ErrInvalidInput, name)
		}
	}
	return nil
}

func validateIdentities(identities []config.IdentityConfig) error {
	for i, id := range identities {
		if id.Name == "" {
			return fmt.Errorf("%w: identities[%d].name is required", ErrInvalidInput, i)
		}
		if id.BotUser == "" {
			return fmt.Errorf("%w: identities[%d].bot_user is required", ErrInvalidInput, i)
		}
		if len(id.Repos) == 0 {
			return fmt.Errorf("%w: identities[%d] has no [[identities.repos]] entries -- at least one is required", ErrInvalidInput, i)
		}
		if !oneOf(id.Forge.Type, forgeTypes) {
			return fmt.Errorf("%w: identities[%d].forge.type %q (want %s)", ErrInvalidInput, i, id.Forge.Type, list(forgeTypes))
		}
		if !ForgeDisabled(id.Forge.Type) && id.Forge.Token == (secret.SecretRef{}) {
			return fmt.Errorf("%w: identities[%d].forge.token is required (each identity needs its own secret reference; unlike the top-level [forge], there is no default)", ErrInvalidInput, i)
		}
		if err := validateRepos(id.Repos); err != nil {
			return fmt.Errorf("identities[%d]: %w", i, err)
		}
	}
	return nil
}

// ForgeDisabled reports whether a forge type explicitly opts out of forge
// integration. It is the single definition shared by config validation and
// cmd/archied's resolveForge: adding or removing an alias here applies to
// both paths together. It is not consulted by forge.New, which has its own
// construction-time dispatch for disabled types (and the empty string);
// that dispatch is a different concern from this validation predicate.
func ForgeDisabled(t string) bool {
	return t == forgeTypeNone || t == forgeTypeOff || t == forgeTypeDisabled
}

func validateSingleIdentity(cfg *config.Config) error {
	if cfg.BotUser == "" {
		return fmt.Errorf("%w: bot_user is required (or define [[identities]])", ErrInvalidInput)
	}
	if !oneOf(cfg.Forge.Type, forgeTypes) {
		return fmt.Errorf("%w: forge.type %q (want %s)", ErrInvalidInput, cfg.Forge.Type, list(forgeTypes))
	}
	return validateRepos(cfg.Repos)
}

func validateRepos(repos []config.Repo) error {
	for i, r := range repos {
		if r.Owner == "" || r.Name == "" {
			return fmt.Errorf("%w: repos[%d] needs owner and name", ErrInvalidInput, i)
		}
		if glob := r.ResolvedTestGlob(); glob != "" {
			if _, err := filepath.Match(glob, ""); err != nil {
				return fmt.Errorf("%w: repos[%d] test_glob %q: %w", ErrInvalidInput, i, glob, err)
			}
		}
	}
	return nil
}

// validateContainers checks the combination that container execution
// requires: a NATS transport and somewhere to reach it.
func validateContainers(cfg *config.Config) error {
	if !cfg.Containers.Enabled {
		return nil
	}
	if cfg.Containers.Image == "" {
		return fmt.Errorf("%w: containers.image is required when containers.enabled is true", ErrInvalidInput)
	}
	if cfg.Agent.Mode != agentModeNATS {
		return fmt.Errorf("%w: agent.mode must be %q when containers.enabled is true", ErrInvalidInput, agentModeNATS)
	}
	if cfg.NATS.URL == "" {
		return fmt.Errorf("%w: [nats] url is required when containers.enabled is true", ErrInvalidInput)
	}
	if cfg.Containers.VolumeTTL < 0 {
		return fmt.Errorf("%w: containers.volume_ttl must not be negative", ErrInvalidInput)
	}
	return nil
}

// oneOf reports whether value is in allowed.
func oneOf(value string, allowed []string) bool {
	return slices.Contains(allowed, value)
}

// list renders allowed values for an error message.
func list(values []string) string { return strings.Join(values, ", ") }

// isOff reports the documented "disable this" spelling.
func isOff(value string) bool { return strings.EqualFold(value, "off") }
