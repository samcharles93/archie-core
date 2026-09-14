package configuration

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/domain/scheduling"
)

// writeSchedulingConfig writes a config file whose only content beyond the
// fields every check requires is the given [scheduling] fragment.
func writeSchedulingConfig(t *testing.T, section string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("bot_user = \"widget\"\n"+section), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestTranslateSchedulingResolvesZeroValuesToEngineDefaults pins the
// translator to the engine's own documented constants: the domain has one
// answer for "unset", and a second copy of those numbers here would be free
// to drift from it.
func TestTranslateSchedulingResolvesZeroValuesToEngineDefaults(t *testing.T) {
	eng, err := TranslateScheduling(config.SchedulingConfig{})
	if err != nil {
		t.Fatalf("TranslateScheduling(zero value) = %v, want nil", err)
	}
	if got := eng.Interval; got != scheduling.DefaultInterval {
		t.Errorf("Interval = %v, want %v", got, scheduling.DefaultInterval)
	}
	if got := eng.MaxParallel; got != scheduling.DefaultMaxParallel {
		t.Errorf("MaxParallel = %d, want %d", got, scheduling.DefaultMaxParallel)
	}
	if got := eng.JobTimeout; got != scheduling.DefaultJobTimeout {
		t.Errorf("JobTimeout = %v, want %v", got, scheduling.DefaultJobTimeout)
	}
	// Events stays nil: this package owns no bus, and the engine documents a
	// nil sink as a valid configuration -- it still runs, it is just
	// unobservable. Attaching one is the composition slice's job.
	if eng.Events != nil {
		t.Errorf("Events = %v, want nil for an absent events_sink", eng.Events)
	}
}

func TestTranslateSchedulingKeepsConfiguredValues(t *testing.T) {
	eng, err := TranslateScheduling(config.SchedulingConfig{
		Interval:    config.Duration(30 * time.Second),
		MaxParallel: 2,
		JobTimeout:  config.Duration(5 * time.Minute),
	})
	if err != nil {
		t.Fatalf("TranslateScheduling(configured) = %v, want nil", err)
	}
	if got := eng.Interval; got != 30*time.Second {
		t.Errorf("Interval = %v, want unchanged 30s", got)
	}
	if got := eng.MaxParallel; got != 2 {
		t.Errorf("MaxParallel = %d, want unchanged 2", got)
	}
	if got := eng.JobTimeout; got != 5*time.Minute {
		t.Errorf("JobTimeout = %v, want unchanged 5m", got)
	}
}

// TestTranslateSchedulingRejectsNegativeValues: the engine's own contract
// documents "negative is an error". A clamp would run something other than
// what the operator wrote, which is how a misconfiguration stays invisible,
// so the translator reports it and lets startup fail.
func TestTranslateSchedulingRejectsNegativeValues(t *testing.T) {
	tests := []struct {
		name string
		cfg  config.SchedulingConfig
	}{
		{name: "interval", cfg: config.SchedulingConfig{Interval: config.Duration(-time.Second)}},
		{name: "max_parallel", cfg: config.SchedulingConfig{MaxParallel: -1}},
		{name: "job_timeout", cfg: config.SchedulingConfig{JobTimeout: config.Duration(-time.Minute)}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := TranslateScheduling(tc.cfg)
			if err == nil {
				t.Fatalf("TranslateScheduling(negative %s) = nil error, want a startup error", tc.name)
			}
			if !errors.Is(err, ErrInvalidInput) {
				t.Errorf("TranslateScheduling(negative %s) = %v, want it to wrap ErrInvalidInput", tc.name, err)
			}
		})
	}
}

// TestLoadSchedulingBlockDecodesAllFourFields pins the four toml keys: the
// translator is only reachable from operator input through them.
func TestLoadSchedulingBlockDecodesAllFourFields(t *testing.T) {
	path := writeSchedulingConfig(t, `
[scheduling]
interval = "30s"
max_parallel = 2
job_timeout = "5m"
events_sink = "bus"
`)
	cfg, err := loadFile(path)
	if err != nil {
		t.Fatalf("loadFile() = %v, want nil", err)
	}
	if got := cfg.Scheduling.Interval.Std(); got != 30*time.Second {
		t.Errorf("Scheduling.Interval = %v, want 30s", got)
	}
	if got := cfg.Scheduling.MaxParallel; got != 2 {
		t.Errorf("Scheduling.MaxParallel = %d, want 2", got)
	}
	if got := cfg.Scheduling.JobTimeout.Std(); got != 5*time.Minute {
		t.Errorf("Scheduling.JobTimeout = %v, want 5m", got)
	}
	if got := cfg.Scheduling.EventsSink; got != config.EventsSinkBus {
		t.Errorf("Scheduling.EventsSink = %q, want %q", got, config.EventsSinkBus)
	}

	eng, err := TranslateScheduling(cfg.Scheduling)
	if err != nil {
		t.Fatalf("TranslateScheduling(loaded) = %v, want nil", err)
	}
	if eng.Interval != 30*time.Second || eng.MaxParallel != 2 || eng.JobTimeout != 5*time.Minute {
		t.Errorf("loaded engine config = %+v, want {30s 2 5m}", eng)
	}
}

