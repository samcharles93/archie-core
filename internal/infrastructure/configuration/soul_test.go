package configuration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/samcharles93/archie-core/internal/domain/agent"
)

func TestConfigDirResolvesFilesAndDirectories(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	configFile := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(configFile, []byte(""), 0o600); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name string
		path string
		want string
	}{
		{name: "a directory is used as given", path: dir, want: dir},
		{name: "a file uses its directory", path: configFile, want: dir},
		{name: "a missing path falls back to its directory", path: filepath.Join(dir, "missing", "config.toml"), want: filepath.Join(dir, "missing")},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := ConfigDir(test.path); got != test.want {
				t.Fatalf("ConfigDir(%q) = %q, want %q", test.path, got, test.want)
			}
		})
	}
}

func TestSoulPath(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	if got, want := SoulPath(dir), filepath.Join(dir, SoulFilename); got != want {
		t.Fatalf("SoulPath(dir) = %q, want %q", got, want)
	}
	configFile := filepath.Join(dir, "config.toml")
	if got, want := SoulPath(configFile), filepath.Join(dir, SoulFilename); got != want {
		t.Fatalf("SoulPath(file) = %q, want %q", got, want)
	}
}

func TestSeedSoulCreatesStarterWhenAbsent(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	result, err := SeedSoul(dir, agent.ShippedSoul())
	if err != nil {
		t.Fatalf("SeedSoul() error = %v", err)
	}
	if result.Action != SoulCreated {
		t.Fatalf("SeedSoul() action = %q, want %q", result.Action, SoulCreated)
	}
	if want := filepath.Join(dir, SoulFilename); result.Path != want {
		t.Fatalf("SeedSoul() path = %q, want %q", result.Path, want)
	}
	got, err := os.ReadFile(result.Path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != agent.ShippedSoul().Default {
		t.Fatalf("seeded SOUL = %q, want the shipped default", got)
	}
	info, err := os.Stat(result.Path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("seeded SOUL mode = %v, want 0600", info.Mode().Perm())
	}
}

func TestSeedSoulNeverClobbersAUserEdit(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	existing := "# My agent\n\nCall me Ada.\n"
	writeSoulTestFile(t, dir, existing)

	result, err := SeedSoul(dir, agent.ShippedSoul())
	if err != nil {
		t.Fatalf("SeedSoul() error = %v", err)
	}
	if result.Action != SoulPreserved {
		t.Fatalf("SeedSoul() action = %q, want %q", result.Action, SoulPreserved)
	}
	if got := readSoulTestFile(t, dir); got != existing {
		t.Fatalf("user edit was changed:\n got %q\nwant %q", got, existing)
	}
}

func TestSeedSoulPreservesAnEmptyFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeSoulTestFile(t, dir, "")

	result, err := SeedSoul(dir, agent.ShippedSoul())
	if err != nil {
		t.Fatalf("SeedSoul() error = %v", err)
	}
	if result.Action != SoulPreserved {
		t.Fatalf("SeedSoul() action = %q, want %q (an empty file is an existing file)", result.Action, SoulPreserved)
	}
	if got := readSoulTestFile(t, dir); got != "" {
		t.Fatalf("empty SOUL was changed to %q", got)
	}
}

func TestSeedSoulLeavesACurrentDocumentUntouched(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	// A CRLF checkout of the current document is still current: normalising
	// line endings must not make it look like a user edit, and it must not
	// provoke a rewrite on every boot.
	existing := strings.ReplaceAll(agent.ShippedSoul().Default, "\n", "\r\n")
	writeSoulTestFile(t, dir, existing)

	result, err := SeedSoul(dir, agent.ShippedSoul())
	if err != nil {
		t.Fatalf("SeedSoul() error = %v", err)
	}
	if result.Action != SoulCurrent {
		t.Fatalf("SeedSoul() action = %q, want %q", result.Action, SoulCurrent)
	}
	if got := readSoulTestFile(t, dir); got != existing {
		t.Fatalf("current SOUL was rewritten:\n got %q\nwant %q", got, existing)
	}
}

func TestSeedSoulUpgradesASupersededTemplate(t *testing.T) {
	t.Parallel()

	const legacy = "# Archie\n\nOld starter identity.\n"
	const current = "# Archie\n\nNew starter identity.\n"
	doc := agent.SoulDocument{Default: current, Legacy: []string{legacy}}

	dir := t.TempDir()
	writeSoulTestFile(t, dir, strings.ReplaceAll(legacy, "\n", "\r\n"))

	result, err := SeedSoul(dir, doc)
	if err != nil {
		t.Fatalf("SeedSoul() error = %v", err)
	}
	if result.Action != SoulUpgraded {
		t.Fatalf("SeedSoul() action = %q, want %q", result.Action, SoulUpgraded)
	}
	if got := readSoulTestFile(t, dir); got != current {
		t.Fatalf("upgraded SOUL = %q, want %q", got, current)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		names := make([]string, 0, len(entries))
		for _, entry := range entries {
			names = append(names, entry.Name())
		}
		t.Fatalf("upgrade left files behind: %v", names)
	}
}

func TestSeedSoulPreservesASymlink(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	target := filepath.Join(dir, "some-other-file.md")
	userContent := "# User-managed\n"
	if err := os.WriteFile(target, []byte(userContent), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, SoulFilename)
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}

	result, err := SeedSoul(dir, agent.ShippedSoul())
	if err != nil {
		t.Fatalf("SeedSoul() error = %v", err)
	}
	if result.Action != SoulPreserved {
		t.Fatalf("SeedSoul() action = %q, want %q", result.Action, SoulPreserved)
	}
	info, err := os.Lstat(link)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatal("SeedSoul replaced a symlink with a regular file")
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != userContent {
		t.Fatalf("symlink target was changed to %q", got)
	}
}

func TestSeedSoulTreatsAnOversizedFileAsAUserEdit(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	oversized := agent.ShippedSoul().Default + strings.Repeat("padding line\n", maxSoulReadBytes/8)
	writeSoulTestFile(t, dir, oversized)

	result, err := SeedSoul(dir, agent.ShippedSoul())
	if err != nil {
		t.Fatalf("SeedSoul() error = %v", err)
	}
	if result.Action != SoulPreserved {
		t.Fatalf("SeedSoul() action = %q, want %q", result.Action, SoulPreserved)
	}
	if got := readSoulTestFile(t, dir); got != oversized {
		t.Fatal("oversized SOUL was rewritten")
	}
}

func TestSeedSoulSeedsBesideASelectedConfigFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	configFile := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(configFile, []byte(""), 0o600); err != nil {
		t.Fatal(err)
	}

	result, err := SeedSoul(configFile, agent.ShippedSoul())
	if err != nil {
		t.Fatalf("SeedSoul() error = %v", err)
	}
	if result.Action != SoulCreated {
		t.Fatalf("SeedSoul() action = %q, want %q", result.Action, SoulCreated)
	}
	if want := filepath.Join(dir, SoulFilename); result.Path != want {
		t.Fatalf("SeedSoul() path = %q, want %q", result.Path, want)
	}
}

func writeSoulTestFile(t *testing.T, dir, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, SoulFilename), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func readSoulTestFile(t *testing.T, dir string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, SoulFilename))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
