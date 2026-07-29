package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func numberedFile(t *testing.T, dir, name string, n int) string {
	t.Helper()
	lines := make([]string, n)
	for i := range lines {
		lines[i] = fmt.Sprintf("line %d", i+1)
	}
	writeReadTestFile(t, dir, name, strings.Join(lines, "\n"))
	return filepath.Join(dir, name)
}

// readTwice runs the read tool twice through one tracker, as a session would.
func readTwice(t *testing.T, cwd, first, second string) (Result, Result) {
	t.Helper()
	tool := NewReadTool(cwd, NewReadTracker())
	r1, err := tool.Execute(context.Background(), json.RawMessage(first), nil)
	if err != nil {
		t.Fatalf("first read: %v", err)
	}
	r2, err := tool.Execute(context.Background(), json.RawMessage(second), nil)
	if err != nil {
		t.Fatalf("second read: %v", err)
	}
	return r1, r2
}

func TestRead_UnchangedRepeatIsSuppressed(t *testing.T) {
	tmp := t.TempDir()
	numberedFile(t, tmp, "f.txt", 50)

	r1, r2 := readTwice(t, tmp, `{"path":"f.txt"}`, `{"path":"f.txt"}`)
	if r1.IsError || !strings.Contains(r1.Content, "line 50") {
		t.Fatalf("first read should return the body, got: %s", r1.Content)
	}
	if r2.IsError {
		t.Fatalf("second read errored: %s", r2.Content)
	}
	if strings.Contains(r2.Content, "line 50") {
		t.Fatalf("second read should not resend the body, got:\n%s", r2.Content)
	}
	if !strings.Contains(r2.Content, "unchanged") {
		t.Fatalf("second read should say the file is unchanged, got:\n%s", r2.Content)
	}
}

func TestRead_ChangedFileIsResent(t *testing.T) {
	tmp := t.TempDir()
	path := numberedFile(t, tmp, "f.txt", 20)

	tool := NewReadTool(tmp, NewReadTracker())
	if _, err := tool.Execute(context.Background(), json.RawMessage(`{"path":"f.txt"}`), nil); err != nil {
		t.Fatal(err)
	}

	// Rewrite with different content and a distinct mtime.
	if err := os.WriteFile(path, []byte("totally different\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, time.Now().Add(time.Second), time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}

	res, err := tool.Execute(context.Background(), json.RawMessage(`{"path":"f.txt"}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Content, "totally different") {
		t.Fatalf("a changed file must be resent in full, got:\n%s", res.Content)
	}
}

func TestRead_OverlappingRangeReturnsOnlyNovelLines(t *testing.T) {
	tmp := t.TempDir()
	numberedFile(t, tmp, "f.txt", 300)

	_, r2 := readTwice(t, tmp,
		`{"path":"f.txt","offset":1,"limit":100}`,
		`{"path":"f.txt","offset":1,"limit":200}`)

	if r2.IsError {
		t.Fatalf("second read errored: %s", r2.Content)
	}
	if strings.Contains(r2.Content, "line 50\n") {
		t.Fatalf("already-served lines were resent, got:\n%s", r2.Content)
	}
	if !strings.Contains(r2.Content, "line 101") || !strings.Contains(r2.Content, "line 200") {
		t.Fatalf("novel lines 101-200 missing, got:\n%s", r2.Content)
	}
}

func TestRead_DisjointRangeIsServedNormally(t *testing.T) {
	tmp := t.TempDir()
	numberedFile(t, tmp, "f.txt", 300)

	_, r2 := readTwice(t, tmp,
		`{"path":"f.txt","offset":1,"limit":50}`,
		`{"path":"f.txt","offset":200,"limit":50}`)

	if r2.IsError {
		t.Fatalf("second read errored: %s", r2.Content)
	}
	if !strings.Contains(r2.Content, "line 200") || !strings.Contains(r2.Content, "line 249") {
		t.Fatalf("a disjoint range must be served in full, got:\n%s", r2.Content)
	}
}

// full:true is the documented escape hatch: if the model has lost the earlier
// content (compaction, handoff) it must still be able to force a re-read.
func TestRead_FullBypassesSuppression(t *testing.T) {
	tmp := t.TempDir()
	numberedFile(t, tmp, "f.txt", 50)

	_, r2 := readTwice(t, tmp, `{"path":"f.txt"}`, `{"path":"f.txt","full":true}`)
	if !strings.Contains(r2.Content, "line 50") {
		t.Fatalf("full:true must bypass suppression, got:\n%s", r2.Content)
	}
}

// The suppression notice has to tell the model how to force a re-read,
// otherwise a model that has lost the content has no recovery path.
func TestRead_SuppressionNoticeNamesTheEscapeHatch(t *testing.T) {
	tmp := t.TempDir()
	numberedFile(t, tmp, "f.txt", 50)

	_, r2 := readTwice(t, tmp, `{"path":"f.txt"}`, `{"path":"f.txt"}`)
	if !strings.Contains(r2.Content, "full") {
		t.Fatalf("notice must mention full:true as the escape hatch, got:\n%s", r2.Content)
	}
}

// Suppression must not weaken the read-before-write guard.
func TestRead_SuppressedReadStillCountsAsRead(t *testing.T) {
	tmp := t.TempDir()
	numberedFile(t, tmp, "f.txt", 20)

	rt := NewReadTracker()
	tool := NewReadTool(tmp, rt)
	for range 2 {
		if _, err := tool.Execute(context.Background(), json.RawMessage(`{"path":"f.txt"}`), nil); err != nil {
			t.Fatal(err)
		}
	}
	if err := rt.CheckRead(tmp, "f.txt"); err != nil {
		t.Fatalf("a suppressed re-read must still satisfy read-before-write: %v", err)
	}
}
