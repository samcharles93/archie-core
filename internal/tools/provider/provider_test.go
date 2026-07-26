package provider

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"

	"github.com/samcharles93/archie-core/internal/plugin"
	"github.com/samcharles93/archie-core/internal/tools"
)

func TestRegistryRegisterAndGet(t *testing.T) {
	tests := []struct {
		name    string
		engine  Engine
		wantErr bool
	}{
		{name: "valid", engine: newFakeEngine("valid")},
		{name: "nil", wantErr: true},
		{
			name: "invalid manifest",
			engine: &fakeEngine{manifest: plugin.Manifest{
				ID: "INVALID",
			}},
			wantErr: true,
		},
		{
			name: "wrong capability",
			engine: &fakeEngine{manifest: testManifest(
				"channel-provider",
				[]plugin.CapabilityKind{"channels"},
				nil,
			)},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			registry := NewRegistry(tools.NewRegistry())
			err := registry.Register(tt.engine)
			if (err != nil) != tt.wantErr {
				t.Fatalf("Register() error = %v, wantErr %t", err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			got, ok := registry.Get(tt.engine.Manifest().ID)
			if !ok || got != tt.engine {
				t.Fatalf("Get() = (%v, %t), want registered engine", got, ok)
			}
		})
	}
}

func TestRegistryRejectsDuplicateAndLateRegistration(t *testing.T) {
	registry := NewRegistry(tools.NewRegistry())
	first := newFakeEngine("duplicate")
	if err := registry.Register(first); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(newFakeEngine("duplicate")); err == nil {
		t.Fatal("duplicate provider was accepted")
	}
	if err := registry.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(newFakeEngine("late")); err == nil {
		t.Fatal("provider registration after start was accepted")
	}
}

func TestRegistryStartsInDependencyOrderAndStopsInReverse(t *testing.T) {
	index := tools.NewRegistry()
	registry := NewRegistry(index)
	var calls callLog

	first := newFakeEngine("first")
	first.calls = &calls
	first.tools = []tools.ToolEntry{testTool("first.tool")}

	second := newFakeEngine("second")
	second.manifest.Dependencies = []string{"first"}
	second.calls = &calls
	second.tools = []tools.ToolEntry{testTool("second.tool")}

	for _, engine := range []Engine{second, first} {
		if err := registry.Register(engine); err != nil {
			t.Fatal(err)
		}
	}
	if err := registry.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got, want := calls.snapshot(), []string{
		"start:first", "discover:first",
		"start:second", "discover:second",
	}; !slices.Equal(got, want) {
		t.Fatalf("startup calls = %v, want %v", got, want)
	}
	for _, name := range []string{"first.tool", "second.tool"} {
		if _, ok := index.Get(name); !ok {
			t.Fatalf("tool %q was not indexed", name)
		}
	}

	if err := registry.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got, want := calls.snapshot(), []string{
		"start:first", "discover:first",
		"start:second", "discover:second",
		"stop:second", "stop:first",
	}; !slices.Equal(got, want) {
		t.Fatalf("lifecycle calls = %v, want %v", got, want)
	}
	if got := index.All(); len(got) != 0 {
		t.Fatalf("tools remain after stop: %v", toolNames(got))
	}
	if err := registry.Stop(context.Background()); err != nil {
		t.Fatalf("second Stop() = %v, want idempotent success", err)
	}
}

func TestRegistryStartRejectsMissingAndCyclicDependencies(t *testing.T) {
	tests := []struct {
		name    string
		engines []*fakeEngine
	}{
		{
			name: "missing",
			engines: []*fakeEngine{
				engineWithDependencies("one", "absent"),
			},
		},
		{
			name: "cycle",
			engines: []*fakeEngine{
				engineWithDependencies("one", "two"),
				engineWithDependencies("two", "one"),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			registry := NewRegistry(tools.NewRegistry())
			for _, engine := range tt.engines {
				if err := registry.Register(engine); err != nil {
					t.Fatal(err)
				}
			}
			if err := registry.Start(context.Background()); err == nil {
				t.Fatal("Start() succeeded, want dependency error")
			}
			for _, engine := range tt.engines {
				if engine.startCount != 0 {
					t.Fatalf("provider %q started despite invalid dependency graph", engine.manifest.ID)
				}
			}
		})
	}
}

func TestRegistryStartRollsBackOnProviderFailures(t *testing.T) {
	tests := []struct {
		name        string
		configure   func(first, second *fakeEngine)
		preexisting []tools.ToolEntry
	}{
		{
			name: "start error",
			configure: func(_, second *fakeEngine) {
				second.startErr = errors.New("start failed")
			},
		},
		{
			name: "start panic",
			configure: func(_, second *fakeEngine) {
				second.startPanic = "start panic"
			},
		},
		{
			name: "discovery error",
			configure: func(_, second *fakeEngine) {
				second.discoverErr = errors.New("discover failed")
			},
		},
		{
			name: "discovery panic",
			configure: func(_, second *fakeEngine) {
				second.discoverPanic = "discover panic"
			},
		},
		{
			name: "tool collision",
			configure: func(_, second *fakeEngine) {
				second.tools = []tools.ToolEntry{
					testTool("new.before.collision"),
					testTool("taken"),
				}
			},
			preexisting: []tools.ToolEntry{testTool("taken")},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			index := tools.NewRegistry()
			for _, entry := range tt.preexisting {
				if err := index.Register(entry); err != nil {
					t.Fatal(err)
				}
			}
			registry := NewRegistry(index)
			first := newFakeEngine("first")
			first.tools = []tools.ToolEntry{testTool("first.tool")}
			second := engineWithDependencies("second", "first")
			second.tools = []tools.ToolEntry{testTool("second.tool")}
			tt.configure(first, second)
			for _, engine := range []Engine{first, second} {
				if err := registry.Register(engine); err != nil {
					t.Fatal(err)
				}
			}

			if err := registry.Start(context.Background()); err == nil {
				t.Fatal("Start() succeeded, want failure")
			}
			if first.stopCount != 1 {
				t.Fatalf("first stop count = %d, want rollback stop", first.stopCount)
			}
			if second.startCount > 0 && second.stopCount != 1 {
				t.Fatalf("second stop count = %d after starting, want rollback stop", second.stopCount)
			}
			if _, ok := index.Get("first.tool"); ok {
				t.Fatal("first provider tool remains after rollback")
			}
			if _, ok := index.Get("new.before.collision"); ok {
				t.Fatal("partial colliding batch was registered")
			}
			if len(tt.preexisting) > 0 {
				if _, ok := index.Get("taken"); !ok {
					t.Fatal("preexisting colliding tool was removed")
				}
			}
		})
	}
}

