package archied

import (
	"fmt"

	"github.com/samcharles93/archie-core/internal/domain/scheduling"
	"github.com/samcharles93/archie-core/internal/events"
	"github.com/samcharles93/archie-core/internal/infrastructure/configuration"
	"github.com/samcharles93/archie-core/internal/infrastructure/cronevents"
)

// schedulingConfig translates external input into the domain-owned engine
// configuration and connects the selected event infrastructure.
func schedulingConfig(input configuration.SchedulingInput, bus *events.Bus) (scheduling.EngineConfig, error) {
	cfg := scheduling.DefaultEngineConfig()
	if input.Interval != 0 {
		cfg.Interval = input.Interval.Std()
	}
	if input.MaxParallel != 0 {
		cfg.MaxParallel = input.MaxParallel
	}
	if input.JobTimeout != 0 {
		cfg.JobTimeout = input.JobTimeout.Std()
	}

	switch input.EventsSink {
	case "", configuration.EventsSinkNone:
	case configuration.EventsSinkBus:
		cfg.Events = cronevents.New(bus)
	default:
		return scheduling.EngineConfig{}, fmt.Errorf("%w: scheduling.events_sink %q (want %q or %q)",
			configuration.ErrInvalidInput, input.EventsSink, configuration.EventsSinkBus, configuration.EventsSinkNone)
	}
	if err := cfg.Validate(); err != nil {
		return scheduling.EngineConfig{}, fmt.Errorf("%w: %w", configuration.ErrInvalidInput, err)
	}
	return cfg, nil
}
