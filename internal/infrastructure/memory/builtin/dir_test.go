package builtin

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A missing memory directory is indistinguishable from an empty memory: both
// stores treat ENOENT as "no content yet". That makes a misconfigured Dir
// invisible until the agent's first memory write fails mid-conversation.
//
// New settles the question at construction, but an unusable directory is not
// grounds for refusing to start -- the daemon falls back to the default
// location and carries on. So New reports the problem through IsAvailable and
// Err rather than by failing.
func TestNewEstablishesDir(t *testing.T) {
	tests := []struct {
		name      string
		dir       func(t *testing.T) string
		wantUsabl bool
	}{
		{
			name:      "existing directory",
			dir:       func(t *testing.T) string { return t.TempDir() },
			wantUsabl: true,
		},
		{
			name: "missing directory is created",
			dir: func(t *testing.T) string {
				return filepath.Join(t.TempDir(), "nested", "memory")
			},
			wantUsabl: true,
		},
		{
			name: "unwritable parent is reported, not fatal",
			dir: func(t *testing.T) string {
				parent := filepath.Join(t.TempDir(), "ro")
				if err := os.Mkdir(parent, 0o500); err != nil {
					t.Fatalf("mkdir: %v", err)
				}
				return filepath.Join(parent, "memory")
			},
		},
		{
			name: "path occupied by a file is reported, not fatal",
			dir: func(t *testing.T) string {
				p := filepath.Join(t.TempDir(), "memory")
				if err := os.WriteFile(p, []byte("not a dir"), 0o600); err != nil {
					t.Fatalf("write: %v", err)
				}
				return p
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Running as root defeats permission-based cases.
			if !tc.wantUsabl && os.Geteuid() == 0 {
				t.Skip("running as root: permission cases are not meaningful")
			}
			dir := tc.dir(t)

			p, err := New(Config{Dir: dir})
			if err != nil {
				t.Fatalf("New(%q) = %v, want a usable provider: an unusable "+
					"directory must not stop the daemon starting", dir, err)
			}
			if p == nil {
				t.Fatal("New returned a nil provider without an error")
			}

			if got := p.IsAvailable(); got != tc.wantUsabl {
				t.Fatalf("IsAvailable() = %v, want %v", got, tc.wantUsabl)
			}
			if tc.wantUsabl {
				if p.Err() != nil {
					t.Fatalf("Err() = %v, want nil", p.Err())
				}
				info, statErr := os.Stat(dir)
				if statErr != nil {
					t.Fatalf("provider reports available but %q does not exist: %v", dir, statErr)
				}
				if !info.IsDir() {
					t.Fatalf("%q is not a directory", dir)
				}
				return
			}

			// An unavailable provider must say why: the operator's only clue
			// is what gets logged, and "memory unavailable" alone is not
			// actionable.
			if p.Err() == nil {
				t.Fatal("IsAvailable() is false but Err() is nil, leaving the operator nothing to act on")
			}
			if !strings.Contains(p.Err().Error(), dir) {
				t.Fatalf("Err() = %q, want it to name the offending directory %q", p.Err(), dir)
			}
		})
	}
}

// A provider on an unusable directory must still answer every read, so a
// degraded memory reads as empty rather than panicking mid-conversation.
func TestUnusableDirStillReadable(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: permission cases are not meaningful")
	}
	parent := filepath.Join(t.TempDir(), "ro")
	if err := os.Mkdir(parent, 0o500); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	p, err := New(Config{Dir: filepath.Join(parent, "memory")})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := p.Initialize("session-1"); err != nil {
		t.Fatalf("Initialize on an unusable dir: %v", err)
	}
	if block := p.SystemPromptBlock(); block != "" {
		t.Fatalf("SystemPromptBlock() = %q, want empty", block)
	}
	if len(p.GetToolSchemas()) == 0 {
		t.Fatal("GetToolSchemas() is empty: the tool must still be offered")
	}
}