// TestLoadAbsentSchedulingSectionResolvesEngineDefaults covers the operator
// who never writes the section at all -- the common case. Both the loaded
// section and the translated engine config must be fully resolved, so
// "nothing configured" and "configured with the defaults" are one state.
func TestLoadAbsentSchedulingSectionResolvesEngineDefaults(t *testing.T) {
	cfg, err := loadFile(writeSchedulingConfig(t, ""))
	if err != nil {
		t.Fatalf("loadFile() = %v, want nil", err)
	}
	if got := cfg.Scheduling.Interval.Std(); got != scheduling.DefaultInterval {
		t.Errorf("Scheduling.Interval = %v, want %v", got, scheduling.DefaultInterval)
	}
	if got := cfg.Scheduling.MaxParallel; got != scheduling.DefaultMaxParallel {
		t.Errorf("Scheduling.MaxParallel = %d, want %d", got, scheduling.DefaultMaxParallel)
	}
	if got := cfg.Scheduling.JobTimeout.Std(); got != scheduling.DefaultJobTimeout {
		t.Errorf("Scheduling.JobTimeout = %v, want %v", got, scheduling.DefaultJobTimeout)
	}

	eng, err := TranslateScheduling(cfg.Scheduling)
	if err != nil {
		t.Fatalf("TranslateScheduling(loaded) = %v, want nil", err)
	}
	if eng.Interval != scheduling.DefaultInterval {
		t.Errorf("Interval = %v, want %v", eng.Interval, scheduling.DefaultInterval)
	}
	if eng.MaxParallel != scheduling.DefaultMaxParallel {
		t.Errorf("MaxParallel = %d, want %d", eng.MaxParallel, scheduling.DefaultMaxParallel)
	}
	if eng.JobTimeout != scheduling.DefaultJobTimeout {
		t.Errorf("JobTimeout = %v, want %v", eng.JobTimeout, scheduling.DefaultJobTimeout)
	}
}

// TestLoadRejectsNegativeSchedulingValues puts the negative-value rule on the
// load path, not just in the translator: a file edit that breaks a running
// deployment (a SIGHUP reload) must be refused with the operator's mistake
// named, rather than applied as a negative cadence.
func TestLoadRejectsNegativeSchedulingValues(t *testing.T) {
	tests := []struct {
		name    string
		section string
	}{
		{name: "interval", section: "[scheduling]\ninterval = \"-1m\"\n"},
		{name: "max_parallel", section: "[scheduling]\nmax_parallel = -1\n"},
		{name: "job_timeout", section: "[scheduling]\njob_timeout = \"-1h\"\n"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := loadFile(writeSchedulingConfig(t, tc.section))
			if err == nil {
				t.Fatalf("loadFile(negative %s) = nil, want a load failure", tc.name)
			}
			if !errors.Is(err, ErrInvalidInput) {
				t.Errorf("loadFile(negative %s) = %v, want it to wrap ErrInvalidInput", tc.name, err)
			}
		})
	}
}

// TestLoadAcceptsEveryDocumentedEventsSinkSpelling pins the on/off
// vocabulary: "bus" publishes on the event stream, and "none" means no sink.
// An absent key resolves to "none" rather than staying empty, the same shape
// applyMemoryDefaults and applyNATSDefaults use -- a resolved config has one
// spelling for "not publishing", not two that happen to mean it. "off" is
// deliberately not a third spelling: a near-miss must be rejected, not
// guessed at.
func TestLoadAcceptsEveryDocumentedEventsSinkSpelling(t *testing.T) {
	tests := []struct {
		name    string
		section string
		want    string
	}{
		{name: "absent", section: "", want: config.EventsSinkNone},
		{name: "none", section: "[scheduling]\nevents_sink = \"none\"\n", want: config.EventsSinkNone},
		{name: "bus", section: "[scheduling]\nevents_sink = \"bus\"\n", want: config.EventsSinkBus},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := loadFile(writeSchedulingConfig(t, tc.section))
			if err != nil {
				t.Fatalf("loadFile(events_sink %s) = %v, want nil", tc.name, err)
			}
			if got := cfg.Scheduling.EventsSink; got != tc.want {
				t.Errorf("Scheduling.EventsSink = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestLoadRejectsUnknownEventsSink: a typo must not silently leave the
// engine unobservable, which is exactly what an unrecognised value would do
// if it defaulted to off.
func TestLoadRejectsUnknownEventsSink(t *testing.T) {
	_, err := loadFile(writeSchedulingConfig(t, "[scheduling]\nevents_sink = \"kafka\"\n"))
	if err == nil {
		t.Fatal("loadFile(unknown events_sink) = nil, want a load failure")
	}
	if !errors.Is(err, ErrInvalidInput) {
		t.Errorf("loadFile(unknown events_sink) = %v, want it to wrap ErrInvalidInput", err)
	}
}

// TestExampleConfigSchedulingSectionIsDocumentationOnly checks the shipped
// template the way an operator's first run will: whatever the [scheduling]
// block documents, the file must load and resolve to the documented state,
// and the block must stay dormant (no live section) the same way [[repos]]
// does -- a live example is a default an operator never chose.
func TestExampleConfigSchedulingSectionIsDocumentationOnly(t *testing.T) {
	cfg, err := loadFile(filepath.Join("..", "..", "..", "config.example.toml"))
	if err != nil {
		t.Fatalf("loadFile(config.example.toml) = %v, want nil", err)
	}
	eng, err := TranslateScheduling(cfg.Scheduling)
	if err != nil {
		t.Fatalf("TranslateScheduling(example) = %v, want nil", err)
	}
	if eng.Interval != scheduling.DefaultInterval {
		t.Errorf("Interval = %v, want the documented default %v", eng.Interval, scheduling.DefaultInterval)
	}
	if eng.MaxParallel != scheduling.DefaultMaxParallel {
		t.Errorf("MaxParallel = %d, want the documented default %d", eng.MaxParallel, scheduling.DefaultMaxParallel)
	}
	if eng.JobTimeout != scheduling.DefaultJobTimeout {
		t.Errorf("JobTimeout = %v, want the documented default %v", eng.JobTimeout, scheduling.DefaultJobTimeout)
	}
	if eng.Events != nil {
		t.Errorf("Events = %v, want nil: the example section is commented out, so no sink is configured", eng.Events)
	}
}
