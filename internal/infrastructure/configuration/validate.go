package configuration

import (
	"fmt"
	"net/url"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/domain/identity"
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

	// memoryEngineBuiltin is the only domain/memory engine implemented so
	// far (internal/infrastructure/memory.BuiltinEngine). Extend
	// memoryEngines, not this list of one, as further engines land.
	memoryEngineBuiltin = "builtin"
)

// forgeTypePattern is a package name: forge.type names the installed forge
// package that serves the instance, or disables the forge.
var forgeTypePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

var (
	dispatchTriggers = []string{dispatchTriggerAssignee, dispatchTriggerLabel, dispatchTriggerEither}
	natsModes        = []string{config.NATSModeEmbedded, config.NATSModeExternal}
	forgeIntakes     = []string{config.ForgeIntakePoll, config.ForgeIntakeWebhook, config.ForgeIntakeBoth}
	memoryEngines    = []string{memoryEngineBuiltin}
)

// Validate checks an effective configuration, including settings the control
// plane owns. It does not apply defaults.
func Validate(cfg *config.Config) error {
	return validate(cfg)
}

// validate reports the first problem that would stop the daemon running.
// It does not modify cfg -- run applyDefaults first.
func validate(cfg *config.Config) error {
	if err := validateBootstrap(cfg); err != nil {
		return err
	}
	return validateDatabaseOwned(cfg)
}

// validateBootstrap checks the settings a file configuration still owns,
// skipping those the control plane stores.
func validateBootstrap(cfg *config.Config) error {
	if err := validateForgeIntake(cfg); err != nil {
		return err
	}
	if err := validateIdentityStructure(cfg); err != nil {
		return err
	}
	if err := validateNATS(cfg); err != nil {
		return err
	}
	if err := validateMemory(cfg); err != nil {
		return err
	}
	if err := validateCurators(cfg); err != nil {
		return err
	}
	return validateCapture(cfg)
}

// validateDatabaseOwned checks the settings the control plane owns:
// providers, scheduling, containers and repositories.
func validateDatabaseOwned(cfg *config.Config) error {
	if err := validateDispatch(cfg); err != nil {
		return err
	}
	if err := validateProviders(cfg.Providers); err != nil {
		return err
	}
	if err := validatePollInterval(cfg); err != nil {
		return err
	}
	if err := validateContainers(cfg); err != nil {
		return err
	}
	return validateRepositoryContents(cfg)
}

// validateCurators rejects a curator definition with no interval. The
// interval is a live-path value: a definition missing it is refused with a
// clear error rather than defaulted at registration or pass time, so a
// config-defined curator can never silently inherit a code constant.
func validateCurators(cfg *config.Config) error {
	for i, def := range cfg.Curators {
		if def.Interval <= 0 {
			return fmt.Errorf("%w: curators[%d].interval must be positive", ErrInvalidInput, i)
		}
	}
	return nil
}

// validateMemory rejects an engine name outside memoryEngines. Empty is
// valid input to Validate (applyDefaults resolves it to memoryEngineBuiltin
// before this runs on the real load path); it is not "unknown" so it does
// not error here.
func validateMemory(cfg *config.Config) error {
	if cfg.Memory.Engine != "" && !oneOf(cfg.Memory.Engine, memoryEngines) {
		return fmt.Errorf("%w: memory.engine %q (want %s)", ErrInvalidInput, cfg.Memory.Engine, list(memoryEngines))
	}
	return nil
}

// validateCapture rejects negative capture settings.
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

// validatePollInterval rejects a negative poll interval.
func validatePollInterval(cfg *config.Config) error {
	if cfg.PollInterval <= 0 {
		return fmt.Errorf("%w: poll_interval must be positive", ErrInvalidInput)
	}
	return nil
}

// validateDispatch rejects a "label" or "either" trigger with no label: an
// empty label filter matches every open issue.
func validateDispatch(cfg *config.Config) error {
	if !DispatchTriggerValid(cfg.Dispatch.Trigger) {
		return fmt.Errorf("%w: dispatch.trigger %q (want %s)", ErrInvalidInput, cfg.Dispatch.Trigger, list(dispatchTriggers))
	}
	if (cfg.Dispatch.Trigger == dispatchTriggerLabel || cfg.Dispatch.Trigger == dispatchTriggerEither) && cfg.Label == "" {
		return fmt.Errorf("%w: label is required when dispatch.trigger is %q (an empty label matches every open issue)", ErrInvalidInput, cfg.Dispatch.Trigger)
	}
	return nil
}

// DispatchTriggerValid reports whether trigger is a known discovery rule.
func DispatchTriggerValid(trigger string) bool {
	return oneOf(trigger, dispatchTriggers)
}

