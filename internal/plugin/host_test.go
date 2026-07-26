package plugin_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/samcharles93/archie-core/internal/plugin"
)

func TestHostRegisterValidatesManifest(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		mutate   func(*plugin.Manifest)
		wantErr  bool
		register plugin.Module
	}{
		{name: "valid", mutate: func(*plugin.Manifest) {}},
		{name: "empty id", mutate: func(m *plugin.Manifest) { m.ID = "" }, wantErr: true},
		{name: "unstable id", mutate: func(m *plugin.Manifest) { m.ID = "Bad ID" }, wantErr: true},
		{name: "empty name", mutate: func(m *plugin.Manifest) { m.Name = "" }, wantErr: true},
		{name: "invalid version", mutate: func(m *plugin.Manifest) { m.Version = "v1" }, wantErr: true},
		{name: "empty prerelease identifier", mutate: func(m *plugin.Manifest) { m.Version = "1.0.0-alpha..1" }, wantErr: true},
		{name: "empty prerelease", mutate: func(m *plugin.Manifest) { m.Version = "1.0.0-." }, wantErr: true},
		{name: "leading zero prerelease", mutate: func(m *plugin.Manifest) { m.Version = "1.0.0-01" }, wantErr: true},
		{name: "invalid api version", mutate: func(m *plugin.Manifest) { m.APIVersion = "latest" }, wantErr: true},
		{name: "incompatible api major", mutate: func(m *plugin.Manifest) { m.APIVersion = "2.0.0" }, wantErr: true},
		{name: "newer api minor", mutate: func(m *plugin.Manifest) { m.APIVersion = "1.1.0" }, wantErr: true},
		{name: "no capabilities", mutate: func(m *plugin.Manifest) { m.Capabilities = nil }, wantErr: true},
		{name: "invalid capability", mutate: func(m *plugin.Manifest) { m.Capabilities = []plugin.CapabilityKind{"Bad Kind"} }, wantErr: true},
		{name: "duplicate capability", mutate: func(m *plugin.Manifest) {
			m.Capabilities = []plugin.CapabilityKind{"tools", "tools"}
		}, wantErr: true},
		{name: "self dependency", mutate: func(m *plugin.Manifest) { m.Dependencies = []string{m.ID} }, wantErr: true},
		{name: "invalid dependency", mutate: func(m *plugin.Manifest) { m.Dependencies = []string{"Bad ID"} }, wantErr: true},
		{name: "duplicate dependency", mutate: func(m *plugin.Manifest) {
			m.Dependencies = []string{"dependency", "dependency"}
		}, wantErr: true},
		{name: "invalid permission", mutate: func(m *plugin.Manifest) { m.Permissions = []plugin.Permission{"Bad Permission"} }, wantErr: true},
		{name: "duplicate permission", mutate: func(m *plugin.Manifest) {
			m.Permissions = []plugin.Permission{"network", "network"}
		}, wantErr: true},
		{name: "invalid config schema json", mutate: func(m *plugin.Manifest) { m.ConfigSchema = []byte(`{`) }, wantErr: true},
		{name: "non-object config schema", mutate: func(m *plugin.Manifest) { m.ConfigSchema = []byte(`[]`) }, wantErr: true},
		{name: "nil module", wantErr: true, register: (*fakeModule)(nil)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			manifest := validManifest("module")
			if tt.mutate != nil {
				tt.mutate(&manifest)
			}
			module := tt.register
			if module == nil {
				module = &fakeModule{manifest: manifest}
			}
			err := plugin.NewHost().Register(module)
			if (err != nil) != tt.wantErr {
				t.Fatalf("Register() error = %v, wantErr %t", err, tt.wantErr)
			}
		})
	}
}

