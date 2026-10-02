package logging

import (
	"fmt"
	"os"
	"sync"
	"testing"
	"time"
)

func TestTaskRegistryOpenWriteCloseRoundTripsThroughTail(t *testing.T) {
	baseDir := t.TempDir()
	feed := NewFeed(10)
	reg := NewTaskRegistry(baseDir, feed, TaskSinkOptions{})

	if err := reg.Open(42, 1); err != nil {
		t.Fatalf("Open: %v", err)
	}
	if ok := reg.Write(t.Context(), 42, Entry{Level: "WARN", Message: "gate failed", Fields: map[string]any{"component": "gate"}}); !ok {
		t.Fatal("Write() = false, want true for an open task")
	}
	if err := reg.Close(42); err != nil {
		t.Fatalf("Close: %v", err)
	}

	result, err := Tail(TaskLogPath(baseDir, 42, 1), Query{})
	if err != nil {
		t.Fatalf("Tail: %v", err)
	}
	if len(result.Entries) != 1 {
		t.Fatalf("got %d entries, want 1: %+v", len(result.Entries), result.Entries)
	}
	entry := result.Entries[0]
	if entry.Message != "gate failed" || entry.Level != "WARN" || entry.Fields["component"] != "gate" {
		t.Errorf("entry = %+v, want message=gate failed level=WARN component=gate", entry)
	}
}

// TestTaskRegistryWriteKeepsEachEntriesOwnEventTime guards the point of a
// task log: a record shipped from the container must be filed under the time
// the event happened, not the time the daemon happened to receive it. A run's
// iterations spread over minutes must be interleaved on disk the way they
// happened; collapsing them onto one receipt burst makes a task's timing
// unrecoverable (which stage ran when, how long an iteration took, whether a
// stage stalled). The daemon's receipt time is kept too, but separately -- in
// the record's own received_at field -- so "the event's time" and "when this
// process saw it" cannot be confused for one another.
func TestTaskRegistryWriteKeepsEachEntriesOwnEventTime(t *testing.T) {
	baseDir := t.TempDir()
	feed := NewFeed(10)
	reg := NewTaskRegistry(baseDir, feed, TaskSinkOptions{})

	if err := reg.Open(11, 1); err != nil {
		t.Fatal(err)
	}
	// Three iterations, minutes apart -- the shape a real run has.
	happened := []time.Time{
		time.Date(2026, 10, 1, 9, 30, 0, 123456789, time.UTC),
		time.Date(2026, 10, 1, 9, 33, 41, 987654321, time.UTC),
		time.Date(2026, 10, 1, 9, 37, 12, 500000000, time.UTC),
	}
	for i, at := range happened {
		if ok := reg.Write(t.Context(), 11, Entry{Time: at, Level: "INFO", Message: fmt.Sprintf("iteration %d", i+1)}); !ok {
			t.Fatalf("Write() = false for an open task, iteration %d", i)
		}
	}
	if err := reg.Close(11); err != nil {
		t.Fatal(err)
	}

	result, err := Tail(TaskLogPath(baseDir, 11, 1), Query{})
	if err != nil {
		t.Fatalf("Tail: %v", err)
	}
	if len(result.Entries) != len(happened) {
		t.Fatalf("got %d entries, want %d", len(result.Entries), len(happened))
	}
	for i, want := range happened {
		entry := result.Entries[i]
		if !entry.Time.Equal(want) {
			t.Errorf("entry %d time = %s, want the iteration's own time %s", i, entry.Time, want)
		}
		if _, ok := entry.Fields[receivedAtField]; !ok {
			t.Errorf("entry %d fields = %+v, want receipt time preserved separately as received_at", i, entry.Fields)
		}
	}

	// The live dashboard feed must carry the event's time too, not the
	// mirror's own write time.
	snapshot := feed.Snapshot()
	if len(snapshot) != len(happened) {
		t.Fatalf("feed has %d entries, want %d", len(snapshot), len(happened))
	}
	for i, want := range happened {
		if !snapshot[i].Time.Equal(want) {
			t.Errorf("feed entry %d time = %s, want %s", i, snapshot[i].Time, want)
		}
	}
}

