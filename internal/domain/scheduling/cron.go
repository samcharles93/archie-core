package scheduling

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// cronSlots is the set of values one cron field matches, as a bitmask; every
// field's range fits in 64 bits.
type cronSlots uint64

func (s cronSlots) has(v int) bool { return v >= 0 && v < 64 && s&(1<<v) != 0 }

// cronSpec is a parsed 5-field cron expression. A time is due only when every
// field matches: day-of-month and day-of-week are ANDed, unlike Vixie cron's
// OR, so "0 0 1 * 1" means only a Monday that is the 1st.
type cronSpec struct {
	minutes, hours, days, months, weekdays cronSlots
}

func (c *cronSpec) due(t time.Time) bool {
	return c.minutes.has(t.Minute()) && c.hours.has(t.Hour()) && c.days.has(t.Day()) &&
		c.months.has(int(t.Month())) && c.weekdays.has(int(t.Weekday()))
}

var cronMacros = map[string]string{
	"@yearly":   "0 0 1 1 *",
	"@annually": "0 0 1 1 *",
	"@monthly":  "0 0 1 * *",
	"@weekly":   "0 0 * * 0",
	"@daily":    "0 0 * * *",
	"@midnight": "0 0 * * *",
	"@hourly":   "0 * * * *",
}

// parseCronSpec parses the project's cron dialect: five space-separated
// fields (minute, hour, day of month, month, day of week) each a wildcard,
// value, range, step (*/n or a-b/n) or comma list of those, or one of the
// @yearly, @annually, @monthly, @weekly, @daily, @midnight and @hourly
// macros. Any failure wraps ErrInvalidSpec.
func parseCronSpec(expr string) (*cronSpec, error) {
	spec, err := parseCron(expr)
	if err != nil {
		return nil, fmt.Errorf("%w: cron expression %q: %w", ErrInvalidSpec, expr, err)
	}
	return spec, nil
}

func parseCron(expr string) (*cronSpec, error) {
	if macro, ok := cronMacros[expr]; ok {
		expr = macro
	}
	fields := strings.Split(expr, " ")
	if len(fields) != 5 {
		return nil, errors.New("must be a macro or exactly 5 space-separated fields")
	}
	bounds := [5][2]int{{0, 59}, {0, 23}, {1, 31}, {1, 12}, {0, 6}}
	var slots [5]cronSlots
	for i, field := range fields {
		parsed, err := parseCronSegment(field, bounds[i][0], bounds[i][1])
		if err != nil {
			return nil, err
		}
		slots[i] = parsed
	}
	return &cronSpec{minutes: slots[0], hours: slots[1], days: slots[2], months: slots[3], weekdays: slots[4]}, nil
}

// parseCronSegment parses one field whose values lie in [lo, hi].
func parseCronSegment(segment string, lo, hi int) (cronSlots, error) {
	var slots cronSlots
	for part := range strings.SplitSeq(segment, ",") {
		base, stepText, stepped := strings.Cut(part, "/")
		step := 1
		if stepped {
			n, err := strconv.Atoi(stepText)
			if err != nil || n < 1 || n > hi {
				return 0, fmt.Errorf("step %q must be between 1 and %d", stepText, hi)
			}
			step = n
		}
		first, last, err := cronRange(base, part, stepped, lo, hi)
		if err != nil {
			return 0, err
		}
		for v := first; v <= last; v += step {
			slots |= 1 << v
		}
	}
	return slots, nil
}

// cronRange returns the values base names: every value for "*", else a
// single value or a from-to range.
func cronRange(base, part string, stepped bool, lo, hi int) (first, last int, err error) {
	if base == "*" {
		return lo, hi, nil
	}
	from, to, ranged := strings.Cut(base, "-")
	if !ranged && stepped {
		return 0, 0, fmt.Errorf("step in %q needs a wildcard or a range", part)
	}
	if first, err = cronValue(from, lo, hi); err != nil {
		return 0, 0, err
	}
	if !ranged {
		return first, first, nil
	}
	if last, err = cronValue(to, first, hi); err != nil {
		return 0, 0, err
	}
	return first, last, nil
}

func cronValue(text string, lo, hi int) (int, error) {
	v, err := strconv.Atoi(text)
	if err != nil {
		return 0, fmt.Errorf("value %q is not a number", text)
	}
	if v < lo || v > hi {
		return 0, fmt.Errorf("value %d must be between %d and %d", v, lo, hi)
	}
	return v, nil
}
