package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestNormalize(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"bare package", "internal/config", "internal/config"},
		{"trailing slash", "internal/policy/", "internal/policy"},
		{"file", "internal/daemon/daemon.go", "internal/daemon/daemon.go"},
		{"go wildcard", "internal/infrastructure/...", "internal/infrastructure"},
		{"package and symbol", "internal/config.Config", "internal/config.Config"},
		{"sentence punctuation", "internal/store,", "internal/store"},
		{"line number", "internal/store/store.go:120", "internal/store/store.go"},
		{"deep wildcard", "internal/domain/...", "internal/domain"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := normalize(tc.in); got != tc.want {
				t.Fatalf("normalize(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestCandidates(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want []string
	}{
		{"package with symbol drops the symbol", "internal/config.Config", []string{"internal/config.Config", "internal/config"}},
		{"file keeps only itself", "internal/daemon/daemon.go", []string{"internal/daemon/daemon.go"}},
		{"bare package", "internal/webui", []string{"internal/webui"}},
		{"dotted directory is not a symbol", "internal/domain/eda/module", []string{"internal/domain/eda/module"}},
		{"test cited with its package drops the test name", "internal/app/archieui/TestFoo", []string{"internal/app/archieui/TestFoo", "internal/app/archieui"}},
		{"lowercase final segment is a package, not a symbol", "internal/domain/curator", []string{"internal/domain/curator"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := candidates(tc.in)
			if len(got) != len(tc.want) {
				t.Fatalf("candidates(%q) = %v, want %v", tc.in, got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("candidates(%q) = %v, want %v", tc.in, got, tc.want)
				}
			}
		})
	}
}

// treeFor builds the tracked-path set a citation resolves against, without
// touching the filesystem: resolution is defined by what git tracks, so a test
// asserts the definition rather than one machine's leftover directories.
func treeFor(files ...string) *trackedTree {
	tree := &trackedTree{files: map[string]bool{}, dirs: map[string]bool{}}
	for _, file := range files {
		tree.files[file] = true
		for dir := filepath.Dir(file); dir != "." && dir != "/" && dir != ""; dir = filepath.Dir(dir) {
			tree.dirs[dir] = true
		}
	}
	return tree
}

func TestResolves(t *testing.T) {
	tree := treeFor(
		"internal/config/config.go",
		"internal/daemon/daemon.go",
		"internal/webui/api_logs.go",
		"internal/app/archieui/run.go",
		"internal/domain/workflow/workflow.go",
	)
	tests := []struct {
		name string
		in   string
		want bool
	}{
		{"package directory", "internal/webui", true},
		{"nested package directory", "internal/domain/workflow", true},
		{"file", "internal/daemon/daemon.go", true},
		{"package and symbol resolves via the package", "internal/config.Config", true},
		{"package cited without the .go file present", "internal/daemon", true},
		{"moved package", "internal/memory", false},
		{"deleted file", "internal/app/archied/config_update.go", false},
		{"never built", "internal/policy", false},
		{"whole package tree", "internal", true},
		{"file-shape template resolves against a sibling", "internal/webui/api_", true},
		{"test cited with its package resolves via the package", "internal/app/archieui/TestComposeUIServerHoldsNoDaemonState", true},
		{"file-shape template with no sibling", "internal/webui/nothing_", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tree.resolves(tc.in); got != tc.want {
				t.Fatalf("resolves(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

// TestResolvesIgnoresTheFilesystem is the regression for the bug this gate
// shipped with. Resolution was a filesystem stat, so a directory left behind by
// a checkout that removed its files -- internal/gate, internal/gate/gateeval --
// resolved on the machine that had the leftovers and resolved nothing in CI.
// The gate passed locally and failed in CI on the same commit, and a baseline
// generated that way is worthless in either direction.
func TestResolvesIgnoresTheFilesystem(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "internal", "gate", "gateeval"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "internal", "config"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "internal", "config", "config.go"), []byte("package config\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	tree := treeFor("internal/config/config.go")
	if tree.resolves("internal/gate") {
		t.Error("an empty directory on disk resolved; resolution must come from the tracked tree")
	}
	if tree.resolves("internal/gate/gateeval") {
		t.Error("an empty nested directory on disk resolved; resolution must come from the tracked tree")
	}
	if !tree.resolves("internal/config") {
		t.Error("a tracked package did not resolve")
	}
}

// TestNewTrackedTreeReadsGit covers the git half: the tree is what the
// repository holds, so a file present on disk but never added is invisible, and
// an empty directory is invisible in both worlds.
func TestNewTrackedTreeReadsGit(t *testing.T) {
	root := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.CommandContext(t.Context(), "git", args...)
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-q")
	git("config", "user.email", "test@example.test")
	git("config", "user.name", "test")
	if err := os.MkdirAll(filepath.Join(root, "internal", "tracked"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "internal", "tracked", "x.go"), []byte("package tracked\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	git("add", "internal/tracked/x.go")
	// Neither of these is in the repository: one is ignored-by-absence, the
	// other is what a checkout that removed a package leaves behind.
	if err := os.WriteFile(filepath.Join(root, "internal", "untracked.go"), []byte("package internal\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "internal", "gate", "gateeval"), 0o750); err != nil {
		t.Fatal(err)
	}

	tree, err := newTrackedTree(t.Context(), root)
	if err != nil {
		t.Fatalf("newTrackedTree: %v", err)
	}
	if !tree.resolves("internal/tracked") {
		t.Error("a tracked package did not resolve")
	}
	if tree.resolves("internal/untracked.go") {
		t.Error("an untracked file resolved; the tree is what the repository holds, not what is on disk")
	}
	if tree.resolves("internal/gate") {
		t.Error("an empty directory resolved; it is not in the repository")
	}
}

// TestAnalyseSeparatesNewDriftFromKnownDebt pins the gate's core distinction: a
// citation already broken before this change must not fail the build, while one
// that dangles now must.
func TestAnalyseSeparatesNewDriftFromKnownDebt(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "docs", "architecture"), 0o750); err != nil {
		t.Fatal(err)
	}
	body := "See `internal/webui` and `internal/memory`.\nAlso `internal/nats` and `internal/config`.\n"
	if err := os.WriteFile(filepath.Join(root, "docs", "architecture", "page.md"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	tree := treeFor("internal/config/config.go", "internal/webui/server.go")
	got, err := analyse(root, "docs", tree, map[string]bool{"internal/memory": true})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.newDrift) != 1 || got.newDrift[0].Path != "internal/nats" {
		t.Fatalf("newDrift = %v, want exactly internal/nats", got.newDrift)
	}
	if got.newDrift[0].Line != 2 {
		t.Fatalf("newDrift line = %d, want 2", got.newDrift[0].Line)
	}
	if got.known != 1 {
		t.Fatalf("known = %d, want 1 (internal/memory is allowlisted)", got.known)
	}
	if got.scanned != 2 {
		t.Fatalf("scanned = %d, want 2 (internal/webui, internal/config)", got.scanned)
	}
	if len(got.stale) != 0 {
		t.Fatalf("stale = %v, want none", got.stale)
	}
}

// TestAnalyseFlagsAnAllowlistEntryThatResolvesAgain keeps the burn-down list
// honest: once the code comes back, the exemption must go.
func TestAnalyseFlagsAnAllowlistEntryThatResolvesAgain(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "docs"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "docs", "p.md"), []byte("`internal/policy`\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := analyse(root, "docs", treeFor("internal/policy/policy.go"), map[string]bool{"internal/policy": true})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.stale) != 1 || got.stale[0] != "internal/policy" {
		t.Fatalf("stale = %v, want [internal/policy]", got.stale)
	}
	if len(got.newDrift) != 0 {
		t.Fatalf("newDrift = %v, want none", got.newDrift)
	}
}

// TestScanSkipsGeneratedAndNonMarkdown guards the double-count: the generated
// dev-docs artifact embeds every page body, so scanning it would inflate both
// the findings and the baseline.
func TestScanSkipsGeneratedAndNonMarkdown(t *testing.T) {
	root := t.TempDir()
	for _, dir := range []string{"docs/architecture", "docs/data/generated"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o750); err != nil {
			t.Fatal(err)
		}
	}
	write := func(rel, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, rel), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("docs/architecture/page.md", "`internal/gone`\n")
	write("docs/data/generated/dev-docs.json", "{\"body\":\"internal/also-gone\"}\n")
	write("docs/notes.txt", "internal/not-markdown\n")

	got, err := scan(root, "docs")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Path != "internal/gone" {
		t.Fatalf("scan = %v, want exactly [internal/gone]", got)
	}
}

func TestReadBaseline(t *testing.T) {
	root := t.TempDir()
	p := filepath.Join(root, "baseline.txt")
	body := "# a comment\n\ninternal/store\ninternal/memory  \n"
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := readBaseline(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || !got["internal/store"] || !got["internal/memory"] {
		t.Fatalf("readBaseline = %v, want internal/store and internal/memory", got)
	}
}

// TestReadBaselineMissingIsEmpty lets the tool run before a baseline exists.
func TestReadBaselineMissingIsEmpty(t *testing.T) {
	got, err := readBaseline(filepath.Join(t.TempDir(), "absent.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("readBaseline = %v, want empty", got)
	}
}
