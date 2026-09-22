package configuration

import (
	"fmt"
	"net/url"
	"path/filepath"
	"slices"
	"strings"

	"github.com/samcharles93/archie-core/internal/config"
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

var (
	forgeTypes       = []string{forgeTypeGitHub, forgeTypeGitea, forgeTypeNone, forgeTypeOff, forgeTypeDisabled}
	dispatchTriggers = []string{dispatchTriggerAssignee, dispatchTriggerLabel, dispatchTriggerEither}
	natsModes        = []string{config.NATSModeEmbedded, config.NATSModeExternal}
	forgeIntakes     = []string{config.ForgeIntakePoll, config.ForgeIntakeWebhook, config.ForgeIntakeBoth}
	memoryEngines    = []string{memoryEngineBuiltin}
)

// Validate judges a configuration a process will run with -- every check that
// can reject one -- against a config.Config value already built in memory, e.g.
// by archied setup before it has written anything to disk. It is a superset of
// what [Loader] applies to a file (validateBootstrap): a check over a setting
// the control plane owns belongs here and not there, so that a stale TOML value
// cannot fail a process's startup.
//
// It judges an EFFECTIVE document: the bootstrap file with every setting the
// control plane owns layered over it. That layering happens at exactly one
// place, boot.runtimeConfig (internal/app/archied/control_plane.go), which runs
// this afterwards; the daemon and the Gateway both go through it, and the
// readiness config probe re-runs it against the running value. A database value
// that will not validate therefore stops the process rather than starting it
// degraded.
//
// A plain file document is judged by validateBootstrap instead: see the split
// there for why the loader must not apply this to it.
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
	if err := validateBootstrap(cfg); err != nil {
		return err
	}
	return validateDatabaseOwned(cfg)
}

// validateBootstrap reports the first problem in the settings a bootstrap
// document still owns: the file config a process reads before it can reach the
// State Store. [Loader] applies this to every file source.
//
// It deliberately omits the settings the control plane stores, because a stale
// TOML value in one of them must not be able to fail a process's startup
// (docs/prds/runtime-control-plane.md, "Bootstrap, migration, and recovery":
// after migration, settings in TOML are ignored and cannot block State Store
// startup). archie-state-store resolves its file config and seeds the
// control-plane resources from that same document on a fresh database, and a
// seed it cannot validate is skipped rather than fatal
// (controlplane.Server.ImportConfig), so a check left here is a check that can
// still block it. Those checks live in validateDatabaseOwned and run on the
// effective document instead -- which is where the process that uses the value
// refuses it, and where the operator's fix is the file again.
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
	if err := validateImage(cfg); err != nil {
		return err
	}
	if err := validateCurators(cfg); err != nil {
		return err
	}
	return validateCapture(cfg)
}

// validateDatabaseOwned reports the first problem in a setting the control
// plane owns. Each check below judges a field that a control-plane resource is
// seeded from and replaces wholesale: providers (provider-settings),
// poll_interval and dispatch (scheduling-policy), containers
// (container-runtime-policies), and the repository lists
// (repository-policies). It runs on the effective document, after that
// replacement, which is the only point at which the value being judged is the
// value a process will use.
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

// validateImage rejects an enabled hosted provider with no class or no
// resolvable credential, and a Default naming a provider that is not both
// present and enabled -- either would let /image silently fall back to
// "always ask" (harmless) or fail at call time with a confusing error
// (not harmless). An absent [image] section is valid and registers no
// provider, matching the epic's non-goal that a paid provider must never
// activate by omission.
func validateImage(cfg *config.Config) error {
	for name, p := range cfg.Image.Hosted {
		if !p.Enabled {
			continue
		}
		if p.Class == "" {
			return fmt.Errorf("%w: image.hosted.%s.class is required when enabled", ErrInvalidInput, name)
		}
		if p.APIKeyEnv == "" && p.APIKey == (config.SecretRef{}) {
			return fmt.Errorf("%w: image.hosted.%s requires api_key_env or api_key when enabled", ErrInvalidInput, name)
		}
	}
	for name, p := range cfg.Image.Local {
		if p.Enabled && p.Backend == "" {
			return fmt.Errorf("%w: image.local.%s.backend is required when enabled", ErrInvalidInput, name)
		}
	}
	if cfg.Image.Default != "" {
		hosted, hostedOK := cfg.Image.Hosted[cfg.Image.Default]
		local, localOK := cfg.Image.Local[cfg.Image.Default]
		switch {
		case hostedOK && hosted.Enabled, localOK && local.Enabled:
			// found and enabled
		default:
			return fmt.Errorf("%w: image.default %q must name an enabled provider under image.hosted or image.local", ErrInvalidInput, cfg.Image.Default)
		}
	}
	return nil
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
	if !DispatchTriggerValid(cfg.Dispatch.Trigger) {
		return fmt.Errorf("%w: dispatch.trigger %q (want %s)", ErrInvalidInput, cfg.Dispatch.Trigger, list(dispatchTriggers))
	}
	if (cfg.Dispatch.Trigger == dispatchTriggerLabel || cfg.Dispatch.Trigger == dispatchTriggerEither) && cfg.Label == "" {
		return fmt.Errorf("%w: label is required when dispatch.trigger is %q (an empty label matches every open issue)", ErrInvalidInput, cfg.Dispatch.Trigger)
	}
	return nil
}