func TestHostRegisterRejectsDuplicateAndMutationAfterStart(t *testing.T) {
	t.Parallel()

	host := plugin.NewHost()
	first := &fakeModule{manifest: validManifest("duplicate")}
	if err := host.Register(first); err != nil {
		t.Fatal(err)
	}
	if err := host.Register(&fakeModule{manifest: validManifest("duplicate")}); err == nil {
		t.Fatal("duplicate Register() succeeded")
	}
	if err := host.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := host.Register(&fakeModule{manifest: validManifest("late")}); err == nil {
		t.Fatal("Register() after Start succeeded")
	}
}

func TestHostManifestsAreImmutableSnapshots(t *testing.T) {
	t.Parallel()

	host := plugin.NewHost()
	manifest := validManifest("immutable")
	if err := host.Register(&fakeModule{manifest: manifest}); err != nil {
		t.Fatal(err)
	}

	manifest.Capabilities[0] = "mutated-at-source"
	manifest.Dependencies = append(manifest.Dependencies, "source-dependency")

	first := host.Manifests()
	if len(first) != 1 {
		t.Fatalf("Manifests() length = %d, want 1", len(first))
	}
	first[0].Capabilities[0] = "mutated-at-caller"
	first[0].Dependencies = append(first[0].Dependencies, "caller-dependency")

	second := host.Manifests()
	if got := second[0].Capabilities[0]; got != "tools" {
		t.Fatalf("stored capability = %q, want tools", got)
	}
	if len(second[0].Dependencies) != 0 {
		t.Fatalf("stored dependencies = %v, want empty", second[0].Dependencies)
	}
}

func TestHostStartsInDependencyOrderAndStopsInReverse(t *testing.T) {
	t.Parallel()

	events := &eventLog{}
	host := plugin.NewHost()
	modules := []*fakeModule{
		{manifest: validManifest("api", "database"), events: events},
		{manifest: validManifest("database"), events: events},
		{manifest: validManifest("worker", "api"), events: events},
	}
	for _, module := range modules {
		if err := host.Register(module); err != nil {
			t.Fatal(err)
		}
	}

	if err := host.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := host.Start(context.Background()); err != nil {
		t.Fatalf("second Start() error = %v", err)
	}
	if got, want := events.snapshot(), []string{"start:database", "start:api", "start:worker"}; !slices.Equal(got, want) {
		t.Fatalf("start events = %v, want %v", got, want)
	}

	if err := host.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := host.Stop(context.Background()); err != nil {
		t.Fatalf("second Stop() error = %v", err)
	}
	if got, want := events.snapshot(), []string{
		"start:database", "start:api", "start:worker",
		"stop:worker", "stop:api", "stop:database",
	}; !slices.Equal(got, want) {
		t.Fatalf("lifecycle events = %v, want %v", got, want)
	}
}

func TestHostStartRejectsMissingDependenciesAndCycles(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		modules []*fakeModule
	}{
		{
			name:    "missing dependency",
			modules: []*fakeModule{{manifest: validManifest("dependent", "missing")}},
		},
		{
			name: "dependency cycle",
			modules: []*fakeModule{
				{manifest: validManifest("first", "second")},
				{manifest: validManifest("second", "first")},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			events := &eventLog{}
			host := plugin.NewHost()
			for _, module := range tt.modules {
				module.events = events
				if err := host.Register(module); err != nil {
					t.Fatal(err)
				}
			}
			if err := host.Start(context.Background()); err == nil {
				t.Fatal("Start() succeeded")
			}
			if got := events.snapshot(); len(got) != 0 {
				t.Fatalf("lifecycle events = %v, want none", got)
			}
		})
	}
}

