package builtin_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	provider "github.com/samcharles93/archie-core/internal/tools/provider/builtin"
)

func startedProvider(t *testing.T, workspace string) *provider.Provider {
	t.Helper()
	p := provider.New(workspace)
	if err := p.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = p.Stop(context.Background()) })
	return p
}

func TestDiscoverExposesWorkspaceTools(t *testing.T) {
	t.Parallel()

	entries, err := startedProvider(t, t.TempDir()).Discover(context.Background())
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}

	got := make(map[string]bool, len(entries))
	for _, e := range entries {
		got[e.Name] = true
		if e.Handler == nil {
			t.Errorf("tool %q has no handler, so the model could call something that does nothing", e.Name)
		}
		if e.Description == "" {
			t.Errorf("tool %q has no description", e.Name)
		}
	}
	for _, want := range []string{"read", "write", "edit", "shell", "grep", "find"} {
		if !got[want] {
			t.Errorf("tool %q missing from discovery; got %v", want, got)
		}
	}
}

// TestHandlerRoundTripsThroughTauExecutor covers the seam this package
// exists for: archie hands the handler decoded arguments, tau's executor
// wants raw JSON, and the result comes back as content.
func TestHandlerRoundTripsThroughTauExecutor(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "hello.txt"), []byte("g'day\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	entries, err := startedProvider(t, dir).Discover(context.Background())
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	var read func(context.Context, map[string]any) (any, error)
	for _, e := range entries {
		if e.Name == "read" {
			read = e.Handler
		}
	}
	if read == nil {
		t.Fatal("no read tool discovered")
	}

	out, err := read(context.Background(), map[string]any{"path": "hello.txt"})
	if err != nil {
		t.Fatalf("read handler: %v", err)
	}
	if text, ok := out.(string); !ok || !strings.Contains(text, "g'day") {
		t.Errorf("read returned %#v, want content containing the file text", out)
	}
}

// TestHandlerSurfacesToolFailureAsError guards the second half of the seam:
// tau reports a tool-level failure in Result.IsError rather than as an
// error, and swallowing that would show the model a successful call.
func TestHandlerSurfacesToolFailureAsError(t *testing.T) {
	t.Parallel()

	entries, err := startedProvider(t, t.TempDir()).Discover(context.Background())
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	for _, e := range entries {
		if e.Name != "read" {
			continue
		}
		if _, err := e.Handler(context.Background(), map[string]any{"path": "absent.txt"}); err == nil {
			t.Error("reading a missing file reported success")
		}
		return
	}
	t.Fatal("no read tool discovered")
}

// TestUnconfiguredWorkspaceIsRejected keeps the tools from silently rooting
// at the daemon's own working directory, which in the container is /.
func TestUnconfiguredWorkspaceIsRejected(t *testing.T) {
	t.Parallel()

	if err := provider.New("").Start(context.Background()); err == nil {
		t.Error("a provider with no workspace started successfully")
	}
}
