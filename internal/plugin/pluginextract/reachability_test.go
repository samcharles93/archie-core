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
// the Host/Module/Manifest/Permission/CapabilityKind types and the
// NewHost/AdaptLegacy/HostAPIVersion identifiers that plugin.go's LoadDir
// exposes to interpreted daemon plugins were absent from the table. A daemon
// plugin written against the capability-host API could not resolve them,
// silently.
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
func (testModule) Stop(context.Context) error  { return nil }

// The former TestWrapperNilGuardsPreserved was retired. It pinned hand-added
// nil guards that every regeneration silently dropped, and the behaviour it
// protected was a lie: a nil WStop reported a clean shutdown for a module that
// never ran one. Such a wrapper is now refused in yaegiutil.Resolve, before it
// can reach a call site.

var _ plugin.Module = testModule{}