func TestHostStartFailureRollsBackAndIsolatesPanics(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		failing    *fakeModule
		errorMatch string
	}{
		{
			name: "returned error",
			failing: &fakeModule{
				manifest: validManifest("failing"),
				startErr: errors.New("start failure"),
				stopErr:  errors.New("rollback failure"),
			},
			errorMatch: "start failure",
		},
		{
			name:       "panic",
			failing:    &fakeModule{manifest: validManifest("failing"), startPanic: true},
			errorMatch: "panic",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			events := &eventLog{}
			host := plugin.NewHost()
			started := &fakeModule{manifest: validManifest("started"), events: events}
			tt.failing.manifest.Dependencies = []string{"started"}
			tt.failing.events = events
			never := &fakeModule{manifest: validManifest("never", "failing"), events: events}
			for _, module := range []*fakeModule{started, tt.failing, never} {
				if err := host.Register(module); err != nil {
					t.Fatal(err)
				}
			}

			err := host.Start(context.Background())
			if err == nil || !strings.Contains(err.Error(), tt.errorMatch) {
				t.Fatalf("Start() error = %v, want containing %q", err, tt.errorMatch)
			}
			if tt.failing.stopErr != nil && !strings.Contains(err.Error(), tt.failing.stopErr.Error()) {
				t.Fatalf("Start() error = %v, want rollback error %q", err, tt.failing.stopErr)
			}
			if got, want := events.snapshot(), []string{
				"start:started", "start:failing", "stop:failing", "stop:started",
			}; !slices.Equal(got, want) {
				t.Fatalf("rollback events = %v, want %v", got, want)
			}
		})
	}
}

func TestEmptyHostLifecycleIsTerminalAfterStop(t *testing.T) {
	t.Parallel()

	host := plugin.NewHost()
	if err := host.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := host.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := host.Start(context.Background()); err == nil {
		t.Fatal("Start() after Stop() succeeded")
	}
}

func TestHostRegisterContainsManifestPanics(t *testing.T) {
	t.Parallel()

	module := &fakeModule{manifestPanic: true}
	if err := plugin.NewHost().Register(module); err == nil || !strings.Contains(err.Error(), "panic") {
		t.Fatalf("Register() error = %v, want contained panic", err)
	}
}

func TestHostHealthRejectsInvalidProviderStatus(t *testing.T) {
	t.Parallel()

	host := plugin.NewHost()
	module := &fakeModule{
		manifest: validManifest("invalid-health"),
		health:   plugin.Health{Status: "surprising"},
	}
	if err := host.Register(module); err != nil {
		t.Fatal(err)
	}
	if err := host.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	status := host.Health(context.Background())[0]
	if status.Health.Status != plugin.HealthUnhealthy || !strings.Contains(status.Health.Message, "invalid health status") {
		t.Fatalf("health status = %+v", status)
	}
}

func TestHostStopContinuesAfterErrorsAndPanics(t *testing.T) {
	t.Parallel()

	events := &eventLog{}
	host := plugin.NewHost()
	modules := []*fakeModule{
		{manifest: validManifest("first"), events: events, stopErr: errors.New("stop failure")},
		{manifest: validManifest("second"), events: events, stopPanic: true},
		{manifest: validManifest("third"), events: events},
	}
	for _, module := range modules {
		if err := host.Register(module); err != nil {
			t.Fatal(err)
		}
	}
	if err := host.Start(context.Background()); err != nil {
		t.Fatal(err)
	}

	err := host.Stop(context.Background())
	if err == nil || !strings.Contains(err.Error(), "stop failure") || !strings.Contains(err.Error(), "panic") {
		t.Fatalf("Stop() error = %v, want joined error and panic", err)
	}
	if got, want := events.snapshot(), []string{
		"start:first", "start:second", "start:third",
		"stop:third", "stop:second", "stop:first",
	}; !slices.Equal(got, want) {
		t.Fatalf("stop events = %v, want %v", got, want)
	}
}