// TestTaskRegistryWriteStampsAnEntryWithNoEventTime pins the fallback: a
// locally-produced entry that carries no time of its own (recordPark does set
// one, but the field is optional) is stamped now, so a log line is never
// filed under the zero time and never sinks to the bottom of a time-ordered
// view.
func TestTaskRegistryWriteStampsAnEntryWithNoEventTime(t *testing.T) {
	baseDir := t.TempDir()
	reg := NewTaskRegistry(baseDir, NewFeed(10), TaskSinkOptions{})

	if err := reg.Open(12, 1); err != nil {
		t.Fatal(err)
	}
	before := time.Now()
	if ok := reg.Write(t.Context(), 12, Entry{Level: "INFO", Message: "no time of its own"}); !ok {
		t.Fatal("Write() = false for an open task")
	}
	after := time.Now()
	if err := reg.Close(12); err != nil {
		t.Fatal(err)
	}

	result, err := Tail(TaskLogPath(baseDir, 12, 1), Query{})
	if err != nil {
		t.Fatalf("Tail: %v", err)
	}
	if len(result.Entries) != 1 {
		t.Fatalf("got %d entries, want 1", len(result.Entries))
	}
	got := result.Entries[0].Time
	if got.Before(before) || got.After(after) {
		t.Errorf("entry time = %s, want a receipt-time stamp between %s and %s", got, before, after)
	}
}

// TestTaskRegistryWriteMirrorsToTheDashboardFeed guards the other half of
// the point of routing writes through a slog.Logger built on FeedHandler
// instead of writing the file directly: a remotely-shipped entry must show
// up live on the dashboard the same way a locally-written one does.
func TestTaskRegistryWriteMirrorsToTheDashboardFeed(t *testing.T) {
	baseDir := t.TempDir()
	feed := NewFeed(10)
	reg := NewTaskRegistry(baseDir, feed, TaskSinkOptions{})

	if err := reg.Open(1, 1); err != nil {
		t.Fatalf("Open: %v", err)
	}
	reg.Write(t.Context(), 1, Entry{Level: "INFO", Message: "hello"})
	if err := reg.Close(1); err != nil {
		t.Fatal(err)
	}

	snapshot := feed.Snapshot()
	if len(snapshot) != 1 || snapshot[0].Message != "hello" {
		t.Fatalf("feed snapshot = %+v, want one entry with message %q", snapshot, "hello")
	}
}

// TestTaskRegistryWriteWithoutOpenIsANoOp pins that a system-log message for
// a task with no currently-open sink -- a late or duplicate delivery after
// the task finished, or one this daemon instance never dispatched -- is
// expected, not exceptional: no panic, no error return path to check.
func TestTaskRegistryWriteWithoutOpenIsANoOp(t *testing.T) {
	reg := NewTaskRegistry(t.TempDir(), NewFeed(10), TaskSinkOptions{})
	if ok := reg.Write(t.Context(), 999, Entry{Message: "orphan"}); ok {
		t.Error("Write() = true for a task with no open sink, want false")
	}
}

// TestTaskRegistryOpenReplacesThePreviousAttempt guards a retry: the same
// task ID reopens at a higher Attempt, and writes after that must land in
// the new attempt's file, not silently keep flowing to the old one under
// its name.
func TestTaskRegistryOpenReplacesThePreviousAttempt(t *testing.T) {
	baseDir := t.TempDir()
	reg := NewTaskRegistry(baseDir, NewFeed(10), TaskSinkOptions{})

	if err := reg.Open(7, 1); err != nil {
		t.Fatalf("Open attempt 1: %v", err)
	}
	reg.Write(t.Context(), 7, Entry{Message: "attempt one"})

	if err := reg.Open(7, 2); err != nil {
		t.Fatalf("Open attempt 2: %v", err)
	}
	reg.Write(t.Context(), 7, Entry{Message: "attempt two"})
	if err := reg.Close(7); err != nil {
		t.Fatal(err)
	}

	attemptOne, err := Tail(TaskLogPath(baseDir, 7, 1), Query{})
	if err != nil {
		t.Fatal(err)
	}
	if len(attemptOne.Entries) != 1 || attemptOne.Entries[0].Message != "attempt one" {
		t.Errorf("attempt 1 file = %+v, want exactly [attempt one]", attemptOne.Entries)
	}

	attemptTwo, err := Tail(TaskLogPath(baseDir, 7, 2), Query{})
	if err != nil {
		t.Fatal(err)
	}
	if len(attemptTwo.Entries) != 1 || attemptTwo.Entries[0].Message != "attempt two" {
		t.Errorf("attempt 2 file = %+v, want exactly [attempt two]", attemptTwo.Entries)
	}
}