// validateForgeIntake checks the intake mode and webhook settings, and
// rejects webhook intake with [[identities]] or on an identity.
func validateForgeIntake(cfg *config.Config) error {
	intake := cfg.Forge.Intake
	if intake == "" {
		intake = config.ForgeIntakePoll
	}
	if !oneOf(intake, forgeIntakes) {
		return fmt.Errorf("%w: forge.intake %q (want %s)", ErrInvalidInput, cfg.Forge.Intake, list(forgeIntakes))
	}
	if intake == config.ForgeIntakeWebhook || intake == config.ForgeIntakeBoth {
		if len(cfg.Identities) > 0 {
			return fmt.Errorf("%w: forge.intake %q is not supported alongside [[identities]]: webhook intake is single-identity only, so every identity would silently keep polling (drop the [[identities]] blocks, or set forge.intake = %q and let each identity poll)",
				ErrInvalidInput, intake, config.ForgeIntakePoll)
		}
		if cfg.Forge.WebhookSecret == (config.SecretRef{}) {
			return fmt.Errorf("%w: forge.webhook_secret is required when forge.intake is %q", ErrInvalidInput, intake)
		}
		if cfg.Forge.WebhookAddr == "" {
			return fmt.Errorf("%w: forge.webhook_addr is required when forge.intake is %q", ErrInvalidInput, intake)
		}
	}
	return validateIdentityForgeIntake(cfg.Identities)
}

// identityIntakes are the intake modes an identity may declare.
var identityIntakes = []string{config.ForgeIntakePoll}

// validateIdentityForgeIntake rejects intake settings on an identity; only
// the root forge's are read.
func validateIdentityForgeIntake(identities []config.IdentityConfig) error {
	for i, id := range identities {
		switch id.Forge.Intake {
		case "", config.ForgeIntakePoll:
		case config.ForgeIntakeWebhook, config.ForgeIntakeBoth:
			return fmt.Errorf("%w: %s %q is not supported: per-identity webhook intake is not implemented, so this identity would silently keep polling (want %q)",
				ErrInvalidInput, identitySettingPath(i, id, "forge.intake"), id.Forge.Intake, config.ForgeIntakePoll)
		default:
			return fmt.Errorf("%w: %s %q (want %s)", ErrInvalidInput, identitySettingPath(i, id, "forge.intake"), id.Forge.Intake, list(identityIntakes))
		}
		if id.Forge.WebhookSecret != (config.SecretRef{}) {
			return fmt.Errorf("%w: %s is not supported: per-identity webhook intake is not implemented, so only the top-level forge.webhook_secret is read",
				ErrInvalidInput, identitySettingPath(i, id, "forge.webhook_secret"))
		}
		if id.Forge.WebhookAddr != "" {
			return fmt.Errorf("%w: %s is not supported: per-identity webhook intake is not implemented, so only the top-level forge.webhook_addr is read",
				ErrInvalidInput, identitySettingPath(i, id, "forge.webhook_addr"))
		}
	}
	return nil
}

