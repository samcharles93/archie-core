package archied

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/samcharles93/archie-core/internal/app/controlplane"
	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/domain/eda/module"
	"github.com/samcharles93/archie-core/internal/plugin"
	"github.com/samcharles93/archie-core/internal/secret"
)

// docs/prds/plugin-settings-live.md: a plugin or secret-engine file dropped
// into the running configuration's directory loads without a restart, an
// edited file replaces what runs, and a removed file keeps running with the
// removal reported through apply status until the process restarts.

const reconcilerPluginSource = `package main

import "github.com/samcharles93/archie-core/internal/plugin"

var Plugin = plugin._Plugin{
	WName:    func() string { return "hello" },
	WVersion: func() string { return "1.0.0" },
}
`

const reconcilerSecretSource = `package main

import "github.com/samcharles93/archie-core/internal/secret"

var Engine = secret._Engine{
	WName:    func() string { return "hello" },
	WVersion: func() string { return "1.0.0" },
	WResolve: func(key string) (string, error) { return "one:" + key, nil },
}
`

const reconcilerEditedSecretSource = `package main

import "github.com/samcharles93/archie-core/internal/secret"

var Engine = secret._Engine{
	WName:    func() string { return "hello" },
	WVersion: func() string { return "2.0.0" },
	WResolve: func(key string) (string, error) { return "two:" + key, nil },
}
`

// reconcilerHarness is one running directory set: a started plugin host, a
// fresh secret registry, an empty module registry, and the config that names
// the two directories a test writes into.
type reconcilerHarness struct {
	root     *plugin.Host
	secrets  *secret.Registry
	cfg      config.Config
	plugins  string
	secretsD string
	mods     *module.ModuleRegistry
}

func newReconcilerHarness(t *testing.T) *reconcilerHarness {
	t.Helper()
	h := &reconcilerHarness{
		root:     plugin.NewHost(),
		secrets:  secret.NewRegistry(),
		mods:     module.New(),
		plugins:  t.TempDir(),
		secretsD: t.TempDir(),
	}
	if err := h.root.Start(t.Context()); err != nil {
		t.Fatalf("start host: %v", err)
	}
	h.cfg = config.Config{PluginDir: h.plugins, SecretEngineDir: h.secretsD}
	return h
}

func (h *reconcilerHarness) reconciler() *pluginReconciler {
	return newPluginReconciler(slog.New(slog.DiscardHandler), pluginReconcileTargets{
		host:    h.root,
		secrets: h.secrets,
		modules: h.mods,
		dirs:    func() config.Config { return h.cfg },
	})
}

