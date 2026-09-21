package cronstore

import (
	"encoding/json"
	"fmt"
	"time"
)

// Duration is a schedule interval as the document writes it: the human string
// time.ParseDuration reads, never a nanosecond count. It reads the count too,
// because every schedule stored before this shape existed carries one -- both in
// the control plane's schedules resource and in this package's own jobs table,
// which persists the spec as JSON.
//
// The type is deliberately local, as config.Duration is for the file document
// and channelDuration is in internal/app/controlplane: each document owns its own
// wire form, and what is actually worth unifying later is the legacy-count
// tolerance rather than the tag shape.
type Duration time.Duration

func (d Duration) MarshalJSON() ([]byte, error) {
	return json.Marshal(time.Duration(d).String())
}

func (d *Duration) UnmarshalJSON(data []byte) error {
	var text string
	if err := json.Unmarshal(data, &text); err == nil {
		value, err := time.ParseDuration(text)
		if err != nil {
			return fmt.Errorf("%w: interval must be a Go duration string such as %q: %w", ErrInvalidSpec, "30m", err)
		}
		*d = Duration(value)
		return nil
	}
	var nanos int64
	if err := json.Unmarshal(data, &nanos); err != nil {
		return fmt.Errorf("%w: interval must be a Go duration string such as %q or a nanosecond count: %w", ErrInvalidSpec, "30m", err)
	}
	*d = Duration(nanos)
	return nil
}

// Std is the standard-library duration, for arithmetic and comparison.
func (d Duration) Std() time.Duration { return time.Duration(d) }

// String renders the interval the way the document writes it, so an error
// message or a log line reads "30m0s" rather than 1800000000000.
func (d Duration) String() string { return time.Duration(d).String() }

// Schedule decides when a job is due. The Kind field selects which of
// Interval, Cron, At the store uses to compute nextRun. Empty Kind
// resolves to "interval" so the simplest recurring job needs no
// explicit kind in its JSON.
type Schedule struct {
	// Kind is one of ScheduleInterval, ScheduleCron, ScheduleOnce.
	// Empty resolves to ScheduleInterval.
	Kind string `json:"kind,omitempty"`

	// Interval is the period for ScheduleInterval. Required when Kind
	// is ScheduleInterval; ignored otherwise. It is a Duration rather
	// than a time.Duration so the document carries "30m0s" and not the
	// nanosecond count that asked an operator to type 1800000000000.
	Interval Duration `json:"interval,omitempty"`

	// Cron is the 5-field cron expression for ScheduleCron. Required
	// when Kind is ScheduleCron; ignored otherwise.
	Cron string `json:"cron,omitempty"`

	// At is the one-shot firing time for ScheduleOnce. Required when
	// Kind is ScheduleOnce; ignored otherwise.
	At *time.Time `json:"at,omitempty"`

	// Start anchors the first run for ScheduleInterval / ScheduleCron.
	// Empty means "use creation time" — a freshly-created job with no
	// Start still has a finite next_run. Omitted from the wire when
	// unset so operator-readable files are not noisy.
	Start time.Time `json:"start"`
}

// Schedule kinds the store recognises. Slice 1 fully implements
// Interval; Once is accepted at Create / fires at Due exactly once, but
// has no next-run arithmetic for MarkRun (one-shot by definition). Cron
// is parsed at Create but has no firing arithmetic yet — Slice 2 will
// add the parser. MarkRun on a kind this build does not implement
// returns ErrScheduleUnsupported rather than silently advancing next_run.
const (
	// ScheduleInterval fires every Interval after the last successful run
	// (or after Start, if never run). The simplest recurring schedule.
	ScheduleInterval = "interval"

	// ScheduleCron fires at the next moment matching a standard 5-field
	// cron expression after the last successful run. Slice 2 will add
	// the parser; until then, MarkRun on a cron job returns
	// ErrScheduleUnsupported.
	ScheduleCron = "cron"

	// ScheduleOnce fires once at the configured At time. Subsequent
	// MarkRun calls return ErrScheduleUnsupported because there is no
	// recurring next-run to compute.
	ScheduleOnce = "once"
)

// Validate rejects empty or unknown schedule kinds up front so Create does
// not have to defer the check until MarkRun. Interval additionally requires
// a strictly positive duration; the others defer their full validation.
func (s Schedule) Validate() error {
	switch s.Kind {
	case "", ScheduleInterval:
		if s.Interval <= 0 {
			return fmt.Errorf("%w: interval must be positive, got %v", ErrInvalidSpec, s.Interval)
		}
	case ScheduleCron:
		if s.Cron == "" {
			return fmt.Errorf("%w: cron expression must be non-empty", ErrInvalidSpec)
		}
	case ScheduleOnce:
		if s.At == nil {
			return fmt.Errorf("%w: once schedule requires an `at` time", ErrInvalidSpec)
		}
	default:
		return fmt.Errorf("%w: unknown schedule kind %q", ErrInvalidSpec, s.Kind)
	}
	return nil
}

// resolve maps the empty kind onto its documented default — "interval" is
// what an operator gets when they leave the field unset on the simplest
// recurring job.
func (s Schedule) resolve() Schedule {
	if s.Kind == "" {
		s.Kind = ScheduleInterval
	}
	return s
}

// firstRun returns when the job should fire the very first time it has
// never run before. For Interval, it is Start + Interval (or now + Interval
// when Start is zero, so a freshly-created job with no Start still has a
// finite next-run). For Once, it is At. For Cron, it is left as a future
// responsibility — Slice 1 will not synthesise a value it cannot honour.
func (s Schedule) firstRun(now time.Time) (time.Time, error) {
	switch s.resolve().Kind {
	case ScheduleInterval:
		if !s.Start.IsZero() {
			return s.Start.Add(s.Interval.Std()), nil
		}
		return now.Add(s.Interval.Std()), nil
	case ScheduleOnce:
		if s.At == nil {
			return time.Time{}, fmt.Errorf("%w: once schedule has no at time", ErrScheduleUnsupported)
		}
		return *s.At, nil
	case ScheduleCron:
		return time.Time{}, fmt.Errorf("%w: cron parsing not yet implemented (slice 2)", ErrScheduleUnsupported)
	default:
		return time.Time{}, fmt.Errorf("%w: unknown kind %q", ErrScheduleUnsupported, s.Kind)
	}
}

func (s Schedule) FirstRun(now time.Time) (time.Time, error) { return s.firstRun(now) }

func (s Schedule) NextRun(lastRun time.Time) (time.Time, error) { return s.nextRun(lastRun) }

func (s Schedule) Resolved() Schedule { return s.resolve() }

// nextRun returns when the job should fire after a successful run at
// lastRun. It is the engine-side counterpart to firstRun: every MarkRun
// advances next_run through this function. Interval is fully implemented;
// Once and Cron deliberately fail so a silently-wrong value cannot reach
// the ticker.
func (s Schedule) nextRun(lastRun time.Time) (time.Time, error) {
	switch s.resolve().Kind {
	case ScheduleInterval:
		return lastRun.Add(s.Interval.Std()), nil
	case ScheduleOnce:
		return time.Time{}, fmt.Errorf("%w: once schedule has no next run", ErrScheduleUnsupported)
	case ScheduleCron:
		return time.Time{}, fmt.Errorf("%w: cron arithmetic not yet implemented (slice 2)", ErrScheduleUnsupported)
	default:
		return time.Time{}, fmt.Errorf("%w: unknown kind %q", ErrScheduleUnsupported, s.Kind)
	}
}
