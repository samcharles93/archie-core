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

type staticGrepIndex struct{ files []string }

func (s staticGrepIndex) Candidates(context.Context, string, bool, bool) ([]string, bool) {
	return s.files, true
}

func TestGrepUsesIndexCandidatesAsAdvisoryFileSet(t *testing.T) {
	tmp := t.TempDir()
	indexed := filepath.Join(tmp, "indexed.txt")
	createGrepTestFile(t, tmp, "indexed.txt", "needle\n")
	createGrepTestFile(t, tmp, "excluded.txt", "needle\n")
	tool := NewGrepTool(tmp, staticGrepIndex{files: []string{indexed}})
	result, err := tool.Execute(context.Background(), json.RawMessage(`{"pattern":"needle"}`), nil)
	if err != nil || result.IsError {
		t.Fatalf("grep result = %#v, err = %v", result, err)
	}
	if !strings.Contains(result.Content, "indexed.txt") || strings.Contains(result.Content, "excluded.txt") {
		t.Fatalf("candidate-filtered output = %s", result.Content)
	}
}

func TestGrepRetriesDirectSearchWhenIndexedCandidatesAreStale(t *testing.T) {
	tmp := t.TempDir()
	createGrepTestFile(t, tmp, "live.txt", "needle\n")
	missing := filepath.Join(tmp, "deleted.txt")
	tool := NewGrepTool(tmp, staticGrepIndex{files: []string{missing}})
	result, err := tool.Execute(context.Background(), json.RawMessage(`{"pattern":"needle"}`), nil)
	if err != nil || result.IsError {
		t.Fatalf("grep result = %#v, err = %v", result, err)
	}
	if !strings.Contains(result.Content, "live.txt") || result.MetricLabels["search_backend"] != "direct" {
		t.Fatalf("fallback output = %s, labels = %v", result.Content, result.MetricLabels)
	}
}

func TestGrepDiscardsPartialIndexedOutputOnCandidateError(t *testing.T) {
	tmp := t.TempDir()
	live := filepath.Join(tmp, "live.txt")
	createGrepTestFile(t, tmp, "live.txt", "needle\n")
	createGrepTestFile(t, tmp, "also-live.txt", "needle\n")
	missing := filepath.Join(tmp, "deleted.txt")
	tool := NewGrepTool(tmp, staticGrepIndex{files: []string{live, missing}})
	result, err := tool.Execute(context.Background(), json.RawMessage(`{"pattern":"needle"}`), nil)
	if err != nil || result.IsError {
		t.Fatalf("grep result = %#v, err = %v", result, err)
	}
	if !strings.Contains(result.Content, "also-live.txt") || result.MetricLabels["search_backend"] != "direct" {
		t.Fatalf("partial indexed output was not replaced: %s, labels = %v", result.Content, result.MetricLabels)
	}
}

