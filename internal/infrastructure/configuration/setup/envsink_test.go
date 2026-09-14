package setup

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEnvFileSink_MatchSetEnvKeyFormat(t *testing.T) {
	path := filepath.Join(t.TempDir(), "env")
	s := NewEnvFileSink(path)
	if err := s.Put("env", "ARCHIE_GITHUB_TOKEN", "abc"); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if err := s.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read env file: %v", err)
	}
	if got, want := string(b), "ARCHIE_GITHUB_TOKEN='abc'\n"; got != want {
		t.Errorf("env file = %q, want %q", got, want)
	}
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("env file mode = %v (%v), want 0600", info.Mode().Perm(), err)
	}
}

func TestEnvFileSink_ReplacesExistingAndPreservesUnrelated(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "env")
	if err := os.WriteFile(path, []byte("KEEP='keep'\nARCHIE_GITHUB_TOKEN='old'\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	s := NewEnvFileSink(path)
	if err := s.Put("env", "ARCHIE_GITHUB_TOKEN", "new"); err != nil {
		t.Fatal(err)
	}
	if err := s.Put("env", "ARCHIE_GITEA_TOKEN", "gitea"); err != nil {
		t.Fatal(err)
	}
	if err := s.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}

	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// Unrelated line keeps its position; replaced keys move to the end (the
	// same behaviour as set_env_key's grep -v + append), new keys sorted.
	want := "KEEP='keep'\nARCHIE_GITEA_TOKEN='gitea'\nARCHIE_GITHUB_TOKEN='new'\n"
	if got := string(b); got != want {
		t.Errorf("env file = %q, want %q", got, want)
	}
}

func TestEnvFileSink_EscapesSingleQuotes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "env")
	s := NewEnvFileSink(path)
	if err := s.Put("env", "K", "a'b"); err != nil {
		t.Fatal(err)
	}
	if err := s.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(b), "K='a'\\''b'\n"; got != want {
		t.Errorf("env file = %q, want %q", got, want)
	}
}

func TestEnvFileSink_RejectsNonEnvEngine(t *testing.T) {
	s := NewEnvFileSink(filepath.Join(t.TempDir(), "env"))
	if err := s.Put("bws", "K", "v"); err == nil {
		t.Error("Put(bws) = nil error, want a failure: the env file has no representation for non-env engines")
	}
}
