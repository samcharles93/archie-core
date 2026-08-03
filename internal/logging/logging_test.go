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