func TestGrepFallback(t *testing.T) {
	// Create a temporary directory with test files.
	tmp := t.TempDir()
	createGrepTestFile(t, tmp, "alpha.go", "package alpha\nfunc Hello() {}\n")
	createGrepTestFile(t, tmp, "beta.go", "package beta\nfunc World() {}\nfunc hello() {}\n")
	createGrepTestFile(t, tmp, "gamma.txt", "Hello world\n")
	createGrepTestFile(t, tmp, "vendor/mod.go", "package vendor\nfunc Hello() {}\n")
	createGrepTestFile(t, tmp, "subdir/nested.go", "package nested\nfunc HelloThere() {}\n")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cases := []struct {
		name      string
		params    GrepParams
		want      []string
		wantEmpty bool
	}{
		{
			name:   "literal match in file",
			params: GrepParams{Pattern: "Hello", Path: filepath.Join(tmp, "alpha.go")},
			want:   []string{"alpha.go:2:func Hello() {}"},
		},
		{
			// Smart case: an uppercase letter in the pattern makes the
			// search case-sensitive, matching ripgrep's --smart-case.
			name:   "match in directory (smart case)",
			params: GrepParams{Pattern: "Hello", Path: tmp},
			want: []string{
				"alpha.go:2:func Hello() {}",
				"gamma.txt:1:Hello world",
				"subdir/nested.go:2:func HelloThere() {}",
				"vendor/mod.go:2:func Hello() {}",
			},
		},
		{
			name:   "lowercase pattern matches both cases",
			params: GrepParams{Pattern: "hello", Path: tmp},
			want: []string{
				"alpha.go:2:func Hello() {}",
				"beta.go:3:func hello() {}",
				"gamma.txt:1:Hello world",
				"subdir/nested.go:2:func HelloThere() {}",
				"vendor/mod.go:2:func Hello() {}",
			},
		},
		{
			name:   "case sensitive literal",
			params: GrepParams{Pattern: "Hello", Path: tmp, CaseSensitive: true},
			want: []string{
				"alpha.go:2:func Hello() {}",
				"gamma.txt:1:Hello world",
				"subdir/nested.go:2:func HelloThere() {}",
				"vendor/mod.go:2:func Hello() {}",
			},
		},
		{
			name:   "regex match by default",
			params: GrepParams{Pattern: "Hello.*", Path: tmp},
			want: []string{
				"alpha.go:2:func Hello() {}",
				"gamma.txt:1:Hello world",
				"subdir/nested.go:2:func HelloThere() {}",
				"vendor/mod.go:2:func Hello() {}",
			},
		},
		{
			name:   "regex alternation",
			params: GrepParams{Pattern: "World|world", Path: tmp},
			want: []string{
				"beta.go:2:func World() {}",
				"gamma.txt:1:Hello world",
			},
		},
		{
			name:   "literal match with regex metacharacters",
			params: GrepParams{Pattern: "Hello() {}", Path: tmp, Literal: true, CaseSensitive: true},
			want: []string{
				"alpha.go:2:func Hello() {}",
				"vendor/mod.go:2:func Hello() {}",
			},
		},
		{
			name:   "include filter",
			params: GrepParams{Pattern: "hello", Path: tmp, Include: "*.go"},
			want: []string{
				"alpha.go:2:func Hello() {}",
				"beta.go:3:func hello() {}",
				"subdir/nested.go:2:func HelloThere() {}",
				"vendor/mod.go:2:func Hello() {}",
			},
		},
		{
			name:   "context lines",
			params: GrepParams{Pattern: "Hello", Path: filepath.Join(tmp, "alpha.go"), ContextBefore: 1, ContextAfter: 1},
			want: []string{
				"alpha.go-1-package alpha",
				"alpha.go:2:func Hello() {}",
				"alpha.go-3-", // trailing newline yields empty third line
			},
		},
		{
			name:      "no matches",
			params:    GrepParams{Pattern: "nonexistentXYZ", Path: tmp},
			wantEmpty: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := grepFallback(ctx, tc.params, tc.params.Path, tmp, nil)
			if err != nil {
				t.Fatalf("grepFallback error: %v", err)
			}
			if tc.wantEmpty {
				if got != "" {
					t.Fatalf("expected empty, got:\n%s", got)
				}
				return
			}
			gotLines := strings.Split(strings.TrimSpace(got), "\n")
			if len(gotLines) == 1 && gotLines[0] == "" {
				gotLines = []string{}
			}
			if len(gotLines) != len(tc.want) {
				t.Fatalf("got %d results, want %d\ngot:\n%s\nwant: %v", len(gotLines), len(tc.want), got, tc.want)
			}
			for i := range tc.want {
				if gotLines[i] != tc.want[i] {
					t.Fatalf("result mismatch at %d: got %q, want %q", i, gotLines[i], tc.want[i])
				}
			}
		})
	}
}

