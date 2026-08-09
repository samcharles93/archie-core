package logging

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTaskLogPath(t *testing.T) {
	tests := []struct {
		name    string
		baseDir string
		taskID  int64
		attempt int
		want    string
	}{
		{"first attempt", "/var/log/archie/tasks", 42, 1, "/var/log/archie/tasks/42/attempt-1.jsonl"},
		{"later attempt", "/var/log/archie/tasks", 42, 3, "/var/log/archie/tasks/42/attempt-3.jsonl"},
		{"different task", "/var/log/archie/tasks", 7, 1, "/var/log/archie/tasks/7/attempt-1.jsonl"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := TaskLogPath(tt.baseDir, tt.taskID, tt.attempt)
			want := filepath.FromSlash(tt.want)
			if got != want {
				t.Errorf("TaskLogPath(%q, %d, %d) = %q, want %q", tt.baseDir, tt.taskID, tt.attempt, got, want)
			}
		})
	}
}

// TestNewTaskSinkWritesEntriesReadableByTail pins that a task sink's on-disk
// format is exactly what Tail/Query/Components already know how to parse --
// a task's log history must be readable with the same reader the daemon-wide
// log uses, not a second incompatible format.
func TestNewTaskSinkWritesEntriesReadableByTail(t *testing.T) {
	baseDir := t.TempDir()

	sink, err := NewTaskSink(baseDir, 42, 1, TaskSinkOptions{})
	if err != nil {
		t.Fatalf("NewTaskSink: %v", err)
	}
	sink.Logger().Warn("gate failed", "component", "gate", "cmd", "go build ./...")
	if err := sink.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	path := TaskLogPath(baseDir, 42, 1)
	result, err := Tail(path, Query{})
	if err != nil {
		t.Fatalf("Tail: %v", err)
	}
	if len(result.Entries) != 1 {
		t.Fatalf("got %d entries, want 1: %+v", len(result.Entries), result.Entries)
	}
	entry := result.Entries[0]
	if entry.Message != "gate failed" {
		t.Errorf("Message = %q, want %q", entry.Message, "gate failed")
	}
	if entry.Level != "WARN" {
		t.Errorf("Level = %q, want WARN", entry.Level)
	}
	if got := entry.Fields["component"]; got != "gate" {
		t.Errorf("Fields[component] = %v, want gate", got)
	}
}

func TestNewTaskSinkCreatesNestedTaskDirectory(t *testing.T) {
	baseDir := t.TempDir()

	sink, err := NewTaskSink(baseDir, 99, 1, TaskSinkOptions{})
	if err != nil {
		t.Fatalf("NewTaskSink: %v", err)
	}
	t.Cleanup(func() { _ = sink.Close() })

	if _, err := os.Stat(TaskLogPath(baseDir, 99, 1)); err != nil {
		t.Errorf("task log file not created: %v", err)
	}
}

// TestNewTaskSinkHonoursRotationOptions confirms TaskSinkOptions.MaxSizeMB
// and .Keep both actually reach the underlying rotatingFile, not just
// MaxSizeMB. Writing enough to rotate past DefaultKeep (5) generations while
// asking for Keep: 2 is deliberate: if Keep were silently dropped and
// defaulted, generation .3 would still exist and this test would catch it --
// a smaller write volume that only ever produces 1-2 rotations would pass
// whether or not Keep was wired through at all. Rotation ordering itself
// (which generation holds what) is already covered by TestRotation; this
// only checks that TaskSink forwards both options.
func TestNewTaskSinkHonoursRotationOptions(t *testing.T) {
	baseDir := t.TempDir()

	sink, err := NewTaskSink(baseDir, 1, 1, TaskSinkOptions{MaxSizeMB: 1, Keep: 2})
	if err != nil {
		t.Fatalf("NewTaskSink: %v", err)
	}
	line := strings.Repeat("x", 64*1024)
	for range 200 { // ~12MB against a 1MB cap: well past DefaultKeep (5) generations.
		sink.Logger().Info(line)
	}
	if err := sink.Close(); err != nil {
		t.Fatal(err)
	}

	path := TaskLogPath(baseDir, 1, 1)
	for i := 1; i <= 2; i++ {
		if _, err := os.Stat(fmt.Sprintf("%s.%d", path, i)); err != nil {
			t.Errorf("rotated file .%d missing, want rotation to have engaged: %v", i, err)
		}
	}
	if _, err := os.Stat(path + ".3"); err == nil {
		t.Error("generation .3 exists, want retention capped at Keep=2 (not the default of 5, " +
			"which .3 surviving would indicate Keep was dropped)")
	}
}

func TestNewTaskSinkLevelFiltersEntries(t *testing.T) {
	baseDir := t.TempDir()

	sink, err := NewTaskSink(baseDir, 1, 1, TaskSinkOptions{Level: "warn"})
	if err != nil {
		t.Fatalf("NewTaskSink: %v", err)
	}
	sink.Logger().Info("info-line")
	sink.Logger().Warn("warn-line")
	if err := sink.Close(); err != nil {
		t.Fatal(err)
	}

	body, err := os.ReadFile(TaskLogPath(baseDir, 1, 1))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "info-line") {
		t.Error("info-line present, want it filtered at warn level")
	}
	if !strings.Contains(string(body), "warn-line") {
		t.Error("warn-line missing")
	}
}

// TestNewTaskSinkRejectsUnusableDirectory confirms a task sink surfaces the
// error instead of quietly falling back -- unlike the daemon-wide New, which
// intentionally degrades to stderr because losing the daemon over a log path
// is worse than losing the durable copy. A task sink has no such tradeoff:
// there is no equivalent fallback destination for one task's log, so a task
// sink that fails to open should fail loudly and let the caller decide.
func TestNewTaskSinkRejectsUnusableDirectory(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := NewTaskSink(blocker, 1, 1, TaskSinkOptions{})
	if err == nil {
		t.Error("NewTaskSink returned nil error for an unusable base directory")
	}
}

func TestTaskSinkOutputIsValidJSONLines(t *testing.T) {
	baseDir := t.TempDir()
	sink, err := NewTaskSink(baseDir, 1, 1, TaskSinkOptions{})
	if err != nil {
		t.Fatalf("NewTaskSink: %v", err)
	}
	sink.Logger().Info("one")
	sink.Logger().Info("two")
	if err := sink.Close(); err != nil {
		t.Fatal(err)
	}

	body, err := os.ReadFile(TaskLogPath(baseDir, 1, 1))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(body)), "\n")
	if len(lines) != 2 {
		t.Fatalf("got %d lines, want 2", len(lines))
	}
	for _, line := range lines {
		var raw map[string]any
		if err := json.Unmarshal([]byte(line), &raw); err != nil {
			t.Errorf("line is not JSON: %v (%q)", err, line)
		}
	}
}
