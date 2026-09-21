package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const archiedFixture = `# archied changelog

## [1.31.0] - 2026-09-22

- gateway bullet
`

const archieFixture = `# archie-agent changelog

## [1.31.0] - 2026-09-22

- runtime bullet
`

func writeChangelogs(t *testing.T, root, archied, archie string) {
	t.Helper()
	for name, body := range map[string]string{
		"CHANGELOG.archied.md": archied,
		"CHANGELOG.archie.md":  archie,
	} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
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
	writeChangelogs(t, root, archiedFixture, archieFixture)

	if err := write(root); err != nil {
		t.Fatalf("write error = %v", err)
	}
	if err := check(root); err != nil {
		t.Fatalf("check after write error = %v", err)
	}
}

func TestWriteIsIdempotent(t *testing.T) {
	root := t.TempDir()
	writeChangelogs(t, root, archiedFixture, archieFixture)

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
	writeChangelogs(t, root, archiedFixture, archieFixture)
	if err := write(root); err != nil {
		t.Fatalf("write error = %v", err)
	}

	writeChangelogs(t, root, archiedFixture+"\n## [1.32.0] - 2026-09-23\n\n- newer bullet\n", archieFixture)

	err := check(root)
	if err == nil {
		t.Fatal("check accepted a committed tree that lags the changelog")
	}
	for _, want := range []string{"task news", "docs/news/archied/1.32.0.md"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error = %q, want it to mention %q", err, want)
		}
	}
}

func TestCheckReportsFilesItDoesNotOwn(t *testing.T) {
	root := t.TempDir()
	writeChangelogs(t, root, archiedFixture, archieFixture)
	if err := write(root); err != nil {
		t.Fatalf("write error = %v", err)
	}

	handwritten := filepath.Join(root, "docs", "news", "archied", "handwritten.md")
	if err := os.WriteFile(handwritten, []byte("mine\n"), 0o644); err != nil {
		t.Fatalf("write handwritten page: %v", err)
	}

	err := check(root)
	if err == nil {
		t.Fatal("check ignored a file in a generated directory")
	}
	if !strings.Contains(err.Error(), "handwritten.md") || !strings.Contains(err.Error(), "not generated") {
		t.Fatalf("error = %q, want it to name the unmanaged page", err)
	}
}

func TestWriteLeavesOtherNewsFilesAlone(t *testing.T) {
	root := t.TempDir()
	writeChangelogs(t, root, archiedFixture, archieFixture)
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

func TestRealChangelogsParse(t *testing.T) {
	releases, err := load(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("load real changelogs: %v", err)
	}
	seen := map[string]int{}
	for _, r := range releases {
		seen[r.Component]++
		if r.Version == "" || r.Date == "" || r.Body == "" {
			t.Fatalf("incomplete release: %+v", r)
		}
	}
	for _, c := range components {
		if seen[c.key] == 0 {
			t.Fatalf("%s produced no releases", c.changelog)
		}
	}
}
