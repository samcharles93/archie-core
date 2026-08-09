package logging

import (
	"os"
	"sync"
	"testing"
)

func TestTaskRegistryOpenWriteCloseRoundTripsThroughTail(t *testing.T) {
	baseDir := t.TempDir()
	feed := NewFeed(10)
	reg := NewTaskRegistry(baseDir, feed, TaskSinkOptions{})

	if err := reg.Open(42, 1); err != nil {
		t.Fatalf("Open: %v", err)
	}
	if ok := reg.Write(42, Entry{Level: "WARN", Message: "gate failed", Fields: map[string]any{"component": "gate"}}); !ok {
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
	reg.Write(1, Entry{Level: "INFO", Message: "hello"})
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
	if ok := reg.Write(999, Entry{Message: "orphan"}); ok {
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
	reg.Write(7, Entry{Message: "attempt one"})

	if err := reg.Open(7, 2); err != nil {
		t.Fatalf("Open attempt 2: %v", err)
	}
	reg.Write(7, Entry{Message: "attempt two"})
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
	reg.Write(5, Entry{Message: "x"})
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
	if ok := reg.Write(1, Entry{Message: "x"}); ok {
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
			reg.Write(taskID, Entry{Message: "line"})
			if err := reg.Close(taskID); err != nil {
				t.Errorf("Close(%d): %v", taskID, err)
			}
		}(i)
	}
	wg.Wait()
}