func TestRegistryStopContinuesAndJoinsErrors(t *testing.T) {
	index := tools.NewRegistry()
	registry := NewRegistry(index)
	first := newFakeEngine("first")
	first.tools = []tools.ToolEntry{testTool("first.tool")}
	first.stopErr = errors.New("first stop")
	second := engineWithDependencies("second", "first")
	second.tools = []tools.ToolEntry{testTool("second.tool")}
	second.stopPanic = "second stop panic"
	for _, engine := range []Engine{first, second} {
		if err := registry.Register(engine); err != nil {
			t.Fatal(err)
		}
	}
	if err := registry.Start(context.Background()); err != nil {
		t.Fatal(err)
	}

	err := registry.Stop(context.Background())
	if err == nil {
		t.Fatal("Stop() error = nil, want joined stop failures")
	}
	if first.stopCount != 1 || second.stopCount != 1 {
		t.Fatalf("stop counts = (%d, %d), want both stopped", first.stopCount, second.stopCount)
	}
	if len(index.All()) != 0 {
		t.Fatal("tools remain indexed after failed provider stops")
	}
}

func TestRegistryRetriesFailedProviderStops(t *testing.T) {
	stopFailure := errors.New("transient provider stop")
	index := tools.NewRegistry()
	registry := NewRegistry(index)
	engine := newFakeEngine("retry")
	engine.tools = []tools.ToolEntry{testTool("retry.tool")}
	engine.stopErrors = []error{stopFailure, nil}
	if err := registry.Register(engine); err != nil {
		t.Fatal(err)
	}
	if err := registry.Start(context.Background()); err != nil {
		t.Fatal(err)
	}

	if err := registry.Stop(context.Background()); !errors.Is(err, stopFailure) {
		t.Fatalf("first Stop() error = %v, want %v", err, stopFailure)
	}
	if _, ok := index.Get("retry.tool"); ok {
		t.Fatal("tool remains available while its provider failed to stop")
	}
	if err := registry.Stop(context.Background()); err != nil {
		t.Fatalf("second Stop() error = %v, want retry success", err)
	}
	if engine.stopCount != 2 {
		t.Fatalf("provider Stop() calls = %d, want 2", engine.stopCount)
	}
}

func TestRegistryRetriesFailedStartupRollback(t *testing.T) {
	cleanupFailure := errors.New("rollback stop failed")
	registry := NewRegistry(tools.NewRegistry())
	first := newFakeEngine("first")
	first.stopErrors = []error{cleanupFailure, nil}
	second := engineWithDependencies("second", "first")
	second.startErr = errors.New("second start failed")
	for _, engine := range []Engine{first, second} {
		if err := registry.Register(engine); err != nil {
			t.Fatal(err)
		}
	}

	if err := registry.Start(context.Background()); !errors.Is(err, cleanupFailure) {
		t.Fatalf("Start() error = %v, want rollback cleanup failure", err)
	}
	if err := registry.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() retry error = %v", err)
	}
	if first.stopCount != 2 {
		t.Fatalf("first provider Stop() calls = %d, want rollback plus retry", first.stopCount)
	}
}