func writeReconcilerFile(t *testing.T, path, source string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestPluginReconcilerLoadsADroppedPluginAndSecretEngine(t *testing.T) {
	h := newReconcilerHarness(t)
	r := h.reconciler()
	// Boot loaded the (empty) directories; seed is what the daemon does after
	// its own boot load so the first tick does not re-load the boot set.
	r.seed()

	writeReconcilerFile(t, filepath.Join(h.plugins, "hello.go"), reconcilerPluginSource)
	writeReconcilerFile(t, filepath.Join(h.secretsD, "hello.go"), reconcilerSecretSource)

	if err := r.reconcile(t.Context()); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if !h.root.Has("hello") {
		t.Error("dropped plugin was not loaded in place")
	}
	engine, ok := h.secrets.Get("hello")
	if !ok {
		t.Fatal("dropped secret engine was not loaded in place")
	}
	value, err := engine.Resolve("k")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if value != "one:k" {
		t.Errorf("Resolve() = %q, want one:k", value)
	}
}

func TestPluginReconcilerReplacesAnEditedSecretEngine(t *testing.T) {
	h := newReconcilerHarness(t)
	r := h.reconciler()
	r.seed()

	path := filepath.Join(h.secretsD, "hello.go")
	writeReconcilerFile(t, path, reconcilerSecretSource)
	if err := r.reconcile(t.Context()); err != nil {
		t.Fatalf("first reconcile: %v", err)
	}

	writeReconcilerFile(t, path, reconcilerEditedSecretSource)
	if err := r.reconcile(t.Context()); err != nil {
		t.Fatalf("second reconcile: %v", err)
	}
	engine, ok := h.secrets.Get("hello")
	if !ok {
		t.Fatal("edited secret engine disappeared")
	}
	value, err := engine.Resolve("k")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if value != "two:k" {
		t.Errorf("Resolve() after edit = %q, want the replaced two:k", value)
	}
}

func TestPluginReconcilerReportsARemovedFileThatKeepsRunning(t *testing.T) {
	h := newReconcilerHarness(t)
	r := h.reconciler()
	r.seed()

	pluginPath := filepath.Join(h.plugins, "hello.go")
	secretPath := filepath.Join(h.secretsD, "hello.go")
	writeReconcilerFile(t, pluginPath, reconcilerPluginSource)
	writeReconcilerFile(t, secretPath, reconcilerSecretSource)
	if err := r.reconcile(t.Context()); err != nil {
		t.Fatalf("first reconcile: %v", err)
	}

	if err := os.Remove(pluginPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(secretPath); err != nil {
		t.Fatal(err)
	}
	err := r.reconcile(t.Context())
	if err == nil {
		t.Fatal("a reconcile after two removals reported no problem")
	}
	for _, want := range []string{"hello.go", "removed", "restart"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("removal report %q does not mention %q", err, want)
		}
	}
	// The running code is not unloaded: Yaegi has no unload path, so both the
	// module and the engine keep serving until the process restarts.
	if !h.root.Has("hello") {
		t.Error("a removed plugin stopped running")
	}
	if _, ok := h.secrets.Get("hello"); !ok {
		t.Error("a removed secret engine was unregistered")
	}

	// Re-adding the file clears the removal: the replacement takes over and no
	// old interpreter is left serving the path.
	writeReconcilerFile(t, pluginPath, reconcilerPluginSource)
	writeReconcilerFile(t, secretPath, reconcilerSecretSource)
	if err := r.reconcile(t.Context()); err != nil {
		t.Fatalf("reconcile after re-adding the files: %v", err)
	}
}

func TestPluginSettingsLiveUpdateLoadsTheNewDirectory(t *testing.T) {
	dir := t.TempDir()
	writeReconcilerFile(t, filepath.Join(dir, "hello.go"), reconcilerPluginSource)

	running := fileConfig()
	b, status := newLiveApplyBoot(t, running)
	resources := databaseOwnedResources()
	resources[controlplane.PluginSettingsKind] = map[string]any{
		"plugin_dir": dir, "module_dir": "/db/modules", "secret_engine_dir": "/db/secrets", "skills_dir": "",
	}
	b.controlPlane = controlplane.NewRPCClient(&controlPlaneStub{values: resources})
	b.secrets = secret.NewRegistry()
	host := plugin.NewHost()
	if err := host.Start(t.Context()); err != nil {
		t.Fatalf("start host: %v", err)
	}
	b.pluginReconciler = newPluginReconciler(b.log, pluginReconcileTargets{
		host:    host,
		secrets: b.secrets,
		modules: module.New(),
		dirs:    func() config.Config { return b.cfgHolder.Get() },
	})
	b.pluginReconciler.seed()

	b.applyRuntimeResourceUpdate(t.Context(), controlplane.PluginSettingsKind, controlplane.AppliedResource{Version: 2})

	if got := b.cfgHolder.Get().PluginDir; got != dir {
		t.Fatalf("running plugin dir = %q, want the stored %q", got, dir)
	}
	if !host.Has("hello") {
		t.Fatal("a stored plugin_dir change did not load its plugin without a restart")
	}
	last, ok := status.last(controlplane.PluginSettingsKind)
	if !ok {
		t.Fatal("the live plugin-settings update reported no apply status")
	}
	if last.Error != "" {
		t.Errorf("apply status error = %q, want none for a live directory load", last.Error)
	}
}

func TestPluginSettingsSkillsDirChangeIsRefusedOnTheLivePath(t *testing.T) {
	running := fileConfig()
	b, status := newLiveApplyBoot(t, running)
	// The stored document's skills_dir (/db/skills) differs from the running
	// empty value, and skills_dir has no live consumer.
	b.controlPlane = controlplane.NewRPCClient(&controlPlaneStub{values: databaseOwnedResources()})

	b.applyRuntimeResourceUpdate(t.Context(), controlplane.PluginSettingsKind, controlplane.AppliedResource{Version: 2})

	if got := b.cfgHolder.Get().PluginDir; got != fileConfig().PluginDir {
		t.Errorf("a refused plugin-settings document published plugin_dir %q, want the file's %q", got, fileConfig().PluginDir)
	}
	last, ok := status.last(controlplane.PluginSettingsKind)
	if !ok {
		t.Fatal("the refused plugin-settings update reported no apply status")
	}
	if !strings.Contains(last.Error, "skills_dir") {
		t.Errorf("apply status error = %q, want it to name skills_dir", last.Error)
	}
	if last.AppliedVersion != 2 {
		t.Errorf("apply status version = %d, want the refused version 2", last.AppliedVersion)
	}
}

func TestPluginReconcilerReportsTheApplyStatusTruth(t *testing.T) {
	h := newReconcilerHarness(t)
	var reported []error
	reported = nil
	r := newPluginReconciler(slog.New(slog.DiscardHandler), pluginReconcileTargets{
		host:    h.root,
		secrets: h.secrets,
		modules: h.mods,
		dirs:    func() config.Config { return h.cfg },
		report:  func(_ context.Context, err error) { reported = append(reported, err) },
	})
	r.seed()

	writeReconcilerFile(t, filepath.Join(h.secretsD, "hello.go"), reconcilerSecretSource)
	if err := r.reconcile(t.Context()); err != nil {
		t.Fatalf("clean reconcile: %v", err)
	}
	if len(reported) != 1 || reported[0] != nil {
		t.Fatalf("clean reconcile reported %v, want one nil (a live add is applied)", reported)
	}

	if err := os.Remove(filepath.Join(h.secretsD, "hello.go")); err != nil {
		t.Fatal(err)
	}
	if err := r.reconcile(t.Context()); err == nil {
		t.Fatal("removal reconcile reported no error")
	}
	if len(reported) != 2 || reported[1] == nil {
		t.Fatalf("removal reconcile reported %v, want the removal as the second record", reported)
	}
}
