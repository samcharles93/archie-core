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

// TestShellHandlerEnforcesHardlineRules checks the rules are actually
// wired into the executing handler. The rules are tested in
// internal/tools/command; what matters here is that a call cannot reach
// tau's executor without passing through them.
func TestShellHandlerEnforcesHardlineRules(t *testing.T) {
	t.Parallel()

	shell := shellHandler(t, t.TempDir())

	if _, err := shell(context.Background(), map[string]any{"command": "rm -rf /"}); err == nil {
		t.Error("the shell tool accepted a recursive delete of /")
	} else if !strings.Contains(err.Error(), "refused") {
		t.Errorf("error = %v, want a refusal naming the rule", err)
	}
}

// TestShellHandlerAllowsOrdinaryCommands guards the other direction: the
// screen must not stand between the model and normal work.
func TestShellHandlerAllowsOrdinaryCommands(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	out, err := shellHandler(t, dir)(context.Background(), map[string]any{
		"command": `echo "rm -rf / is only mentioned here"`,
	})
	if err != nil {
		t.Fatalf("an ordinary echo was refused: %v", err)
	}
	if text, ok := out.(string); !ok || !strings.Contains(text, "only mentioned here") {
		t.Errorf("shell returned %#v, want the echoed text", out)
	}
}

func shellHandler(t *testing.T, workspace string) func(context.Context, map[string]any) (any, error) {
	t.Helper()

	entries, err := startedProvider(t, workspace).Discover(context.Background())
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	for _, e := range entries {
		if e.Name == "shell" {
			return e.Handler
		}
	}
	t.Fatal("no shell tool discovered")
	return nil
}

// TestUnconfiguredWorkspaceIsRejected keeps the tools from silently rooting
// at the daemon's own working directory, which in the container is /.
func TestUnconfiguredWorkspaceIsRejected(t *testing.T) {
	t.Parallel()

	if err := provider.New("").Start(context.Background()); err == nil {
		t.Error("a provider with no workspace started successfully")
	}
}
