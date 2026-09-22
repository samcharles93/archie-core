package scheduling

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/pocketbase/pocketbase/tools/cron"
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

// Schedule kinds the store recognises. Interval and Cron have full
// next-run arithmetic; Once is accepted at Create / fires at Due exactly
// once, but has no next-run arithmetic for MarkRun (one-shot by
// definition). MarkRun on a kind this build does not implement returns
// ErrScheduleUnsupported rather than silently advancing next_run.
const (
	// ScheduleInterval fires every Interval after the last successful run
	// (or after Start, if never run). The simplest recurring schedule.
	ScheduleInterval = "interval"

	// ScheduleCron fires at the next moment matching a 5-field cron
	// expression (or one of the @ macros) after the last successful
	// run, computed in the zone of that run time. Expressions are parsed
	// with pocketbase tools/cron, which ANDs day-of-month with day-of-week
	// instead of Vixie cron's OR: "0 0 1 * 1" means only Monday the 1st,
	// not "the 1st or any Monday".
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
		if _, err := parseCronSpec(s.Cron); err != nil {
			return err
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
// finite next-run). For Once, it is At. For Cron, it is the first matching
// minute strictly after Start (or after now when Start is zero).
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
		sched, err := parseCronSpec(s.Cron)
		if err != nil {
			return time.Time{}, err
		}
		anchor := now
		if !s.Start.IsZero() {
			anchor = s.Start
		}
		return nextCronRun(sched, anchor)
	default:
		return time.Time{}, fmt.Errorf("%w: unknown kind %q", ErrScheduleUnsupported, s.Kind)
	}
}

// parseCronSpec parses expr with pocketbase tools/cron — the project's
// chosen cron dialect: standard 5-field expressions plus the @yearly,
// @monthly, @weekly, @daily and @hourly macros — and wraps any parse
// failure in ErrInvalidSpec so callers need only the one sentinel.
func parseCronSpec(expr string) (*cron.Schedule, error) {
	sched, err := cron.NewSchedule(expr)
	if err != nil {
		return nil, fmt.Errorf("%w: cron expression %q: %w", ErrInvalidSpec, expr, err)
	}
	return sched, nil
}

// cronHorizon bounds the next-run scan. A satisfiable expression always
// fires well within it (the sparsest realistic case, Feb 29, recurs every
// four years); only a spec that can never fire — e.g. "0 0 30 2 *", which
// the parser's field ranges cannot reject — exhausts the scan.
const cronHorizon = 5 * 365 * 24 * time.Hour

// nextCronRun returns the first wall-clock minute in the location of from
// at which sched is due, strictly after the minute containing from — the
// minute that just fired is never its own successor. Whole months and
// hours that cannot match are skipped, so even a sparse expression costs
// a handful of iterations per year scanned.
func nextCronRun(sched *cron.Schedule, from time.Time) (time.Time, error) {
	t := from.Truncate(time.Minute).Add(time.Minute)
	for limit := from.Add(cronHorizon); t.Before(limit); {
		if sched.IsDue(cron.NewMoment(t)) {
			return t, nil
		}
		if _, ok := sched.Months[int(t.Month())]; !ok {
			t = time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, t.Location()).AddDate(0, 1, 0)
			continue
		}
		if _, ok := sched.Hours[t.Hour()]; !ok {
			t = t.Add(time.Hour).Truncate(time.Hour)
			continue
		}
		t = t.Add(time.Minute)
	}
	return time.Time{}, fmt.Errorf("%w: cron expression does not match any minute within the next five years", ErrInvalidSpec)
}

func (s Schedule) FirstRun(now time.Time) (time.Time, error) { return s.firstRun(now) }

func (s Schedule) NextRun(lastRun time.Time) (time.Time, error) { return s.nextRun(lastRun) }

func (s Schedule) Resolved() Schedule { return s.resolve() }

// nextRun returns when the job should fire after a successful run at
// lastRun. It is the engine-side counterpart to firstRun: every MarkRun
// advances next_run through this function. Interval and Cron are fully
// implemented; Once deliberately fails (there is no recurring next run)
// so a silently-wrong value cannot reach the ticker.
func (s Schedule) nextRun(lastRun time.Time) (time.Time, error) {
	switch s.resolve().Kind {
	case ScheduleInterval:
		return lastRun.Add(s.Interval.Std()), nil
	case ScheduleOnce:
		return time.Time{}, fmt.Errorf("%w: once schedule has no next run", ErrScheduleUnsupported)
	case ScheduleCron:
		sched, err := parseCronSpec(s.Cron)
		if err != nil {
			return time.Time{}, err
		}
		return nextCronRun(sched, lastRun)
	default:
		return time.Time{}, fmt.Errorf("%w: unknown kind %q", ErrScheduleUnsupported, s.Kind)
	}
}
