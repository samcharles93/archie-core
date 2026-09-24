package scheduling

import (
	"errors"
	"testing"
	"time"
)

// testZone is a fixed +02:00 zone: explicit, DST-free, and distinct from UTC
// so any arithmetic that silently computes in UTC fails. Every schedule test
// builds its times here, making the suite independent of the host's TZ.
var testZone = time.FixedZone("TEST", 2*60*60)

// zoned builds a wall-clock time in the fixed test zone.
func zoned(year int, month time.Month, day, hour, minute, sec int) time.Time {
	return time.Date(year, month, day, hour, minute, sec, 0, testZone)
}

func TestScheduleCronNextRun(t *testing.T) {
	tests := []struct {
		name    string
		expr    string
		lastRun time.Time
		want    time.Time
	}{
		{
			name:    "minute later in the same hour",
			expr:    "30 14 * * *",
			lastRun: zoned(2026, 8, 24, 13, 0, 0),
			want:    zoned(2026, 8, 24, 14, 30, 0),
		},
		{
			name:    "hour boundary skips the remainder of the hour",
			expr:    "*/15 * * * *",
			lastRun: zoned(2026, 8, 24, 12, 7, 0),
			want:    zoned(2026, 8, 24, 12, 15, 0),
		},
		{
			// pins the AND semantics: Vixie cron would give the next Monday
			// (2026-08-31); this dialect gives Monday the 1st.
			name:    "dual-restricted day-of-month and day-of-week AND, not Vixie OR",
			expr:    "0 0 1 * 1",
			lastRun: zoned(2026, 8, 24, 12, 15, 30),
			want:    zoned(2027, 2, 1, 0, 0, 0),
		},
		{
			name:    "run exactly on a matching minute advances to the next one",
			expr:    "0 * * * *",
			lastRun: zoned(2026, 8, 24, 12, 0, 0),
			want:    zoned(2026, 8, 24, 13, 0, 0),
		},
		{
			name:    "across the hour boundary",
			expr:    "5,35 * * * *",
			lastRun: zoned(2026, 8, 24, 12, 36, 0),
			want:    zoned(2026, 8, 24, 13, 5, 0),
		},
		{
			name:    "range with step across hours",
			expr:    "0 9-17/2 * * *",
			lastRun: zoned(2026, 8, 24, 10, 0, 0),
			want:    zoned(2026, 8, 24, 11, 0, 0),
		},
		{
			name:    "across midnight",
			expr:    "0 0 * * *",
			lastRun: zoned(2026, 8, 24, 23, 59, 0),
			want:    zoned(2026, 8, 25, 0, 0, 0),
		},
		{
			name:    "across the month boundary",
			expr:    "0 0 1 * *",
			lastRun: zoned(2026, 8, 31, 12, 0, 0),
			want:    zoned(2026, 9, 1, 0, 0, 0),
		},
		{
			name:    "weekday rolls to next Monday",
			expr:    "0 0 * * 1",
			lastRun: zoned(2026, 8, 21, 10, 0, 0),
			want:    zoned(2026, 8, 24, 0, 0, 0),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := Schedule{Kind: ScheduleCron, Cron: tc.expr}
			got, err := s.NextRun(tc.lastRun)
			if err != nil {
				t.Fatalf("NextRun(%v): %v", tc.lastRun, err)
			}
			if !got.Equal(tc.want) {
				t.Fatalf("NextRun(%v) = %v (%s), want %v (%s)",
					tc.lastRun, got, got.Format("2006-01-02 15:04:05 MST"),
					tc.want, tc.want.Format("2006-01-02 15:04:05 MST"))
			}
		})
	}
}

func TestScheduleCronNextRunMacros(t *testing.T) {
	// lastRun is Monday 2026-08-24 12:15:30 TEST.
	lastRun := zoned(2026, 8, 24, 12, 15, 30)
	tests := []struct {
		name string
		expr string
		want time.Time
	}{
		{name: "@hourly", expr: "@hourly", want: zoned(2026, 8, 24, 13, 0, 0)},
		{name: "@daily", expr: "@daily", want: zoned(2026, 8, 25, 0, 0, 0)},
		{name: "@midnight", expr: "@midnight", want: zoned(2026, 8, 25, 0, 0, 0)},
		{name: "@weekly", expr: "@weekly", want: zoned(2026, 8, 30, 0, 0, 0)},
		{name: "@monthly", expr: "@monthly", want: zoned(2026, 9, 1, 0, 0, 0)},
		{name: "@yearly", expr: "@yearly", want: zoned(2027, 1, 1, 0, 0, 0)},
		{name: "@annually", expr: "@annually", want: zoned(2027, 1, 1, 0, 0, 0)},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := Schedule{Kind: ScheduleCron, Cron: tc.expr}
			got, err := s.NextRun(lastRun)
			if err != nil {
				t.Fatalf("NextRun(%v): %v", lastRun, err)
			}
			if !got.Equal(tc.want) {
				t.Fatalf("NextRun(%v) with %q = %v, want %v", lastRun, tc.expr, got, tc.want)
			}
		})
	}
}

