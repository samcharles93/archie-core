package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func execRead(t *testing.T, cwd, params string) Result {
	t.Helper()
	tool := NewReadTool(cwd, nil)
	res, err := tool.Execute(context.Background(), json.RawMessage(params), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	return res
}

func TestReadTool_RawContentNoGutter(t *testing.T) {
	tmp := t.TempDir()
	writeReadTestFile(t, tmp, "f.txt", "alpha\nbeta\ngamma\n")

	res := execRead(t, tmp, `{"path": "f.txt"}`)
	if res.IsError {
		t.Fatalf("unexpected tool error: %s", res.Content)
	}
	if strings.Contains(res.Content, "│") {
		t.Fatalf("expected raw content without line-number gutter, got:\n%s", res.Content)
	}
	if !strings.HasPrefix(res.Content, "alpha\nbeta\ngamma") {
		t.Fatalf("unexpected content:\n%s", res.Content)
	}
}

func TestReadToolAcceptsFileAlias(t *testing.T) {
	tmp := t.TempDir()
	writeReadTestFile(t, tmp, "f.txt", "alpha\nbeta\n")

	res := execRead(t, tmp, `{"file":"f.txt"}`)
	if res.IsError || !strings.HasPrefix(res.Content, "alpha\nbeta") {
		t.Fatalf("file alias result = %#v", res)
	}
}

func TestReadToolDefaultsToBoundedOutput(t *testing.T) {
	tmp := t.TempDir()
	lines := make([]string, DefaultReadLines+10)
	for i := range lines {
		lines[i] = fmt.Sprintf("line %d", i+1)
	}
	writeReadTestFile(t, tmp, "f.txt", strings.Join(lines, "\n"))

	res := execRead(t, tmp, `{"path":"f.txt"}`)
	if res.IsError || !strings.Contains(res.Content, fmt.Sprintf("Use offset=%d to continue", DefaultReadLines+1)) {
		t.Fatalf("bounded read result = %s", res.Content)
	}
}

func TestReadTool_UserLimitContinuationNotice(t *testing.T) {
	tmp := t.TempDir()
	var lines []string
	for i := 1; i <= 50; i++ {
		lines = append(lines, fmt.Sprintf("line %d", i))
	}
	writeReadTestFile(t, tmp, "f.txt", strings.Join(lines, "\n"))

	res := execRead(t, tmp, `{"path": "f.txt", "offset": 10, "limit": 5}`)
	if res.IsError {
		t.Fatalf("unexpected tool error: %s", res.Content)
	}
	if !strings.HasPrefix(res.Content, "line 10") {
		t.Fatalf("expected content to start at line 10, got:\n%s", res.Content)
	}
	if !strings.Contains(res.Content, "Use offset=15 to continue") {
		t.Fatalf("expected continuation notice with offset=15, got:\n%s", res.Content)
	}
	if !strings.Contains(res.Content, "more lines in file") {
		t.Fatalf("expected remaining-lines notice, got:\n%s", res.Content)
	}
}

func TestReadTool_TruncationContinuationNotice(t *testing.T) {
	tmp := t.TempDir()
	var lines []string
	for i := 1; i <= DefaultMaxLines+500; i++ {
		lines = append(lines, fmt.Sprintf("line %d", i))
	}
	writeReadTestFile(t, tmp, "f.txt", strings.Join(lines, "\n"))

	res := execRead(t, tmp, `{"path": "f.txt", "full": true}`)
	if res.IsError {
		t.Fatalf("unexpected tool error: %s", res.Content)
	}
	notice := fmt.Sprintf("[showing lines 1-%d of %d. Use offset=%d to continue.]", DefaultMaxLines, DefaultMaxLines+500, DefaultMaxLines+1)
	if !strings.Contains(res.Content, notice) {
		t.Fatalf("expected notice %q, got tail:\n%s", notice, res.Content[len(res.Content)-200:])
	}
}

func TestReadTool_FirstLineExceedsByteLimit(t *testing.T) {
	tmp := t.TempDir()
	writeReadTestFile(t, tmp, "big.min.js", strings.Repeat("x", DefaultMaxBytes+100))

	res := execRead(t, tmp, `{"path": "big.min.js"}`)
	if res.IsError {
		t.Fatalf("unexpected tool error: %s", res.Content)
	}
	if !strings.Contains(res.Content, "Use shell: sed -n '1p'") {
		t.Fatalf("expected shell fallback hint, got:\n%s", res.Content)
	}
}

func writeReadTestFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestReadTool_DirectoryReturnsListing(t *testing.T) {
	tmp := t.TempDir()
	writeReadTestFile(t, tmp, "alpha.go", "package a\n")
	writeReadTestFile(t, tmp, "beta.go", "package b\n")
	if err := os.Mkdir(filepath.Join(tmp, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}

	res := execRead(t, tmp, `{"path": "."}`)
	if res.IsError {
		t.Fatalf("expected a listing, not an error: %s", res.Content)
	}
	for _, want := range []string{"alpha.go", "beta.go", "sub/"} {
		if !strings.Contains(res.Content, want) {
			t.Fatalf("listing missing %q, got:\n%s", want, res.Content)
		}
	}
	if !strings.Contains(res.Content, "is a directory") {
		t.Fatalf("expected the listing to say the path is a directory, got:\n%s", res.Content)
	}
}

// A directory must not satisfy the read-before-write check: marking it read
// would let a later write to a same-named file bypass the guard.
func TestReadTool_DirectoryDoesNotMarkRead(t *testing.T) {
	tmp := t.TempDir()
	rt := NewReadTracker()
	tool := NewReadTool(tmp, rt)
	res, err := tool.Execute(context.Background(), json.RawMessage(`{"path": "."}`), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected tool error: %s", res.Content)
	}
	if err := rt.CheckRead(tmp, "."); err == nil {
		t.Fatal("reading a directory must not mark it as read")
	}
}

// 5 reads in the analysed corpus failed on plausible-but-wrong paths, each
// costing a round trip plus a follow-up find.
func TestReadTool_NotFoundSuggestsNearestPaths(t *testing.T) {
	tmp := t.TempDir()
	if err := os.MkdirAll(filepath.Join(tmp, "internal", "ui", "theme"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeReadTestFile(t, filepath.Join(tmp, "internal", "ui", "theme"), "theme.go", "package theme\n")

	res := execRead(t, tmp, `{"path":"internal/theme/theme.go"}`)
	if !res.IsError {
		t.Fatal("expected a not-found error")
	}
	if !strings.Contains(res.Content, filepath.Join("internal", "ui", "theme", "theme.go")) {
		t.Fatalf("expected the real path to be suggested, got:\n%s", res.Content)
	}
}

func TestReadTool_NotFoundWithNoCandidateStaysPlain(t *testing.T) {
	tmp := t.TempDir()
	writeReadTestFile(t, tmp, "other.go", "package other\n")

	res := execRead(t, tmp, `{"path":"internal/nothing/absent.go"}`)
	if !res.IsError {
		t.Fatal("expected a not-found error")
	}
	if strings.Contains(res.Content, "did you mean") {
		t.Fatalf("no candidate should mean no suggestion, got:\n%s", res.Content)
	}
}

// The real-world miss was internal/theme/theme.go: the directory exists but
// holds differently-named files, so a basename search finds nothing. Listing
// the directory that does exist is the useful answer.
func TestReadTool_NotFoundListsExistingParentDir(t *testing.T) {
	tmp := t.TempDir()
	if err := os.MkdirAll(filepath.Join(tmp, "internal", "theme"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeReadTestFile(t, filepath.Join(tmp, "internal", "theme"), "colors.go", "package theme\n")

	res := execRead(t, tmp, `{"path":"internal/theme/theme.go"}`)
	if !res.IsError {
		t.Fatal("expected a not-found error")
	}
	if !strings.Contains(res.Content, "colors.go") {
		t.Fatalf("expected the existing directory to be listed, got:\n%s", res.Content)
	}
}
