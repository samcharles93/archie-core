package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const changelogFixture = `# Changelog

## [1.31.0] - 2026-09-22

### archied — Gateway

- gateway bullet
`

func writeChangelog(t *testing.T, root, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, changelogPath), []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", changelogPath, err)
	}
}

func readFile(t *testing.T, root, name string) []byte {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(root, name))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return body
}

func TestWriteThenCheckIsClean(t *testing.T) {
	root := t.TempDir()
	writeChangelog(t, root, changelogFixture)

	if err := write(root); err != nil {
		t.Fatalf("write error = %v", err)
	}
	if err := check(root); err != nil {
		t.Fatalf("check after write error = %v", err)
	}
}

func TestWriteIsIdempotent(t *testing.T) {
	root := t.TempDir()
	writeChangelog(t, root, changelogFixture)

	if err := write(root); err != nil {
		t.Fatalf("first write error = %v", err)
	}
	first := readFile(t, root, releasesJSONPath)
	if err := write(root); err != nil {
		t.Fatalf("second write error = %v", err)
	}
	if second := readFile(t, root, releasesJSONPath); string(second) != string(first) {
		t.Fatalf("second write changed %s", releasesJSONPath)
	}
}

func TestCheckReportsAStaleChangelog(t *testing.T) {
	root := t.TempDir()
	writeChangelog(t, root, changelogFixture)
	if err := write(root); err != nil {
		t.Fatalf("write error = %v", err)
	}

	writeChangelog(t, root, changelogFixture+"\n## [1.32.0] - 2026-09-23\n\n- newer bullet\n")

	err := check(root)
	if err == nil {
		t.Fatal("check accepted a committed tree that lags the changelog")
	}
	for _, want := range []string{"task news", releasesJSONPath} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error = %q, want it to mention %q", err, want)
		}
	}
}

func TestCheckIgnoresRetiredMarkdownAdapters(t *testing.T) {
	root := t.TempDir()
	writeChangelog(t, root, changelogFixture)
	if err := write(root); err != nil {
		t.Fatalf("write error = %v", err)
	}

	stale := filepath.Join(root, "docs", "news", "9.9.9.md")
	if err := os.WriteFile(stale, []byte("gone from the changelog\n"), 0o644); err != nil {
		t.Fatalf("write stale page: %v", err)
	}

	if err := check(root); err != nil {
		t.Fatalf("check considered a retired Markdown adapter: %v", err)
	}
}

func TestCheckIgnoresRetiredComponentMarkdownLayout(t *testing.T) {
	root := t.TempDir()
	writeChangelog(t, root, changelogFixture)
	if err := write(root); err != nil {
		t.Fatalf("write error = %v", err)
	}

	retired := filepath.Join(root, "docs", "news", "archied", "1.31.0.md")
	if err := os.MkdirAll(filepath.Dir(retired), 0o755); err != nil {
		t.Fatalf("create retired directory: %v", err)
	}
	if err := os.WriteFile(retired, []byte("old layout\n"), 0o644); err != nil {
		t.Fatalf("write retired page: %v", err)
	}

	if err := check(root); err != nil {
		t.Fatalf("check considered a retired component Markdown adapter: %v", err)
	}
}

func TestWriteLeavesOtherNewsFilesAlone(t *testing.T) {
	root := t.TempDir()
	writeChangelog(t, root, changelogFixture)
	if err := write(root); err != nil {
		t.Fatalf("write error = %v", err)
	}

	announcement := filepath.Join(root, "docs", "news", "announcement.md")
	if err := os.WriteFile(announcement, []byte("hand-written\n"), 0o644); err != nil {
		t.Fatalf("write announcement: %v", err)
	}
	if err := write(root); err != nil {
		t.Fatalf("second write error = %v", err)
	}
	if _, err := os.Stat(announcement); err != nil {
		t.Fatalf("write removed a hand-written news page: %v", err)
	}
	if err := check(root); err != nil {
		t.Fatalf("check rejected a hand-written news page it does not own: %v", err)
	}
}

func TestRealChangelogParses(t *testing.T) {
	releases, err := load(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("load the real changelog: %v", err)
	}
	if len(releases) == 0 {
		t.Fatal("the real changelog produced no releases")
	}
	seen := map[string]bool{}
	for _, r := range releases {
		if r.Version == "" || r.Date == "" || r.Body == "" {
			t.Fatalf("incomplete release: %+v", r)
		}
		if seen[r.Version] {
			t.Fatalf("version %s appears twice in the merged history", r.Version)
		}
		seen[r.Version] = true
	}
}