func TestRegistryStartupRollbackUsesBoundedDetachedContext(t *testing.T) {
	registry := NewRegistry(tools.NewRegistry())
	engine := newFakeEngine("bounded-cleanup")
	engine.startErr = errors.New("start failed")
	if err := registry.Register(engine); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := registry.Start(ctx); err == nil {
		t.Fatal("Start() succeeded")
	}
	if len(engine.stopContextDeadlines) != 1 || !engine.stopContextDeadlines[0] {
		t.Fatalf("Stop() deadline flags = %v, want bounded cleanup", engine.stopContextDeadlines)
	}
	if got := engine.stopContextErrors[0]; got != nil {
		t.Fatalf("Stop() context error = %v, want detached original cancellation", got)
	}
}

func TestRegistryHealthAggregatesProviderHealthAndPanics(t *testing.T) {
	tests := []struct {
		name   string
		health plugin.Health
		panic  any
		want   plugin.HealthStatus
	}{
		{name: "healthy", health: plugin.Health{Status: plugin.HealthHealthy}, want: plugin.HealthHealthy},
		{name: "degraded", health: plugin.Health{Status: plugin.HealthDegraded}, want: plugin.HealthDegraded},
		{name: "unhealthy", health: plugin.Health{Status: plugin.HealthUnhealthy}, want: plugin.HealthUnhealthy},
		{name: "panic", panic: "health panic", want: plugin.HealthUnhealthy},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			registry := NewRegistry(tools.NewRegistry())
			engine := newFakeEngine("health")
			engine.health = tt.health
			engine.healthPanic = tt.panic
			if err := registry.Register(engine); err != nil {
				t.Fatal(err)
			}
			if err := registry.Start(context.Background()); err != nil {
				t.Fatal(err)
			}
			if got := registry.Health(context.Background()).Status; got != tt.want {
				t.Fatalf("Health().Status = %q, want %q", got, tt.want)
			}
		})
	}
}

type fakeEngine struct {
	manifest             plugin.Manifest
	tools                []tools.ToolEntry
	startErr             error
	discoverErr          error
	stopErr              error
	stopErrors           []error
	startPanic           any
	discoverPanic        any
	stopPanic            any
	healthPanic          any
	health               plugin.Health
	startCount           int
	discoverCount        int
	stopCount            int
	stopContextDeadlines []bool
	stopContextErrors    []error
	calls                *callLog
}

func newFakeEngine(id string) *fakeEngine {
	return &fakeEngine{
		manifest: testManifest(id, []plugin.CapabilityKind{"tools"}, nil),
		health:   plugin.Health{Status: plugin.HealthHealthy},
	}
}

func engineWithDependencies(id string, dependencies ...string) *fakeEngine {
	engine := newFakeEngine(id)
	engine.manifest.Dependencies = dependencies
	return engine
}

func testManifest(
	id string,
	capabilities []plugin.CapabilityKind,
	dependencies []string,
) plugin.Manifest {
	return plugin.Manifest{
		ID:           id,
		Name:         id,
		Version:      "1.0.0",
		APIVersion:   plugin.HostAPIVersion,
		Capabilities: capabilities,
		Dependencies: dependencies,
	}
}

func (e *fakeEngine) Manifest() plugin.Manifest {
	return e.manifest
}

func (e *fakeEngine) Start(context.Context) error {
	e.startCount++
	e.log("start")
	if e.startPanic != nil {
		panic(e.startPanic)
	}
	return e.startErr
}

func (e *fakeEngine) Discover(context.Context) ([]tools.ToolEntry, error) {
	e.discoverCount++
	e.log("discover")
	if e.discoverPanic != nil {
		panic(e.discoverPanic)
	}
	return e.tools, e.discoverErr
}

func (e *fakeEngine) Health(context.Context) plugin.Health {
	if e.healthPanic != nil {
		panic(e.healthPanic)
	}
	return e.health
}

func (e *fakeEngine) Stop(ctx context.Context) error {
	e.stopCount++
	e.log("stop")
	_, hasDeadline := ctx.Deadline()
	e.stopContextDeadlines = append(e.stopContextDeadlines, hasDeadline)
	e.stopContextErrors = append(e.stopContextErrors, ctx.Err())
	if e.stopPanic != nil {
		panic(e.stopPanic)
	}
	if len(e.stopErrors) >= e.stopCount {
		return e.stopErrors[e.stopCount-1]
	}
	return e.stopErr
}

func (e *fakeEngine) log(operation string) {
	if e.calls != nil {
		e.calls.add(fmt.Sprintf("%s:%s", operation, e.manifest.ID))
	}
}

type callLog struct {
	mu    sync.Mutex
	calls []string
}

func (l *callLog) add(call string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.calls = append(l.calls, call)
}

func (l *callLog) snapshot() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.calls...)
}

func testTool(name string) tools.ToolEntry {
	return tools.ToolEntry{
		Name: name,
		Handler: func(context.Context, map[string]any) (any, error) {
			return name, nil
		},
	}
}

func toolNames(entries []tools.ToolEntry) []string {
	names := make([]string, len(entries))
	for i, entry := range entries {
		names[i] = entry.Name
	}
	slices.Sort(names)
	return names
}
