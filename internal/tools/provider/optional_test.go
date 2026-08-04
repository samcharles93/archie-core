package provider

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/samcharles93/archie-core/internal/plugin"
	"github.com/samcharles93/archie-core/internal/tools"
)

// Atomic rollback is right for a provider the daemon requires. It is wrong
// for an external MCP server pulled from a registry at runtime -- exactly the
// category that is allowed to be absent.
//
// Observed on carina 2026-07-30: mcp.desktop-commander could not start
// (missing unzip in the runtime image) and archied crash-looped under
// Restart=on-failure, losing chat, the gateway and every builtin tool over
// one third-party npm package.
func TestOptionalProviderFailureDoesNotStopTheFamily(t *testing.T) {
	failure := errors.New("npx: command not found")

	tests := []struct {
		name string
		// break is applied to the optional engine.
		breakEngine func(*fakeEngine)
		wantReason  string
	}{
		{
			name:        "start fails",
			breakEngine: func(e *fakeEngine) { e.startErr = failure },
			wantReason:  "npx",
		},
		{
			name:        "discover fails",
			breakEngine: func(e *fakeEngine) { e.discoverErr = failure },
			wantReason:  "npx",
		},
		{
			name:        "start panics",
			breakEngine: func(e *fakeEngine) { e.startPanic = "boom" },
			wantReason:  "boom",
		},
		{
			name:        "discover panics",
			breakEngine: func(e *fakeEngine) { e.discoverPanic = "boom" },
			wantReason:  "boom",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			index := tools.NewRegistry()
			registry := NewRegistry(index)

			required := newFakeEngine("builtin")
			required.tools = []tools.ToolEntry{testTool("read"), testTool("shell")}
			if err := registry.Register(required); err != nil {
				t.Fatalf("Register(required): %v", err)
			}

			optional := newFakeEngine("mcp.desktop-commander")
			optional.tools = []tools.ToolEntry{testTool("desktop")}
			tc.breakEngine(optional)
			if err := registry.RegisterOptional(optional); err != nil {
				t.Fatalf("RegisterOptional: %v", err)
			}

			if err := registry.Start(context.Background()); err != nil {
				t.Fatalf("Start returned %v; an optional provider failing must not "+
					"fail the family, or the daemon exits and crash-loops", err)
			}

			// The builtins must still be there. This is the whole point.
			for _, name := range []string{"read", "shell"} {
				if _, ok := index.Get(name); !ok {
					t.Errorf("tool %q is missing: an optional provider's failure "+
						"rolled back the required providers", name)
				}
			}
			// The failed provider's tools must not be half-registered.
			if _, ok := index.Get("desktop"); ok {
				t.Error("tool \"desktop\" is registered despite its provider failing")
			}

			// Degraded, not healthy: a silent failure is how this went
			// unnoticed until the daemon crash-looped.
			health := registry.Health(context.Background())
			if health.Status != plugin.HealthDegraded {
				t.Errorf("Health = %q, want %q", health.Status, plugin.HealthDegraded)
			}
			if !strings.Contains(health.Message, "mcp.desktop-commander") {
				t.Errorf("Health message %q does not name the failed provider", health.Message)
			}

			skipped := registry.Skipped()
			if len(skipped) != 1 {
				t.Fatalf("Skipped() = %v, want exactly the failed provider", skipped)
			}
			if skipped[0].ID != "mcp.desktop-commander" {
				t.Errorf("Skipped()[0].ID = %q, want %q", skipped[0].ID, "mcp.desktop-commander")
			}
			if skipped[0].Err == nil {
				t.Error("Skipped()[0].Err is nil: the operator has nothing to act on")
			} else if !strings.Contains(skipped[0].Err.Error(), tc.wantReason) {
				t.Errorf("Skipped()[0].Err = %v, want it to mention %q", skipped[0].Err, tc.wantReason)
			}
		})
	}
}

// A required provider keeps today's all-or-nothing semantics.
func TestRequiredProviderFailureStillRollsBack(t *testing.T) {
	index := tools.NewRegistry()
	registry := NewRegistry(index)

	healthy := newFakeEngine("builtin")
	healthy.tools = []tools.ToolEntry{testTool("read")}
	if err := registry.Register(healthy); err != nil {
		t.Fatal(err)
	}
	broken := newFakeEngine("required-thing")
	broken.startErr = errors.New("nope")
	if err := registry.Register(broken); err != nil {
		t.Fatal(err)
	}

	if err := registry.Start(context.Background()); err == nil {
		t.Fatal("Start returned nil; a required provider failing must still fail the family")
	}
	if _, ok := index.Get("read"); ok {
		t.Error("tool \"read\" survived: the required-provider rollback no longer rolls back")
	}
}