// identitySettingPath renders a per-identity setting's location, with its
// name when set.
func identitySettingPath(i int, id config.IdentityConfig, field string) string {
	if id.Name == "" {
		return fmt.Sprintf("identities[%d].%s", i, field)
	}
	return fmt.Sprintf("identities[%d] (%q).%s", i, id.Name, field)
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

// validateIdentityStructure checks identity definitions, excluding their
// repository lists.
func validateIdentityStructure(cfg *config.Config) error {
	if len(cfg.Identities) == 0 {
		return validateSingleIdentity(cfg)
	}
	return validateIdentities(cfg.Identities)
}

func validateIdentities(identities []config.IdentityConfig) error {
	for i, id := range identities {
		// The name rule belongs to the identity feature, which the store path
		// also applies (identity.New). Calling it here keeps a name the file
		// accepts from being a name the store refuses.
		if !identity.ValidDisplayName(id.Name) {
			return fmt.Errorf("%w: identities[%d].name is required", ErrInvalidInput, i)
		}
		if id.BotUser == "" {
			return fmt.Errorf("%w: identities[%d].bot_user is required", ErrInvalidInput, i)
		}
		if len(id.Repos) == 0 {
			return fmt.Errorf("%w: identities[%d] has no [[identities.repos]] entries -- at least one is required", ErrInvalidInput, i)
		}
		if !forgeTypePattern.MatchString(id.Forge.Type) {
			return fmt.Errorf("%w: identities[%d].forge.type %q (want none or the name of an installed forge package)", ErrInvalidInput, i, id.Forge.Type)
		}
		if !ForgeDisabled(id.Forge.Type) && id.Forge.Token == (config.SecretRef{}) {
			return fmt.Errorf("%w: identities[%d].forge.token is required (each identity needs its own secret reference; unlike the top-level [forge], there is no default)", ErrInvalidInput, i)
		}
	}
	return nil
}

// ForgeDisabled reports whether a forge type disables forge integration.
func ForgeDisabled(t string) bool {
	return t == forgeTypeNone || t == forgeTypeOff || t == forgeTypeDisabled
}

func validateSingleIdentity(cfg *config.Config) error {
	// bot_user becomes the seeded identity's display name
	// (configuredIdentityNames -> identity.New), so it is judged by the same
	// rule rather than a second empty-string test.
	if !identity.ValidDisplayName(cfg.BotUser) {
		return fmt.Errorf("%w: bot_user is required (or define [[identities]])", ErrInvalidInput)
	}
	if !forgeTypePattern.MatchString(cfg.Forge.Type) {
		return fmt.Errorf("%w: forge.type %q (want none or the name of an installed forge package)", ErrInvalidInput, cfg.Forge.Type)
	}
	return nil
}

// validateRepositoryContents checks the shared and per-identity repository
// lists.
func validateRepositoryContents(cfg *config.Config) error {
	if len(cfg.Identities) == 0 {
		return ValidateRepositories(cfg.Repos)
	}
	for i, id := range cfg.Identities {
		if err := ValidateRepositories(id.Repos); err != nil {
			return fmt.Errorf("identities[%d]: %w", i, err)
		}
	}
	return nil
}

// ValidateRepositories checks a repository list: owner and name set, no
// duplicates, and a valid test glob.
func ValidateRepositories(repos []config.Repo) error {
	seen := make(map[string]struct{}, len(repos))
	for i, r := range repos {
		if r.Owner == "" || r.Name == "" {
			return fmt.Errorf("%w: repos[%d] needs owner and name", ErrInvalidInput, i)
		}
		if _, exists := seen[r.FullName()]; exists {
			return fmt.Errorf("%w: repos[%d] duplicates %q", ErrInvalidInput, i, r.FullName())
		}
		seen[r.FullName()] = struct{}{}
		if err := validateTestGlob(r.ResolvedTestGlob()); err != nil {
			return fmt.Errorf("%w: repos[%d] %w", ErrInvalidInput, i, err)
		}
	}
	return nil
}

// validateTestGlob reports whether glob is a pattern the test-protection gate can
// compile. It has one caller on each side through ValidateRepositories, so the
// rule has one definition rather than a copy per layer.
func validateTestGlob(glob string) error {
	if glob == "" {
		return nil
	}
	if _, err := filepath.Match(glob, ""); err != nil {
		return fmt.Errorf("test_glob %q: %w", glob, err)
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
	case config.NATSModeEmbedded:
		if cfg.NATS.URL != "" {
			return fmt.Errorf("%w: nats.url must be empty when nats.mode is %q", ErrInvalidInput, mode)
		}
	}
	return nil
}

// validateContainers checks the mandatory managed-worker configuration.
// Embedded and external NATS are both worker-reachable deployment shapes.
func validateContainers(cfg *config.Config) error {
	if cfg.Containers.Image == "" {
		return fmt.Errorf("%w: containers.image is required for autonomous workflow workers", ErrInvalidInput)
	}
	if cfg.Containers.VolumeTTL < 0 {
		return fmt.Errorf("%w: containers.volume_ttl must not be negative", ErrInvalidInput)
	}
	if err := cfg.Containers.ValidateProfiles(); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidInput, err)
	}
	if err := cfg.Containers.ValidateCredentialBindings(); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidInput, err)
	}
	// A registry credential must set both engine and key, or neither.
	if ref := cfg.Containers.RegistryAuth; ref != (config.SecretRef{}) && (ref.Engine == "" || ref.Key == "") {
		return fmt.Errorf("%w: containers.registry_auth must name both an engine and a key, got {engine: %q, key: %q}", ErrInvalidInput, ref.Engine, ref.Key)
	}
	return validateBindingKeyEngines(cfg.Bindings)
}

// validateBindingKeyEngines requires the bindings encryption keys to name the
// env engine. The State Store resolves them at boot, before it serves the
// installed packages an extension engine comes from, so an extension engine
// could never answer.
func validateBindingKeyEngines(b config.BindingsConfig) error {
	refs := append([]config.SecretRef{b.EncryptionKey}, b.PreviousEncryptionKeys...)
	for _, ref := range refs {
		if ref != (config.SecretRef{}) && ref.Engine != "env" {
			return fmt.Errorf("%w: bindings encryption keys must use the env engine, got engine %q", ErrInvalidInput, ref.Engine)
		}
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