func TestScheduleCronComputesInThePassedZone(t *testing.T) {
	s := Schedule{Kind: ScheduleCron, Cron: "0 6 * * *"}

	// lastRun 05:30 in +02:00: 06:00 local is 04:00 UTC.
	got, err := s.NextRun(zoned(2026, 8, 24, 5, 30, 0))
	if err != nil {
		t.Fatalf("NextRun: %v", err)
	}
	if want := time.Date(2026, 8, 24, 4, 0, 0, 0, time.UTC); !got.Equal(want) {
		t.Fatalf("fixed-zone NextRun = %v (%s), want %v UTC", got, got.Format("15:04 MST"), want)
	}

	// The same wall-clock spec anchored in UTC must land on 06:00 UTC instead.
	got, err = s.NextRun(time.Date(2026, 8, 24, 5, 30, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("NextRun: %v", err)
	}
	if want := time.Date(2026, 8, 24, 6, 0, 0, 0, time.UTC); !got.Equal(want) {
		t.Fatalf("UTC NextRun = %v, want %v UTC", got, want)
	}
}

func TestScheduleCronFirstRun(t *testing.T) {
	t.Run("anchored on Start", func(t *testing.T) {
		s := Schedule{
			Kind:  ScheduleCron,
			Cron:  "0 12 * * *",
			Start: zoned(2026, 8, 24, 9, 0, 0),
		}
		want := zoned(2026, 8, 24, 12, 0, 0)
		got, err := s.FirstRun(time.Date(2026, 8, 24, 6, 0, 0, 0, testZone))
		if err != nil {
			t.Fatalf("FirstRun: %v", err)
		}
		if !got.Equal(want) {
			t.Fatalf("FirstRun = %v, want %v", got, want)
		}
	})

	t.Run("zero Start anchors on now", func(t *testing.T) {
		s := Schedule{Kind: ScheduleCron, Cron: "0 12 * * *"}
		now := zoned(2026, 8, 24, 12, 30, 0)
		want := zoned(2026, 8, 25, 12, 0, 0)
		got, err := s.FirstRun(now)
		if err != nil {
			t.Fatalf("FirstRun: %v", err)
		}
		if !got.Equal(want) {
			t.Fatalf("FirstRun(%v) = %v, want %v", now, got, want)
		}
	})
}

func TestScheduleCronValidate(t *testing.T) {
	t.Run("empty cron is rejected", func(t *testing.T) {
		err := Schedule{Kind: ScheduleCron}.Validate()
		if !errors.Is(err, ErrInvalidSpec) {
			t.Fatalf("Validate() = %v, want ErrInvalidSpec", err)
		}
	})

	t.Run("valid expressions pass", func(t *testing.T) {
		for _, expr := range []string{"*/15 * * * *", "0 9-17/2 * * *", "@daily", "@yearly"} {
			if err := (Schedule{Kind: ScheduleCron, Cron: expr}).Validate(); err != nil {
				t.Fatalf("Validate(%q) = %v, want nil", expr, err)
			}
		}
	})
}

func TestScheduleCronInvalidExpressions(t *testing.T) {
	tests := []struct {
		name string
		expr string
	}{
		{name: "minute out of range", expr: "61 * * * *"},
		{name: "hour out of range", expr: "* 24 * * *"},
		{name: "day of month out of range", expr: "0 0 32 * *"},
		{name: "month out of range", expr: "0 0 * 13 *"},
		{name: "day of week out of range", expr: "0 0 * * 7"},
		{name: "too few fields", expr: "* * * *"},
		{name: "too many fields", expr: "* * * * * *"},
		{name: "non-numeric field", expr: "banana * * * *"},
		{name: "zero step", expr: "*/0 * * * *"},
		{name: "empty", expr: ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := Schedule{Kind: ScheduleCron, Cron: tc.expr}

			if err := s.Validate(); !errors.Is(err, ErrInvalidSpec) {
				t.Errorf("Validate() = %v, want ErrInvalidSpec", err)
			}

			_, err := s.FirstRun(zoned(2026, 8, 24, 12, 0, 0))
			if !errors.Is(err, ErrInvalidSpec) {
				t.Errorf("FirstRun err = %v, want ErrInvalidSpec", err)
			}

			_, err = s.NextRun(zoned(2026, 8, 24, 12, 0, 0))
			if !errors.Is(err, ErrInvalidSpec) {
				t.Errorf("NextRun err = %v, want ErrInvalidSpec", err)
			}
		})
	}
}