// Dependencies of a skipped provider cannot run either. An optional dependent
// is skipped with the reason; a required dependent is a hard failure, because
// it declared it cannot work without something that is now absent.
func TestSkippedProviderCascadesToDependents(t *testing.T) {
	tests := []struct {
		name              string
		dependentOptional bool
		wantStartErr      bool
		wantSkippedIDs    []string
	}{
		{
			name:              "optional dependent is skipped too",
			dependentOptional: true,
			wantSkippedIDs:    []string{"dependent", "flaky"},
		},
		{
			name:         "required dependent fails the family",
			wantStartErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			index := tools.NewRegistry()
			registry := NewRegistry(index)

			flaky := newFakeEngine("flaky")
			flaky.startErr = errors.New("unavailable")
			if err := registry.RegisterOptional(flaky); err != nil {
				t.Fatal(err)
			}

			dependent := engineWithDependencies("dependent", "flaky")
			dependent.tools = []tools.ToolEntry{testTool("dependent-tool")}
			var err error
			if tc.dependentOptional {
				err = registry.RegisterOptional(dependent)
			} else {
				err = registry.Register(dependent)
			}
			if err != nil {
				t.Fatal(err)
			}

			startErr := registry.Start(context.Background())
			if tc.wantStartErr {
				if startErr == nil {
					t.Fatal("Start returned nil; a required provider depending on an " +
						"unavailable one must fail rather than run without it")
				}
				return
			}
			if startErr != nil {
				t.Fatalf("Start: %v", startErr)
			}
			if _, ok := index.Get("dependent-tool"); ok {
				t.Error("a provider whose dependency was skipped was started anyway")
			}

			got := make([]string, 0, len(registry.Skipped()))
			for _, s := range registry.Skipped() {
				got = append(got, s.ID)
			}
			slices.Sort(got)
			slices.Sort(tc.wantSkippedIDs)
			if !slices.Equal(got, tc.wantSkippedIDs) {
				t.Errorf("Skipped() = %v, want %v", got, tc.wantSkippedIDs)
			}
		})
	}
}

// All-optional-and-all-failing is still a successful start: the daemon runs
// with its builtins and nothing else, which is the degraded state we want
// rather than no daemon at all.
func TestEveryOptionalProviderFailing(t *testing.T) {
	index := tools.NewRegistry()
	registry := NewRegistry(index)

	for _, id := range []string{"mcp.one", "mcp.two"} {
		engine := newFakeEngine(id)
		engine.startErr = errors.New("gone")
		if err := registry.RegisterOptional(engine); err != nil {
			t.Fatal(err)
		}
	}

	if err := registry.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if got := registry.Health(context.Background()).Status; got != plugin.HealthDegraded {
		t.Errorf("Health = %q, want %q", got, plugin.HealthDegraded)
	}
	if len(registry.Skipped()) != 2 {
		t.Errorf("Skipped() = %v, want both providers", registry.Skipped())
	}
	if err := registry.Stop(context.Background()); err != nil {
		t.Errorf("Stop: %v", err)
	}
}

// A failed optional provider must still be stopped, or a half-started child
// process is left behind for the daemon's lifetime.
func TestSkippedOptionalProviderIsCleanedUp(t *testing.T) {
	registry := NewRegistry(tools.NewRegistry())

	optional := newFakeEngine("mcp.leaky")
	optional.discoverErr = errors.New("no tools")
	if err := registry.RegisterOptional(optional); err != nil {
		t.Fatal(err)
	}
	if err := registry.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if optional.stopCount != 1 {
		t.Errorf("stopCount = %d, want 1: a provider that started but failed to "+
			"discover must be stopped, not abandoned", optional.stopCount)
	}
}

// The carina failure, reproduced through the capability host -- the path
// main.go actually calls, and the one that returned the error that exited the
// daemon. Registering desktop-commander as required makes this fail with
// `start plugin module "archie.tools": ... unzip: not found`, which is
// verbatim what crash-looped archied on 2026-07-30.
func TestCarinaScenarioThroughCapabilityHost(t *testing.T) {
	index := tools.NewRegistry()
	registry := NewRegistry(index)

	builtins := newFakeEngine("archie.tools.builtin")
	builtins.tools = []tools.ToolEntry{testTool("read"), testTool("shell"), testTool("edit")}
	if err := registry.Register(builtins); err != nil {
		t.Fatal(err)
	}
	memory := newFakeEngine("archie.tools.memory")
	memory.tools = []tools.ToolEntry{testTool("memory_edit")}
	if err := registry.Register(memory); err != nil {
		t.Fatal(err)
	}
	desktop := newFakeEngine("mcp.desktop-commander")
	desktop.startErr = errors.New("npx exec: unzip: not found")
	if err := registry.RegisterOptional(desktop); err != nil {
		t.Fatal(err)
	}

	host := plugin.NewHost()
	if err := host.Register(registry); err != nil {
		t.Fatal(err)
	}
	if err := host.Start(context.Background()); err != nil {
		t.Fatalf("capability host startup failed: %v\n"+
			"main.go logs 'capability host startup' and returns 1 here, which "+
			"under Restart=on-failure is the crash loop", err)
	}

	// Everything the operator actually depends on is still there.
	for _, name := range []string{"read", "shell", "edit", "memory_edit"} {
		if _, ok := index.Get(name); !ok {
			t.Errorf("tool %q was lost to a third-party npm package", name)
		}
	}
	health := registry.Health(context.Background())
	if health.Status != plugin.HealthDegraded {
		t.Errorf("health = %q (%s), want degraded", health.Status, health.Message)
	}
	if !strings.Contains(health.Message, "unzip") {
		t.Errorf("health message %q does not carry the cause", health.Message)
	}
	if err := host.Stop(context.Background()); err != nil {
		t.Errorf("Stop: %v", err)
	}
}
