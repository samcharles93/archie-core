package builtin

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

func TestFindFallback(t *testing.T) {
	// Create a temporary directory tree.
	tmp := t.TempDir()
	createTestFile(t, tmp, "foo.go", "package main")
	createTestFile(t, tmp, "bar.go", "package bar")
	createTestFile(t, tmp, "README.md", "# readme")
	createTestFile(t, tmp, "subdir/baz.go", "package baz")
	createTestFile(t, tmp, "subdir/nested/deep.txt", "deep")
	createTestFile(t, tmp, "vendor/mod.go", "package mod")
	os.MkdirAll(filepath.Join(tmp, "hiddendir"), 0o755)
	createTestFile(t, tmp, "hiddendir/visible.txt", "text")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cases := []struct {
		name      string
		params    FindParams
		want      []string
		wantEmpty bool
	}{
		{
			name:   "glob pattern *.go (matches all depths)",
			params: FindParams{Path: tmp, Pattern: "*.go", Type: "file"},
			want:   []string{"bar.go", "foo.go", "subdir/baz.go", "vendor/mod.go"},
		},
		{
			name:   "match directories (nested shown)",
			params: FindParams{Path: tmp, Type: "directory"},
			want:   []string{"hiddendir", "subdir", "subdir/nested", "vendor"},
		},
		{
			name:   "max depth 1",
			params: FindParams{Path: tmp, Type: "file", MaxDepth: 1},
			want:   []string{"bar.go", "foo.go", "README.md"},
		},
		{
			name:   "max depth 0 unlimited",
			params: FindParams{Path: tmp, Type: "file", MaxDepth: 0},
			want: []string{
				"bar.go", "foo.go", "README.md",
				"hiddendir/visible.txt", "subdir/baz.go",
				"subdir/nested/deep.txt", "vendor/mod.go",
			},
		},
		{
			name:   "exclude vendor",
			params: FindParams{Path: tmp, Type: "file", Exclude: "vendor"},
			want: []string{
				"bar.go", "foo.go", "README.md",
				"hiddendir/visible.txt", "subdir/baz.go",
				"subdir/nested/deep.txt",
			},
		},
		{
			name:   "exclude by glob",
			params: FindParams{Path: tmp, Type: "file", Exclude: "*.md"},
			want: []string{
				"bar.go", "foo.go",
				"hiddendir/visible.txt", "subdir/baz.go",
				"subdir/nested/deep.txt", "vendor/mod.go",
			},
		},
		{
			name:      "no matches",
			params:    FindParams{Path: tmp, Pattern: "nonexistent*", Type: "file"},
			wantEmpty: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res, err := runFindGoFallback(ctx, tmp, tmp, tc.params)
			if err != nil {
				t.Fatalf("runFindGoFallback returned error: %v", err)
			}
			if tc.wantEmpty {
				if res.Content != "no matches found" {
					t.Fatalf("expected empty, got:\n%s", res.Content)
				}
				return
			}
			gotLines := strings.Split(strings.TrimSpace(res.Content), "\n")
			if len(gotLines) == 1 && gotLines[0] == "" {
				gotLines = []string{}
			}
			sort.Strings(gotLines)
			sort.Strings(tc.want)
			if len(gotLines) != len(tc.want) {
				t.Fatalf("got %d results, want %d\ngot: %v\nwant: %v", len(gotLines), len(tc.want), gotLines, tc.want)
			}
			for i := range tc.want {
				if gotLines[i] != tc.want[i] {
					t.Fatalf("result mismatch at %d: got %q, want %q", i, gotLines[i], tc.want[i])
				}
			}
		})
	}
}

func TestFindFallback_NonExistentPath(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	res, _ := runFindGoFallback(ctx, "/nonexistent/path", "/nonexistent/path", FindParams{})
	if !res.IsError {
		t.Fatal("expected error for non-existent path")
	}
}

func TestFindFallback_NotADirectory(t *testing.T) {
	tmp := t.TempDir()
	f := filepath.Join(tmp, "file.txt")
	if err := os.WriteFile(f, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	res, _ := runFindGoFallback(ctx, tmp, f, FindParams{})
	if !res.IsError {
		t.Fatal("expected error when path is a file")
	}
}

func createTestFile(t *testing.T, base, relPath, content string) {
	t.Helper()
	full := filepath.Join(base, relPath)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
