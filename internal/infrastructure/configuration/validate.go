package configuration

import (
	"fmt"
	"net/url"
	"path/filepath"
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

	forgeTypeGitHub = "github"
	forgeTypeGitea  = "gitea"

	dispatchTriggerAssignee = "assignee"
	dispatchTriggerLabel    = "label"
	dispatchTriggerEither   = "either"
)

var (
	agentModes       = []string{agentModeInProcess, agentModeSubprocess, agentModeNATS}
	forgeTypes       = []string{forgeTypeGitHub, forgeTypeGitea}
	dispatchTriggers = []string{dispatchTriggerAssignee, dispatchTriggerLabel, dispatchTriggerEither}
)

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
	return validateContainers(cfg)
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

func validateDispatch(cfg *config.Config) error {
	if !oneOf(cfg.Dispatch.Trigger, dispatchTriggers) {
		return fmt.Errorf("%w: dispatch.trigger %q (want %s)", ErrInvalidInput, cfg.Dispatch.Trigger, list(dispatchTriggers))
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
		if id.Forge.Token == (secret.SecretRef{}) {
			return fmt.Errorf("%w: identities[%d].forge.token is required (each identity needs its own secret reference; unlike the top-level [forge], there is no default)", ErrInvalidInput, i)
		}
		if err := validateRepos(id.Repos); err != nil {
			return fmt.Errorf("identities[%d]: %w", i, err)
		}
	}
	return nil
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
	for _, a := range allowed {
		if value == a {
			return true
		}
	}
	return false
}

// list renders allowed values for an error message.
func list(values []string) string { return strings.Join(values, ", ") }

// isOff reports the documented "disable this" spelling.
func isOff(value string) bool { return strings.EqualFold(value, "off") }
