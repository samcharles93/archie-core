package store

import (
	"context"
	"testing"
	"time"
)

// TestTaskTimestampsRoundTrip pins that a task carries usable created/updated
// times through every read path. They are what the dashboard shows a task's
// age from; before this they existed as SQLite columns but were never selected,
// so the API could not answer "how long has this been running".
func TestTaskTimestampsRoundTrip(t *testing.T) {
	ctx := context.Background()
	s := OpenTest(t)
	t.Cleanup(func() { _ = s.Close() })

	before := time.Now().UTC().Add(-2 * time.Second)
	if _, err := s.EnqueueIssue(ctx, "o", "r", 1, "title", "body", "", ""); err != nil {
		t.Fatalf("EnqueueIssue: %v", err)
	}
	after := time.Now().UTC().Add(2 * time.Second)

	inRange := func(t *testing.T, label string, got time.Time) {
		t.Helper()
		if got.IsZero() {
			t.Errorf("%s is zero; the column was not selected", label)
			return
		}
		if got.Before(before) || got.After(after) {
			t.Errorf("%s = %s, want within [%s, %s]", label, got, before, after)
		}
	}

	// Every read path that returns a Task must populate them.
	t.Run("TaskByIssue", func(t *testing.T) {
		task, err := s.TaskByIssue(ctx, "o", "r", 1)
		if err != nil || task == nil {
			t.Fatalf("TaskByIssue = (%v, %v)", task, err)
		}
		inRange(t, "CreatedAt", task.CreatedAt)
		inRange(t, "UpdatedAt", task.UpdatedAt)
	})

	t.Run("Tasks list", func(t *testing.T) {
		tasks, err := s.Tasks(ctx, 10)
		if err != nil {
			t.Fatalf("Tasks: %v", err)
		}
		if len(tasks) != 1 {
			t.Fatalf("got %d tasks, want 1", len(tasks))
		}
		inRange(t, "CreatedAt", tasks[0].CreatedAt)
		inRange(t, "UpdatedAt", tasks[0].UpdatedAt)
	})

	t.Run("ClaimNext", func(t *testing.T) {
		task, err := s.ClaimNext(ctx)
		if err != nil || task == nil {
			t.Fatalf("ClaimNext = (%v, %v)", task, err)
		}
		inRange(t, "CreatedAt", task.CreatedAt)
		inRange(t, "UpdatedAt", task.UpdatedAt)
	})
}

// TestSQLiteTimeScan pins the scanner that makes the above possible: SQLite
// stores these as TEXT via datetime('now'), so the driver hands back a string
// and a plain time.Time scan fails.
func TestSQLiteTimeScan(t *testing.T) {
	want := time.Date(2026, 8, 4, 9, 30, 15, 0, time.UTC)

	tests := []struct {
		name    string
		input   any
		want    time.Time
		wantErr bool
	}{
		{"datetime('now') text", "2026-08-04 09:30:15", want, false},
		{"same value as bytes", []byte("2026-08-04 09:30:15"), want, false},
		{"rfc3339 from another writer", "2026-08-04T09:30:15Z", want, false},
		{"driver already gave a time", want, want, false},
		{"null leaves the zero value", nil, time.Time{}, false},
		{"empty string leaves the zero value", "", time.Time{}, false},
		{"unrecognised text is an error", "not a time", time.Time{}, true},
		{"unsupported type is an error", 42, time.Time{}, true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var got time.Time
			err := sqliteTime{&got}.Scan(tc.input)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("Scan(%v) = nil error, want an error", tc.input)
				}
				return
			}
			if err != nil {
				t.Fatalf("Scan(%v): %v", tc.input, err)
			}
			if !got.Equal(tc.want) {
				t.Errorf("got %s, want %s", got, tc.want)
			}
		})
	}
}