// DispatchTriggerValid reports whether trigger names a discovery rule the daemon
// can poll with. It is exported because the scheduling-policy resource validator
// (internal/app/controlplane) judges the same field: that resource is seeded
// from cfg.Dispatch and replaces it wholesale once the database owns it, so a
// value this package rejects must not be storable, and a value it accepts must
// not be rejected there. One definition, both layers.
func DispatchTriggerValid(trigger string) bool {
	return oneOf(trigger, dispatchTriggers)
}

// validateForgeIntake checks the forge intake mode and that webhook intake has
// the secret and listen address it needs. An unset intake resolves to "poll"
// without mutating cfg, so Validate (which does not apply defaults) accepts a
// hand-built config the same way Loader.File accepts its on-disk form.
//
// It also refuses the intake settings that no code path reads: the ones on a
// [[identities]] entry, and a root webhook intake paired with [[identities]].
// The receiver is single-identity by construction
// (bootstrap.setupForgeWebhook binds one dispatch predicate and one secret),
// so those two spellings could only have been accepted-and-ignored -- the root
// one silently downgrading to polling at startup.
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

// identityIntakes is the intake modes a [[identities]] entry may declare.
// "poll" is the whole list: the receiver reads the root [forge] block only, so
// a per-identity "webhook" would have no effect (see
// validateIdentityForgeIntake). Named separately from forgeIntakes so a
// rejection for a typo does not advertise modes the identity cannot have.
var identityIntakes = []string{config.ForgeIntakePoll}

// validateIdentityForgeIntake refuses the intake settings on a [[identities]]
// entry. IdentityConfig.Forge is the same type as the root [forge] block, so
// identities[N].forge.intake, .webhook_secret and .webhook_addr all DECODE --
// but every read of them is of the root block (this file and
// bootstrap.setupForgeWebhook), and per-identity intake never reached even
// this function. An operator could therefore write a webhook intake that
// parsed, validated, and did nothing while the identity went on polling.
// Reject it at the source: intake per identity is a migration
// (internal/domain/workintake routing), not a setting.
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

// identitySettingPath renders the location of a per-identity setting, naming
// the identity as well as indexing it: an operator reading a rejection for a
// four-entry [[identities]] block finds the offender by its name instead of
// counting entries. The name is omitted when it is empty, because this runs
// before validateIdentities has required it.
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

// validateIdentityStructure judges the shape of the identity definitions: the
// fields a process needs to know which identities exist and which credentials
// each one commits and calls with. The repository lists inside them are judged
// by validateRepositoryContents, because repositories are a control-plane
// resource that replaces the file's list after the merge.
func validateIdentityStructure(cfg *config.Config) error {
	if len(cfg.Identities) == 0 {
		return validateSingleIdentity(cfg)
	}
	return validateIdentities(cfg.Identities)
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
		if !ForgeDisabled(id.Forge.Type) && id.Forge.Token == (config.SecretRef{}) {
			return fmt.Errorf("%w: identities[%d].forge.token is required (each identity needs its own secret reference; unlike the top-level [forge], there is no default)", ErrInvalidInput, i)
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
	return nil
}

// validateRepositoryContents judges the repositories themselves, whichever list
// they appear in. The shared list is the decision the database's copy replaces,
// so it is verified on the effective document -- after that replacement, which is
// the only point the value being judged is the one a process will use. The
// per-identity lists are verified there for a different reason: no resource
// carries them, and the daemon is the only process that reads them
// (docs/architecture/configuration.md's IdentityConfig note).
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

// ValidateRepositories judges a repository list: owner and name present, no
// duplicate entries, and a test glob the gate can compile. It is exported
// because the repository-policies resource validator (internal/app/controlplane)
// judges the same list -- that resource is seeded from these and replaces them
// wholesale, so the two layers must not disagree about which lists are valid, in
// either direction: a list one layer accepts and the other refuses is a config
// that boots file-side and stores nowhere, or a stored value the daemon then
// refuses to start with.
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
