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
	forgeTypes       = []string{forgeTypeGitHub, forgeTypeGitea, forgeTypeNone, forgeTypeOff, forgeTypeDisabled}
	dispatchTriggers = []string{dispatchTriggerAssignee, dispatchTriggerLabel, dispatchTriggerEither}
	natsModes        = []string{config.NATSModeEmbedded, config.NATSModeExternal, config.NATSModeOff}
	forgeIntakes     = []string{config.ForgeIntakePoll, config.ForgeIntakeWebhook, config.ForgeIntakeBoth}
)

// Validate runs the same checks Loader applies before accepting a config,
// against a config.Config value already built in memory -- e.g. by archied
// setup, before it has written anything to disk.
//
// Like validate, it does not apply defaults. Several checks (dispatch.trigger,
// forge.type) only pass once a default has been filled
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
	if err := validateDispatch(cfg); err != nil {
		return err
	}
	if err := validateForgeIntake(cfg); err != nil {
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
	if err := validateNATS(cfg); err != nil {
		return err
	}
	if err := validateContainers(cfg); err != nil {
		return err
	}
	return validateCapture(cfg)
}

// validateCapture rejects negative capture settings. applyDefaults fills
// every zero value with a positive default, so this only fires on an
// explicit negative value -- which would make time.Duration/AddDate math
// and the token-bucket rate limiter behave nonsensically (e.g. a negative
// retention window would prune every row on every insert, including the one
// just written).
func validateCapture(cfg *config.Config) error {
	if cfg.Capture.Retention < 0 {
		return fmt.Errorf("%w: capture.retention must not be negative", ErrInvalidInput)
	}
	if cfg.Capture.MaxEvents < 0 {
		return fmt.Errorf("%w: capture.max_events must not be negative", ErrInvalidInput)
	}
	if cfg.Capture.MaxBodyBytes < 0 {
		return fmt.Errorf("%w: capture.max_body_bytes must not be negative", ErrInvalidInput)
	}
	if cfg.Capture.RatePerSecond < 0 {
		return fmt.Errorf("%w: capture.rate_per_second must not be negative", ErrInvalidInput)
	}
	if cfg.Capture.RateBurst < 0 {
		return fmt.Errorf("%w: capture.rate_burst must not be negative", ErrInvalidInput)
	}
	return nil
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

// validateForgeIntake checks the forge intake mode and that webhook intake has
// the secret and listen address it needs. An unset intake resolves to "poll"
// without mutating cfg, so Validate (which does not apply defaults) accepts a
// hand-built config the same way Loader.File accepts its on-disk form.
func validateForgeIntake(cfg *config.Config) error {
	intake := cfg.Forge.Intake
	if intake == "" {
		intake = config.ForgeIntakePoll
	}
	if !oneOf(intake, forgeIntakes) {
		return fmt.Errorf("%w: forge.intake %q (want %s)", ErrInvalidInput, cfg.Forge.Intake, list(forgeIntakes))
	}
	if intake == config.ForgeIntakeWebhook || intake == config.ForgeIntakeBoth {
		if cfg.Forge.WebhookSecret == (secret.SecretRef{}) {
			return fmt.Errorf("%w: forge.webhook_secret is required when forge.intake is %q", ErrInvalidInput, intake)
		}
		if cfg.Forge.WebhookAddr == "" {
			return fmt.Errorf("%w: forge.webhook_addr is required when forge.intake is %q", ErrInvalidInput, intake)
		}
		// Webhook intake publishes to the task queue, which is NATS (embedded
		// or external). nats.mode="off" leaves no queue to publish to.
		if effectiveNATSMode(cfg) == config.NATSModeOff {
			return fmt.Errorf("%w: forge.intake %q requires NATS (nats.mode must not be %q)", ErrInvalidInput, intake, config.NATSModeOff)
		}
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

// effectiveNATSMode resolves an unset nats.mode from url without mutating cfg,
// so validation that reads the mode never has to know whether the operator
// spelled it or left it to default. validateNATS and validateContainers both
// consult it, so they cannot disagree about which mode a config is in.
func effectiveNATSMode(cfg *config.Config) string {
	if cfg.NATS.Mode != "" {
		return cfg.NATS.Mode
	}
	if cfg.NATS.URL != "" {
		return config.NATSModeExternal
	}
	return config.NATSModeEmbedded
}

// validateNATS checks the nats mode and its consistency with url. It resolves
// an unset mode from url without mutating cfg, so Validate (which documents it
// does not apply defaults) accepts a hand-built config the same way Loader.File
// accepts its on-disk form.
func validateNATS(cfg *config.Config) error {
	mode := effectiveNATSMode(cfg)
	if !oneOf(mode, natsModes) {
		return fmt.Errorf("%w: nats.mode %q (want %s)", ErrInvalidInput, cfg.NATS.Mode, list(natsModes))
	}
	switch mode {
	case config.NATSModeExternal:
		if cfg.NATS.URL == "" {
			return fmt.Errorf("%w: nats.url is required when nats.mode is %q", ErrInvalidInput, config.NATSModeExternal)
		}
	case config.NATSModeEmbedded, config.NATSModeOff:
		if cfg.NATS.URL != "" {
			return fmt.Errorf("%w: nats.url must be empty when nats.mode is %q", ErrInvalidInput, mode)
		}
	}
	return nil
}

// validateContainers checks the combination container execution requires.
// Embedded and external NATS are both worker-reachable deployment shapes;
// composition resolves the Docker bridge gateway for embedded mode.
func validateContainers(cfg *config.Config) error {
	if !cfg.Containers.Enabled {
		return nil
	}
	if cfg.Containers.Image == "" {
		return fmt.Errorf("%w: containers.image is required when containers.enabled is true", ErrInvalidInput)
	}
	if effectiveNATSMode(cfg) == config.NATSModeOff {
		return fmt.Errorf("%w: nats.mode must not be %q when containers.enabled is true", ErrInvalidInput, config.NATSModeOff)
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
