package configuration

import (
	"fmt"

	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/domain/scheduling"
)

// DefaultScheduling returns the defaults an absent [scheduling] section
// resolves to, taken from the engine's own documented constants rather than
// restated here -- one answer for "unset", owned by the domain that acts on
// it. [applySchedulingDefaults] fills a decoded config with them.
func DefaultScheduling() config.SchedulingConfig {
	return config.SchedulingConfig{
		Interval:    config.Duration(scheduling.DefaultInterval),
		MaxParallel: scheduling.DefaultMaxParallel,
		JobTimeout:  config.Duration(scheduling.DefaultJobTimeout),
		EventsSink:  config.EventsSinkNone,
	}
}

// applySchedulingDefaults resolves an absent [scheduling] section. Every field
// defaults independently, so a section naming only max_parallel still gets a
// cadence and a run timeout, and "nothing configured" is one resolved state
// rather than two that happen to mean the same thing.
//
// A negative value is left alone for [validateScheduling] to reject.
func applySchedulingDefaults(cfg *config.Config) {
	d := DefaultScheduling()
	if cfg.Scheduling.Interval == 0 {
		cfg.Scheduling.Interval = d.Interval
	}
	if cfg.Scheduling.MaxParallel == 0 {
		cfg.Scheduling.MaxParallel = d.MaxParallel
	}
	if cfg.Scheduling.JobTimeout == 0 {
		cfg.Scheduling.JobTimeout = d.JobTimeout
	}
	if cfg.Scheduling.EventsSink == "" {
		cfg.Scheduling.EventsSink = d.EventsSink
	}
}

// validateScheduling rejects unusable engine tunables. applySchedulingDefaults
// fills every zero value, so this only fires on an explicitly negative one --
// the engine's own contract documents "negative is an error" for a reason:
// a negative interval cannot be a tick cadence, and clamping it would run
// something other than what the operator wrote, which is how a
// misconfiguration stays invisible. It also rejects an unknown events_sink
// spelling, so a typo cannot silently leave a deployment unobservable.
func validateScheduling(cfg *config.Config) error {
	switch {
	case cfg.Scheduling.Interval < 0:
		return fmt.Errorf("%w: scheduling.interval must not be negative, got %s", ErrInvalidInput, cfg.Scheduling.Interval.Std())
	case cfg.Scheduling.MaxParallel < 0:
		return fmt.Errorf("%w: scheduling.max_parallel must not be negative, got %d", ErrInvalidInput, cfg.Scheduling.MaxParallel)
	case cfg.Scheduling.JobTimeout < 0:
		return fmt.Errorf("%w: scheduling.job_timeout must not be negative, got %s", ErrInvalidInput, cfg.Scheduling.JobTimeout.Std())
	}
	// Empty is valid input here (applySchedulingDefaults resolves it to
	// "none" before this runs on the real load path); it is not "unknown".
	if sink := cfg.Scheduling.EventsSink; sink != "" && !oneOf(sink, eventsSinkModes) {
		return fmt.Errorf("%w: scheduling.events_sink %q (want %s)", ErrInvalidInput, sink, list(eventsSinkModes))
	}
	return nil
}

// TranslateScheduling turns the operator's [scheduling] input into the
// engine's own configuration type, fully resolved: an unset tunable becomes
// the engine's documented default, so the engine receives what the daemon
// means rather than zero values it has to second-guess.
//
// Clock and Events are deliberately left nil. This package owns no clock (the
// engine's system clock is the deployment's answer, not an operator's) and no
// bus: attaching the sink is composition, and a nil Events is a configuration
// the engine documents as valid -- it still runs, it is just unobservable.
//
// A negative tunable, or an unknown events_sink, is reported rather than
// repaired, so a bad file stops startup instead of running something the
// operator did not ask for.
func TranslateScheduling(cfg config.SchedulingConfig) (scheduling.EngineConfig, error) {
	holder := config.Config{Scheduling: cfg}
	if err := validateScheduling(&holder); err != nil {
		return scheduling.EngineConfig{}, err
	}
	// Resolve through the same applier the load path uses, so a hand-built
	// config and a decoded file cannot resolve differently.
	applySchedulingDefaults(&holder)

	return scheduling.EngineConfig{
		Interval:    holder.Scheduling.Interval.Std(),
		MaxParallel: holder.Scheduling.MaxParallel,
		JobTimeout:  holder.Scheduling.JobTimeout.Std(),
	}, nil
}
