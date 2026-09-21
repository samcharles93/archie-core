package cronstore

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// TestScheduleIntervalWritesADurationString pins the document form: the interval
// an operator edits must read as a duration, not as the nanosecond count that
// asked them to type 1800000000000.
func TestScheduleIntervalWritesADurationString(t *testing.T) {
	encoded, err := json.Marshal(Schedule{Kind: ScheduleInterval, Interval: Duration(30 * time.Minute)})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if got := string(encoded); !strings.Contains(got, `"interval":"30m0s"`) {
		t.Errorf("marshalled schedule = %s, want interval as the string \"30m0s\"", got)
	}
	if strings.Contains(string(encoded), "1800000000000") {
		t.Errorf("marshalled schedule = %s, want no nanosecond count", encoded)
	}
}

// TestScheduleIntervalReadsEveryStoredForm covers what the store already holds.
// Every schedule written before this shape existed carries a nanosecond count, in
// both the control plane's schedules resource and this package's own jobs table,
// and both have to keep working without being re-saved first.
func TestScheduleIntervalReadsEveryStoredForm(t *testing.T) {
	for _, tt := range []struct {
		name     string
		document string
		expected time.Duration
	}{
		{"legacy nanosecond count", `{"kind":"interval","interval":1800000000000}`, 30 * time.Minute},
		{"duration string", `{"kind":"interval","interval":"30m"}`, 30 * time.Minute},
		{"duration string, canonical", `{"kind":"interval","interval":"30m0s"}`, 30 * time.Minute},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var schedule Schedule
			if err := json.Unmarshal([]byte(tt.document), &schedule); err != nil {
				t.Fatalf("unmarshal %s: %v", tt.document, err)
			}
			if schedule.Interval.Std() != tt.expected {
				t.Errorf("Interval = %v, want %v", schedule.Interval, tt.expected)
			}
			if err := schedule.Validate(); err != nil {
				t.Errorf("Validate() = %v, want the decoded schedule to be usable", err)
			}
		})
	}
}

// TestScheduleIntervalRefusesWhatItCannotUse keeps the tolerance above from
// becoming indifference: a duration that does not parse must fail loudly rather
// than decode as zero, because a zero interval is a job that never fires.
func TestScheduleIntervalRefusesWhatItCannotUse(t *testing.T) {
	for _, tt := range []struct{ name, document string }{
		{"an unparseable duration", `{"kind":"interval","interval":"soon"}`},
		{"a fractional count", `{"kind":"interval","interval":1.5}`},
		{"a null interval", `{"kind":"interval","interval":null}`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var schedule Schedule
			if err := json.Unmarshal([]byte(tt.document), &schedule); err == nil {
				t.Fatalf("unmarshal(%s) = nil, want a refusal: a silent zero interval never fires", tt.document)
			}
		})
	}
}

// TestScheduleIntervalRoundTripsThroughTheStore pins the shape the jobs table
// keeps, since it persists the spec as JSON rather than as columns.
func TestScheduleIntervalRoundTripsThroughTheStore(t *testing.T) {
	spec := JobSpec{ID: "nightly", Schedule: Schedule{Kind: ScheduleInterval, Interval: Duration(90 * time.Minute)}}
	encoded, err := json.Marshal(spec)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(encoded), `"interval":"1h30m0s"`) {
		t.Fatalf("marshalled spec = %s, want the interval as \"1h30m0s\"", encoded)
	}
	var decoded JobSpec
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded.Schedule.Interval.Std() != 90*time.Minute {
		t.Errorf("round-tripped Interval = %v, want 1h30m0s", decoded.Schedule.Interval)
	}
}
