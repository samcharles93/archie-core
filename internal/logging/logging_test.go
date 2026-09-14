package logging

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNewWritesToFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "archied.log")

	log, closer, err := New(Options{File: path})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	log.Info("hello", "task", 42)
	if err := closer.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	var entry map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(string(body))), &entry); err != nil {
		t.Fatalf("log line is not JSON: %v (%q)", err, body)
	}
	if entry["msg"] != "hello" {
		t.Errorf("msg = %v, want hello", entry["msg"])
	}
}

func TestNewCreatesParentDirectory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "deeper", "archied.log")
	_, closer, err := New(Options{File: path})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = closer.Close() })
	if _, err := os.Stat(path); err != nil {
		t.Errorf("log file not created: %v", err)
	}
}

// TestNewFallsBackWhenFileUnavailable pins that an unopenable log file
// degrades to stderr with an error, rather than leaving the daemon without a
// logger or refusing to start. Losing the durable copy must not take the
// daemon down with it.
func TestNewFallsBackWhenFileUnavailable(t *testing.T) {
	// A path whose parent is a regular file cannot be created.
	blocker := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	log, closer, err := New(Options{File: filepath.Join(blocker, "archied.log")})
	if err == nil {
		t.Error("New returned nil error for an unusable path, want one reported")
	}
	if log == nil {
		t.Fatal("New returned a nil logger; a usable logger is required even on failure")
	}
	log.Info("still works")
	if closer == nil {
		t.Fatal("New returned a nil closer")
	}
	if err := closer.Close(); err != nil {
		t.Errorf("Close on the fallback closer: %v", err)
	}
}

func TestLevelFiltering(t *testing.T) {
	tests := []struct {
		name      string
		level     string
		wantDebug bool
		wantInfo  bool
	}{
		{"default is info", "", false, true},
		{"debug shows everything", "debug", true, true},
		{"warn hides info", "warn", false, false},
		{"error hides info", "error", false, false},
		{"unrecognised falls back to info", "chatty", false, true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "archied.log")
			log, closer, err := New(Options{File: path, Level: tc.level})
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			log.Debug("a-debug-line")
			log.Info("an-info-line")
			if err := closer.Close(); err != nil {
				t.Fatal(err)
			}

			body, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			got := string(body)
			if strings.Contains(got, "a-debug-line") != tc.wantDebug {
				t.Errorf("debug present = %v, want %v", !tc.wantDebug, tc.wantDebug)
			}
			if strings.Contains(got, "an-info-line") != tc.wantInfo {
				t.Errorf("info present = %v, want %v", !tc.wantInfo, tc.wantInfo)
			}
		})
	}
}

// TestRotation pins that the file is capped and old generations are kept in
// order, so a resident daemon cannot fill the disk.
func TestRotation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "archied.log")

	// 1MB cap, keep 2. Rotation is size-driven, so write past it.
	w, err := newRotatingFile(path, 1, 2)
	if err != nil {
		t.Fatalf("newRotatingFile: %v", err)
	}
	line := strings.Repeat("x", 64*1024) + "\n"
	for range 40 {
		if _, err := w.Write([]byte(line)); err != nil {
			t.Fatalf("Write: %v", err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("live log missing: %v", err)
	}
	if info.Size() > 2*1024*1024 {
		t.Errorf("live log is %d bytes, want it capped near 1MB", info.Size())
	}

	for i := 1; i <= 2; i++ {
		if _, err := os.Stat(fmt.Sprintf("%s.%d", path, i)); err != nil {
			t.Errorf("rotated file .%d missing: %v", i, err)
		}
	}
	// Retention is honoured: nothing beyond keep survives.
	if _, err := os.Stat(path + ".3"); err == nil {
		t.Error("archied.log.3 exists, want retention capped at 2")
	}
}

// TestRotationSurvivesFailedReopen pins that a failed rotation reopen (a
// full filesystem, a permission change hit exactly when archied tries to
// recreate the live file) does not take down the file sink permanently.
// The write that triggered rotation must not be silently swallowed, and
// -- unlike losing a single line -- every write after the failure must
// not be lost either.
func TestRotationSurvivesFailedReopen(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "archied.log")

	w, err := newRotatingFile(path, 1, 2)
	if err != nil {
		t.Fatalf("newRotatingFile: %v", err)
	}

	// Push size right up to the cap without writing megabytes of
	// fixture data -- white-box, same package -- so the marker write
	// below is guaranteed to be the one that crosses maxSize and
	// triggers rotate.
	marker := []byte("MARKER-LINE\n")
	w.size = w.maxSize - 1

	// Simulate the reopen failing (disk full, permission change) while
	// the rename that frees the old name still succeeds -- the scenario
	// a chmod on the directory can't isolate, since that would also
	// block the rename.
	prevOpenFile := openFile
	openFile = func(name string, flag int, perm os.FileMode) (*os.File, error) {
		return nil, fmt.Errorf("simulated reopen failure")
	}
	t.Cleanup(func() { openFile = prevOpenFile })

	if _, err := w.Write(marker); err != nil {
		t.Fatalf("Write after a failed reopen returned an error (line lost): %v", err)
	}

	// A second write after the same failure must also land -- this is
	// what distinguishes "lost one line during rotation" from "the sink
	// is now permanently broken".
	marker2 := []byte("MARKER-LINE-2\n")
	if _, err := w.Write(marker2); err != nil {
		t.Fatalf("second write after a failed reopen returned an error: %v", err)
	}

	openFile = prevOpenFile
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	// The live file was never successfully recreated (reopen kept
	// failing on every write), so each write's rotate attempt renamed
	// the same still-open descriptor further down the generation chain
	// -- the exact filename it lands under is incidental. What matters
	// is that neither marker is gone: scan the whole directory.
	var all strings.Builder
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		all.Write(b)
	}
	if !strings.Contains(all.String(), "MARKER-LINE\n") {
		t.Error("MARKER-LINE missing after a failed reopen")
	}
	if !strings.Contains(all.String(), "MARKER-LINE-2\n") {
		t.Error("MARKER-LINE-2 missing -- the sink stayed broken after the transient failure")
	}
}

func TestRotationDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "archied.log")
	w, err := newRotatingFile(path, 0, 0)
	if err != nil {
		t.Fatalf("newRotatingFile: %v", err)
	}
	t.Cleanup(func() { _ = w.Close() })

	if w.maxSize != int64(DefaultMaxSizeMB)*1024*1024 {
		t.Errorf("maxSize = %d, want the %dMB default", w.maxSize, DefaultMaxSizeMB)
	}
	if w.keep != DefaultKeep {
		t.Errorf("keep = %d, want the default %d", w.keep, DefaultKeep)
	}
}