func TestGrepFallback_ContextDedup(t *testing.T) {
	tmp := t.TempDir()
	content := "line1\nHello\nline3\nHello\nline5\n"
	createGrepTestFile(t, tmp, "test.txt", content)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	params := GrepParams{Pattern: "Hello", Path: tmp, ContextBefore: 1, ContextAfter: 1}
	got, err := grepFallback(ctx, params, tmp, tmp, nil)
	if err != nil {
		t.Fatalf("grepFallback error: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(got), "\n")
	// Expected:
	// line1 (context for first Hello)
	// Hello (match 1)
	// line3 (context after first Hello + context before second Hello - dedup)
	// Hello (match 2)
	// line5 (context after second Hello)
	// Total unique lines: 5
	if len(lines) != 5 {
		t.Fatalf("expected 5 lines, got %d:\n%s", len(lines), got)
	}
}

func TestCapGrepOutput_MatchLimit(t *testing.T) {
	var lines []string
	for i := 1; i <= 20; i++ {
		lines = append(lines, fmt.Sprintf("file.go:%d:match line", i))
	}
	output := strings.Join(lines, "\n")

	got := capGrepResult(output, 5).Content

	if !strings.Contains(got, "[showing first 5 matches") {
		t.Fatalf("expected match-limit notice, got:\n%s", got)
	}
	matchCount := strings.Count(got, "match line")
	if matchCount != 5 {
		t.Fatalf("expected 5 match lines, got %d", matchCount)
	}
}

func TestCapGrepOutput_LongLines(t *testing.T) {
	long := "file.js:1:" + strings.Repeat("x", 2000)
	got := capGrepResult(long, 100).Content

	if !strings.Contains(got, "... [truncated]") {
		t.Fatalf("expected per-line truncation marker, got:\n%s", got)
	}
	if !strings.Contains(got, "[some lines truncated to 500 chars]") {
		t.Fatalf("expected long-line notice, got:\n%s", got)
	}
	if len(strings.Split(got, "\n")[0]) > 600 {
		t.Fatalf("first line not capped: %d chars", len(strings.Split(got, "\n")[0]))
	}
}

func TestCapGrepOutput_ContextLinesNotCounted(t *testing.T) {
	// Context lines (dash separators) must not count towards the match limit.
	output := strings.Join([]string{
		"file.go-1-context before",
		"file.go:2:match one",
		"file.go-3-context after",
		"file.go:4:match two",
	}, "\n")

	got := capGrepResult(output, 2).Content

	if strings.Contains(got, "[showing first") {
		t.Fatalf("limit notice should not fire for 2 matches with limit 2, got:\n%s", got)
	}
	if !strings.Contains(got, "match two") {
		t.Fatalf("expected both matches present, got:\n%s", got)
	}
}

func TestGrepFallback_NonExistentPath(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := grepFallback(ctx, GrepParams{Pattern: "foo"}, "/nonexistent/path", "/nonexistent/path", nil)
	if err == nil {
		t.Fatal("expected error for non-existent path")
	}
}

func createGrepTestFile(t *testing.T, base, relPath, content string) {
	t.Helper()
	full := filepath.Join(base, relPath)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestGrepClampsContextLines(t *testing.T) {
	tmp := t.TempDir()
	var lines []string
	for i := 1; i <= 200; i++ {
		if i == 100 {
			lines = append(lines, "the needle")
			continue
		}
		lines = append(lines, fmt.Sprintf("filler %d", i))
	}
	createGrepTestFile(t, tmp, "f.txt", strings.Join(lines, "\n")+"\n")

	tool := NewGrepTool(tmp, nil)
	res, err := tool.Execute(context.Background(), json.RawMessage(
		`{"pattern":"needle","context_before":40,"context_after":40}`), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected tool error: %s", res.Content)
	}

	// Context is clamped to grepMaxContext either side, so a line that far
	// from the match must not appear.
	if strings.Contains(res.Content, "filler 80") || strings.Contains(res.Content, "filler 120") {
		t.Fatalf("context lines were not clamped, got:\n%s", res.Content)
	}
	if !strings.Contains(res.Content, "filler 99") || !strings.Contains(res.Content, "filler 101") {
		t.Fatalf("clamped context should still include adjacent lines, got:\n%s", res.Content)
	}
	if !strings.Contains(res.Content, "context") {
		t.Fatalf("expected a notice explaining the clamp, got:\n%s", res.Content)
	}
}

func TestGrepLeavesModestContextAlone(t *testing.T) {
	tmp := t.TempDir()
	createGrepTestFile(t, tmp, "f.txt", "a\nb\nneedle\nd\ne\n")

	tool := NewGrepTool(tmp, nil)
	res, err := tool.Execute(context.Background(), json.RawMessage(
		`{"pattern":"needle","context_before":2,"context_after":2}`), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(res.Content, "clamped") {
		t.Fatalf("a within-limit request must not be clamped, got:\n%s", res.Content)
	}
	for _, want := range []string{"a", "b", "needle", "d", "e"} {
		if !strings.Contains(res.Content, want) {
			t.Fatalf("missing %q in:\n%s", want, res.Content)
		}
	}
}

// The index served only 5% of searches across the analysed sessions
// (codesearch 11, direct 199) because it engaged only when the search path was
// exactly cwd. Any subdirectory-scoped search silently fell back to a walk.
func TestGrepUsesIndexForSubdirectorySearch(t *testing.T) {
	tmp := t.TempDir()
	createGrepTestFile(t, tmp, "sub/inside.txt", "needle\n")
	createGrepTestFile(t, tmp, "other/outside.txt", "needle\n")

	index := staticGrepIndex{files: []string{
		filepath.Join(tmp, "sub", "inside.txt"),
		filepath.Join(tmp, "other", "outside.txt"),
	}}

	tool := NewGrepTool(tmp, index)
	res, err := tool.Execute(context.Background(), json.RawMessage(
		`{"pattern":"needle","path":"sub"}`), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected tool error: %s", res.Content)
	}
	if got := res.MetricLabels["search_backend"]; got != "codesearch" {
		t.Fatalf("search_backend = %q, want codesearch", got)
	}
	if !strings.Contains(res.Content, "inside.txt") {
		t.Fatalf("expected the in-scope match, got:\n%s", res.Content)
	}
	if strings.Contains(res.Content, "outside.txt") {
		t.Fatalf("candidates outside the search path must be filtered out, got:\n%s", res.Content)
	}
}

// If the index has no candidates inside the requested subtree we must not
// conclude "no matches" from an empty candidate list - fall back to a walk.
func TestGrepFallsBackToDirectWhenNoCandidatesInSubtree(t *testing.T) {
	tmp := t.TempDir()
	createGrepTestFile(t, tmp, "sub/inside.txt", "needle\n")

	index := staticGrepIndex{files: []string{filepath.Join(tmp, "elsewhere.txt")}}

	tool := NewGrepTool(tmp, index)
	res, err := tool.Execute(context.Background(), json.RawMessage(
		`{"pattern":"needle","path":"sub"}`), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := res.MetricLabels["search_backend"]; got != "direct" {
		t.Fatalf("search_backend = %q, want direct", got)
	}
	if !strings.Contains(res.Content, "inside.txt") {
		t.Fatalf("an empty candidate set must not hide real matches, got:\n%s", res.Content)
	}
}