func TestHostRetriesModulesWhoseStopFailed(t *testing.T) {
	t.Parallel()

	stopFailure := errors.New("transient stop failure")
	host := plugin.NewHost()
	module := &fakeModule{
		manifest:   validManifest("retry-stop"),
		stopErrors: []error{stopFailure, nil},
	}
	if err := host.Register(module); err != nil {
		t.Fatal(err)
	}
	if err := host.Start(context.Background()); err != nil {
		t.Fatal(err)
	}

	if err := host.Stop(context.Background()); !errors.Is(err, stopFailure) {
		t.Fatalf("first Stop() error = %v, want %v", err, stopFailure)
	}
	if err := host.Stop(context.Background()); err != nil {
		t.Fatalf("second Stop() error = %v, want cleanup retry success", err)
	}
	if module.stopCount != 2 {
		t.Fatalf("Stop() calls = %d, want 2", module.stopCount)
	}
}

func TestHostStartupRollbackIsBoundedAndRetryable(t *testing.T) {
	t.Parallel()

	cleanupFailure := errors.New("rollback cleanup failure")
	host := plugin.NewHost()
	module := &fakeModule{
		manifest:   validManifest("rollback-retry"),
		startErr:   errors.New("start failure"),
		stopErrors: []error{cleanupFailure, nil},
	}
	if err := host.Register(module); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := host.Start(ctx)
	if !errors.Is(err, cleanupFailure) {
		t.Fatalf("Start() error = %v, want cleanup failure", err)
	}
	if len(module.stopContextDeadlines) != 1 || !module.stopContextDeadlines[0] {
		t.Fatalf("rollback Stop() deadline flags = %v, want bounded context", module.stopContextDeadlines)
	}
	if got := module.stopContextErrors[0]; got != nil {
		t.Fatalf("rollback Stop() context error = %v, want original cancellation detached", got)
	}
	if got := statusByID(host.Health(context.Background()), module.manifest.ID).State; got != plugin.StateFailed {
		t.Fatalf("failed module state = %q, want failed after successful or failed rollback cleanup", got)
	}
	if err := host.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() retry error = %v", err)
	}
	if module.stopCount != 2 {
		t.Fatalf("Stop() calls = %d, want rollback plus retry", module.stopCount)
	}
}

func TestHostHealthIsPanicSafeAndSnapshotBased(t *testing.T) {
	t.Parallel()

	host := plugin.NewHost()
	healthy := &fakeModule{
		manifest: validManifest("healthy"),
		health:   plugin.Health{Status: plugin.HealthHealthy, Message: "ready"},
	}
	panicky := &fakeModule{manifest: validManifest("panicky"), healthPanic: true}
	for _, module := range []*fakeModule{healthy, panicky} {
		if err := host.Register(module); err != nil {
			t.Fatal(err)
		}
	}

	before := host.Health(context.Background())
	for _, status := range before {
		if status.State != plugin.StateRegistered || status.Health.Status != plugin.HealthUnknown {
			t.Fatalf("pre-start status = %+v, want registered/unknown", status)
		}
	}

	if err := host.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	after := host.Health(context.Background())
	if got := statusByID(after, "healthy"); got.State != plugin.StateRunning || got.Health.Status != plugin.HealthHealthy {
		t.Fatalf("healthy status = %+v", got)
	}
	if got := statusByID(after, "panicky"); got.State != plugin.StateRunning || got.Health.Status != plugin.HealthUnhealthy ||
		!strings.Contains(got.Health.Message, "panic") {
		t.Fatalf("panicky status = %+v", got)
	}

	after[0].Manifest.Capabilities[0] = "caller-mutation"
	if got := host.Health(context.Background())[0].Manifest.Capabilities[0]; got != "tools" {
		t.Fatalf("stored health manifest capability = %q, want tools", got)
	}
}

