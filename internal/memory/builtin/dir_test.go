package builtin

import (
	"os"
	"path/filepath"
	"testing"
)

// A missing memory directory is indistinguishable from an empty memory: both
// stores treat ENOENT as "no content yet". That makes a misconfigured Dir
// invisible until the agent's first memory write fails mid-conversation, long
// after startup reported success. New must settle the question up front.
func TestNewEstablishesDir(t *testing.T) {
	tests := []struct {
		name    string
		dir     func(t *testing.T) string
		wantErr bool
	}{
		{
			name: "existing directory",
			dir:  func(t *testing.T) string { return t.TempDir() },
		},
		{
			name: "missing directory is created",
			dir: func(t *testing.T) string {
				return filepath.Join(t.TempDir(), "nested", "memory")
			},
		},
		{
			name: "unwritable parent is reported",
			dir: func(t *testing.T) string {
				parent := filepath.Join(t.TempDir(), "ro")
				if err := os.Mkdir(parent, 0o500); err != nil {
					t.Fatalf("mkdir: %v", err)
				}
				return filepath.Join(parent, "memory")
			},
			wantErr: true,
		},
		{
			name: "path occupied by a file is reported",
			dir: func(t *testing.T) string {
				p := filepath.Join(t.TempDir(), "memory")
				if err := os.WriteFile(p, []byte("not a dir"), 0o600); err != nil {
					t.Fatalf("write: %v", err)
				}
				return p
			},
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Running as root defeats permission-based cases.
			if tc.wantErr && os.Geteuid() == 0 {
				t.Skip("running as root: permission cases are not meaningful")
			}
			dir := tc.dir(t)

			_, err := New(Config{Dir: dir})
			if tc.wantErr {
				if err == nil {
					t.Fatalf("New(%q) = nil error, want a failure the operator can act on", dir)
				}
				return
			}
			if err != nil {
				t.Fatalf("New(%q): %v", dir, err)
			}

			info, statErr := os.Stat(dir)
			if statErr != nil {
				t.Fatalf("New returned success but %q does not exist: %v", dir, statErr)
			}
			if !info.IsDir() {
				t.Fatalf("%q is not a directory", dir)
			}
		})
	}
}
