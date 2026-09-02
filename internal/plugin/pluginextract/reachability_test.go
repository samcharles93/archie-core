package pluginextract

import (
	"context"
	"testing"

	"github.com/traefik/yaegi/interp"

	"github.com/samcharles93/archie-core/internal/plugin"
	"github.com/samcharles93/archie-core/internal/yaegiutil"
)

// TestPreviouslyMissingSymbolsReachableFromInterpretedCode is the regression
// test for the stale symbol-table drift (e7bo): the committed generated file
// predated internal/plugin/host.go (f73b2d1) and was never regenerated, so
// the Host/Module/Manifest/Permission/Health/CapabilityKind/LifecycleState/
// ModuleStatus types and the NewHost/AdaptLegacy/HostAPIVersion identifiers
// that plugin.go's LoadDir exposes to interpreted daemon plugins were absent
// from the table. A daemon plugin written against the capability-host API
// could not resolve them, silently.
//
// This test interprets a plugin that references the previously-missing
// symbols, resolves it, and calls it -- proving the symbols are wired.
func TestPreviouslyMissingSymbolsReachableFromInterpretedCode(t *testing.T) {
	src := `package main

import (
	"context"
	"fmt"

	"github.com/samcharles93/archie-core/internal/plugin"
)

// BuildHost exercises NewHost and HostAPIVersion -- the module-capability
// registration surface that was missing from the symbol table.
func BuildHost(m plugin.Module) (string, error) {
	h := plugin.NewHost()
	if h == nil {
		return "", fmt.Errorf("NewHost returned nil")
	}
	if plugin.HostAPIVersion == "" {
		return "", fmt.Errorf("HostAPIVersion empty")
	}
	if err := h.Register(m); err != nil {
		return "", err
	}
	if err := h.Start(context.Background()); err != nil {
		return "", err
	}
	defer h.Stop(context.Background())
	return plugin.HostAPIVersion, nil
}
`

	i, err := yaegiutil.New(interp.Options{}, Symbols)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	buildHost, err := yaegiutil.Resolve[func(plugin.Module) (string, error)](i, src, "main.BuildHost")
	if err != nil {
		t.Fatalf("Resolve BuildHost: %v", err)
	}

	// Call through with a Go-side module implementing the contract. This
	// proves the interpreted host API works across the boundary end to end.
	mod := testModule{}
	version, err := buildHost(mod)
	if err != nil {
		t.Fatalf("BuildHost: %v", err)
	}
	if version != plugin.HostAPIVersion {
		t.Errorf("BuildHost version = %q, want %q", version, plugin.HostAPIVersion)
	}
}

// testModule implements plugin.Module for the Go-side call.
type testModule struct{}

func (testModule) Manifest() plugin.Manifest {
	return plugin.Manifest{
		ID:           "test",
		Name:         "test",
		Version:      "1.0.0",
		APIVersion:   plugin.HostAPIVersion,
		Capabilities: []plugin.CapabilityKind{"test.capability"},
	}
}

func (testModule) Start(context.Context) error { return nil }
func (testModule) Health(context.Context) plugin.Health {
	return plugin.Health{Status: plugin.HealthHealthy}
}
func (testModule) Stop(context.Context) error { return nil }

// TestWrapperNilGuardsPreserved follows the established pattern: yaegi's
// fresh wrapper generation omits nil-guards on interface-wrappers, so they
// are re-applied by hand after every regeneration. A zero-value wrapper
// (all function fields nil) must not panic.
func TestWrapperNilGuardsPreserved(t *testing.T) {
	var mod _github_com_samcharles93_archie_core_internal_plugin_Module
	if err := mod.Start(context.Background()); err != nil {
		t.Errorf("Start on nil WStart = %v, want nil", err)
	}
	if got := mod.Manifest(); got.ID != "" || got.Name != "" || got.APIVersion != "" {
		t.Errorf("Manifest on nil WManifest = %#v, want empty", got)
	}
	if got := mod.Health(context.Background()); got.Status != "" || got.Message != "" {
		t.Errorf("Health on nil WHealth = %#v, want empty", got)
	}
	if err := mod.Stop(context.Background()); err != nil {
		t.Errorf("Stop on nil WStop = %v, want nil", err)
	}

	var legacy _github_com_samcharles93_archie_core_internal_plugin_Plugin
	if got := legacy.Name(); got != "" {
		t.Errorf("Name on nil WName = %q, want empty", got)
	}
	if got := legacy.Version(); got != "" {
		t.Errorf("Version on nil WVersion = %q, want empty", got)
	}
}

var _ plugin.Module = testModule{}
