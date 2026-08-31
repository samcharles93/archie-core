package archied

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestMemoryProviderFallsBackInsteadOfFailing guards the same startup rule as
// TestResolveForgeDegradesInsteadOfFailing: a bad path must not take the
// daemon down. A config copied from another host (a container path such as
// /var/lib/archie/work onto a laptop) has to warn and land on the default,
// not exit 1 and crash-loop under Restart=on-failure.
func TestMemoryProviderFallsBackInsteadOfFailing(t *testing.T) {
	tests := []struct {
		name string
		// workDir builds the configured work directory; the returned string
		// is passed to memoryProvider.
		workDir func(t *testing.T) string
		// wantDefault says the result must be the default location rather
		// than the configured one.
		wantDefault bool
		// wantAvailable says memory must end up actually usable.
		wantAvailable bool
	}{
		{
			name:          "usable configured directory is kept",
			workDir:       func(t *testing.T) string { return t.TempDir() },
			wantAvailable: true,
		},
		{
			name: "missing configured directory is created",
			workDir: func(t *testing.T) string {
				return filepath.Join(t.TempDir(), "does", "not", "exist")
			},
			wantAvailable: true,
		},
		{
			name: "unwritable configured directory falls back to the default",
			workDir: func(t *testing.T) string {
				ro := filepath.Join(t.TempDir(), "ro")
				if err := os.Mkdir(ro, 0o500); err != nil {
					t.Fatalf("mkdir: %v", err)
				}
				return filepath.Join(ro, "work")
			},
			wantDefault:   true,
			wantAvailable: true,
		},
		{
			name:          "empty work dir falls back to the default",
			workDir:       func(t *testing.T) string { return "" },
			wantDefault:   true,
			wantAvailable: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.wantDefault && os.Geteuid() == 0 {
				t.Skip("running as root: permission cases are not meaningful")
			}
			// Keep the default location inside the test's own tree so the
			// fallback cannot write to the developer's real ~/.local/share.
			dataHome := t.TempDir()
			t.Setenv("XDG_DATA_HOME", dataHome)
			wantDefaultDir := filepath.Join(dataHome, "archie", "work", "memory")

			workDir := tc.workDir(t)
			provider, dir := memoryProvider(workDir, slog.New(slog.DiscardHandler))

			if provider == nil {
				t.Fatal("memoryProvider returned nil: startup would exit 1 over a path problem")
			}
			if got := provider.IsAvailable(); got != tc.wantAvailable {
				t.Fatalf("IsAvailable() = %v, want %v (dir %q, err %v)",
					got, tc.wantAvailable, dir, provider.Err())
			}

			if tc.wantDefault {
				if dir != wantDefaultDir {
					t.Fatalf("dir = %q, want the default %q", dir, wantDefaultDir)
				}
			} else {
				if want := filepath.Join(workDir, "memory"); dir != want {
					t.Fatalf("dir = %q, want the configured %q", dir, want)
				}
			}

			if tc.wantAvailable {
				if info, err := os.Stat(dir); err != nil || !info.IsDir() {
					t.Fatalf("reported available but %q is not a usable directory (err %v)", dir, err)
				}
			}
		})
	}
}

// The fallback is only worth anything if the operator can tell it happened
// and why -- a silent switch to a different directory looks like memory loss.
func TestMemoryProviderExplainsTheFallback(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: permission cases are not meaningful")
	}
	t.Setenv("XDG_DATA_HOME", t.TempDir())

	ro := filepath.Join(t.TempDir(), "ro")
	if err := os.Mkdir(ro, 0o500); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	configured := filepath.Join(ro, "work")

	var sb strings.Builder
	log := slog.New(slog.NewTextHandler(&sb, &slog.HandlerOptions{Level: slog.LevelWarn}))

	memoryProvider(configured, log)

	out := sb.String()
	if !strings.Contains(out, configured) {
		t.Fatalf("log does not name the rejected directory %q: %s", configured, out)
	}
	if !strings.Contains(out, "default") {
		t.Fatalf("log does not say a default was used: %s", out)
	}
}
