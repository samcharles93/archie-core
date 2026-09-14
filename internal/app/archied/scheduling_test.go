package archied

import (
	"errors"
	"testing"
	"time"

	"github.com/samcharles93/archie-core/internal/events"
	"github.com/samcharles93/archie-core/internal/infrastructure/configuration"
)

func TestSchedulingConfigTranslatesExternalInput(t *testing.T) {
	tests := []struct {
		name            string
		input           configuration.SchedulingInput
		wantInterval    time.Duration
		wantMaxParallel int
		wantJobTimeout  time.Duration
	}{
		{
			name:            "defaults",
			wantInterval:    time.Minute,
			wantMaxParallel: 4,
			wantJobTimeout:  time.Hour,
		},
		{
			name: "configured",
			input: configuration.SchedulingInput{
				Interval:    configuration.Duration(30 * time.Second),
				MaxParallel: 2,
				JobTimeout:  configuration.Duration(5 * time.Minute),
			},
			wantInterval:    30 * time.Second,
			wantMaxParallel: 2,
			wantJobTimeout:  5 * time.Minute,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := schedulingConfig(tc.input, nil)
			if err != nil {
				t.Fatal(err)
			}
			if got.Interval != tc.wantInterval || got.MaxParallel != tc.wantMaxParallel || got.JobTimeout != tc.wantJobTimeout {
				t.Errorf("schedulingConfig() = %+v, want interval=%v max_parallel=%d job_timeout=%v",
					got, tc.wantInterval, tc.wantMaxParallel, tc.wantJobTimeout)
			}
		})
	}
}

func TestSchedulingConfigRejectsInvalidInput(t *testing.T) {
	tests := []configuration.SchedulingInput{
		{Interval: configuration.Duration(-time.Second)},
		{MaxParallel: -1},
		{JobTimeout: configuration.Duration(-time.Second)},
		{EventsSink: "kafka"},
	}
	for _, input := range tests {
		if _, err := schedulingConfig(input, nil); !errors.Is(err, configuration.ErrInvalidInput) {
			t.Errorf("schedulingConfig(%+v) error = %v, want ErrInvalidInput", input, err)
		}
	}
}

func TestSchedulingConfigSelectsEventSink(t *testing.T) {
	bus := events.NewBus()
	t.Cleanup(bus.Close)

	for _, mode := range []string{"", configuration.EventsSinkNone} {
		cfg, err := schedulingConfig(configuration.SchedulingInput{EventsSink: mode}, bus)
		if err != nil {
			t.Fatal(err)
		}
		if cfg.Events != nil {
			t.Errorf("events_sink %q produced a sink", mode)
		}
	}
	cfg, err := schedulingConfig(configuration.SchedulingInput{EventsSink: configuration.EventsSinkBus}, bus)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Events == nil {
		t.Fatal("events_sink bus produced no sink")
	}
}