func TestTaskRegistryCloseWithoutOpenIsSafe(t *testing.T) {
	reg := NewTaskRegistry(t.TempDir(), NewFeed(10), TaskSinkOptions{})
	if err := reg.Close(123); err != nil {
		t.Errorf("Close() on a never-opened task = %v, want nil", err)
	}
}

func TestTaskRegistryRemoveDeletesTheTaskDirectory(t *testing.T) {
	baseDir := t.TempDir()
	reg := NewTaskRegistry(baseDir, NewFeed(10), TaskSinkOptions{})

	if err := reg.Open(5, 1); err != nil {
		t.Fatal(err)
	}
	reg.Write(t.Context(), 5, Entry{Message: "x"})
	if err := reg.Close(5); err != nil {
		t.Fatal(err)
	}

	dir := TaskLogDir(baseDir, 5)
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("task dir missing before Remove: %v", err)
	}
	if err := reg.Remove(5); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("task dir still present after Remove: err=%v", err)
	}
}

func TestTaskRegistryRemoveOnUnopenedTaskIsNotAnError(t *testing.T) {
	reg := NewTaskRegistry(t.TempDir(), NewFeed(10), TaskSinkOptions{})
	if err := reg.Remove(404); err != nil {
		t.Errorf("Remove() on a task with no directory = %v, want nil", err)
	}
}

// TestTaskRegistryNilReceiverIsSafe mirrors Feed's own nil-safety: a
// *TaskRegistry is wired by composition and left nil when task logging
// isn't configured, the same "optional, nil means disabled" convention
// every other Daemon field follows. Every method must tolerate that without
// the caller checking first.
func TestTaskRegistryNilReceiverIsSafe(t *testing.T) {
	var reg *TaskRegistry

	if err := reg.Open(1, 1); err != nil {
		t.Errorf("Open() on nil registry = %v, want nil", err)
	}
	if ok := reg.Write(t.Context(), 1, Entry{Message: "x"}); ok {
		t.Error("Write() on nil registry = true, want false")
	}
	if err := reg.Close(1); err != nil {
		t.Errorf("Close() on nil registry = %v, want nil", err)
	}
	if err := reg.Remove(1); err != nil {
		t.Errorf("Remove() on nil registry = %v, want nil", err)
	}
}

// TestTaskRegistryConcurrentTasksDoNotRace exercises the registry the way
// the daemon actually will: many tasks' Open/Write/Close interleaved from
// different goroutines (task dispatch vs. the NATS subscription handler),
// TestTaskRegistryPathMirrorsTaskLogPath guards the reason a query surface
// (the API handler, the chat tool) can hold only a *TaskRegistry rather than
// also needing baseDir threaded through separately: Path must compute
// exactly what TaskLogPath would from the same baseDir.
func TestTaskRegistryPathMirrorsTaskLogPath(t *testing.T) {
	baseDir := t.TempDir()
	reg := NewTaskRegistry(baseDir, nil, TaskSinkOptions{})

	got := reg.Path(42, 3)
	want := TaskLogPath(baseDir, 42, 3)
	if got != want {
		t.Errorf("Path(42, 3) = %q, want %q", got, want)
	}
}

// TestNilTaskRegistryPathIsEmpty guards the "nil means task logging is not
// configured" convention every other TaskRegistry method already follows --
// a query surface must be able to call Path on a possibly-nil registry
// without a separate nil check.
func TestNilTaskRegistryPathIsEmpty(t *testing.T) {
	var reg *TaskRegistry
	if got := reg.Path(42, 1); got != "" {
		t.Errorf("Path() on nil registry = %q, want empty", got)
	}
}

// none sharing a task ID. Run with -race.
func TestTaskRegistryConcurrentTasksDoNotRace(t *testing.T) {
	reg := NewTaskRegistry(t.TempDir(), NewFeed(100), TaskSinkOptions{})
	var wg sync.WaitGroup
	for i := int64(1); i <= 20; i++ {
		wg.Add(1)
		go func(taskID int64) {
			defer wg.Done()
			if err := reg.Open(taskID, 1); err != nil {
				t.Errorf("Open(%d): %v", taskID, err)
				return
			}
			reg.Write(t.Context(), taskID, Entry{Message: "line"})
			if err := reg.Close(taskID); err != nil {
				t.Errorf("Close(%d): %v", taskID, err)
			}
		}(i)
	}
	wg.Wait()
}