func TestAdaptLegacyCreatesMetadataOnlyLifecycleModule(t *testing.T) {
	t.Parallel()

	legacy := &fakeLegacyPlugin{name: "legacy-plugin", version: "1.4.2"}
	module, err := plugin.AdaptLegacy(legacy)
	if err != nil {
		t.Fatal(err)
	}
	manifest := module.Manifest()
	if manifest.ID != legacy.name || manifest.Name != legacy.name || manifest.Version != legacy.version {
		t.Fatalf("legacy manifest = %+v", manifest)
	}
	if got, want := manifest.Capabilities, []plugin.CapabilityKind{"plugin.metadata"}; !slices.Equal(got, want) {
		t.Fatalf("legacy capabilities = %v, want %v", got, want)
	}
	if err := module.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if health := module.Health(context.Background()); health.Status != plugin.HealthHealthy {
		t.Fatalf("legacy health = %+v, want healthy", health)
	}
	if err := module.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestAdaptLegacyRejectsInvalidOrPanickingMetadata(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		plugin plugin.Plugin
	}{
		{name: "nil", plugin: (*fakeLegacyPlugin)(nil)},
		{name: "invalid name", plugin: &fakeLegacyPlugin{name: "Bad Name", version: "1.0.0"}},
		{name: "invalid version", plugin: &fakeLegacyPlugin{name: "legacy", version: "latest"}},
		{name: "name panic", plugin: &fakeLegacyPlugin{namePanic: true}},
		{name: "version panic", plugin: &fakeLegacyPlugin{name: "legacy", versionPanic: true}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := plugin.AdaptLegacy(tt.plugin); err == nil {
				t.Fatal("AdaptLegacy() succeeded")
			}
		})
	}
}

func validManifest(id string, dependencies ...string) plugin.Manifest {
	return plugin.Manifest{
		ID:           id,
		Name:         id,
		Version:      "1.2.3",
		APIVersion:   plugin.HostAPIVersion,
		Capabilities: []plugin.CapabilityKind{"tools"},
		Dependencies: dependencies,
		Permissions:  []plugin.Permission{"network"},
		ConfigSchema: []byte(`{"type":"object"}`),
	}
}

type eventLog struct {
	mu     sync.Mutex
	events []string
}

func (l *eventLog) add(event string) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.events = append(l.events, event)
}

func (l *eventLog) snapshot() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.Clone(l.events)
}

type fakeModule struct {
	manifest             plugin.Manifest
	events               *eventLog
	startErr             error
	stopErr              error
	stopErrors           []error
	stopCount            int
	stopContextDeadlines []bool
	stopContextErrors    []error
	startPanic           bool
	stopPanic            bool
	healthPanic          bool
	health               plugin.Health
	manifestPanic        bool
}

type fakeLegacyPlugin struct {
	name         string
	version      string
	namePanic    bool
	versionPanic bool
}

func (p *fakeLegacyPlugin) Name() string {
	if p.namePanic {
		panic("name panic")
	}
	return p.name
}

func (p *fakeLegacyPlugin) Version() string {
	if p.versionPanic {
		panic("version panic")
	}
	return p.version
}

func (m *fakeModule) Manifest() plugin.Manifest {
	if m.manifestPanic {
		panic("manifest panic")
	}
	return m.manifest
}

func (m *fakeModule) Start(context.Context) error {
	m.events.add("start:" + m.manifest.ID)
	if m.startPanic {
		panic("start panic")
	}
	return m.startErr
}

func (m *fakeModule) Health(context.Context) plugin.Health {
	if m.healthPanic {
		panic("health panic")
	}
	return m.health
}

func (m *fakeModule) Stop(ctx context.Context) error {
	m.events.add("stop:" + m.manifest.ID)
	m.stopCount++
	_, hasDeadline := ctx.Deadline()
	m.stopContextDeadlines = append(m.stopContextDeadlines, hasDeadline)
	m.stopContextErrors = append(m.stopContextErrors, ctx.Err())
	if m.stopPanic {
		panic("stop panic")
	}
	if len(m.stopErrors) >= m.stopCount {
		return m.stopErrors[m.stopCount-1]
	}
	return m.stopErr
}

func statusByID(statuses []plugin.ModuleStatus, id string) plugin.ModuleStatus {
	for _, status := range statuses {
		if status.Manifest.ID == id {
			return status
		}
	}
	return plugin.ModuleStatus{}
}
