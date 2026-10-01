package plugin_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/samcharles93/archie-core/internal/plugin"
)

// The running-state operations a live plugin load needs: Add puts one new
// module beside the running set, Replace swaps one module's implementation,
// and neither disturbs a module it is not touching.
// docs/prds/plugin-settings-live.md ("The three registries").

func TestHostAddStartsANewModuleWhileRunning(t *testing.T) {
	t.Parallel()

	events := &eventLog{}
	host := plugin.NewHost()
	base := &fakeModule{manifest: validManifest("base"), events: events}
	if err := host.Register(base); err != nil {
		t.Fatal(err)
	}
	if err := host.Start(context.Background()); err != nil {
		t.Fatal(err)
	}

	added := &fakeModule{manifest: validManifest("late", "base"), events: events}
	if err := host.Add(context.Background(), added); err != nil {
		t.Fatalf("Add() error = %v", err)
	}
	if !host.Has("late") {
		t.Fatal("Add() did not register the module")
	}
	if got, want := events.snapshot(), []string{"start:base", "start:late"}; !slices.Equal(got, want) {
		t.Fatalf("lifecycle events = %v, want %v", got, want)
	}
	// The already-running set is stopped after the added module, since the
	// added module came later.
	if err := host.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got, want := events.snapshot(), []string{"start:base", "start:late", "stop:late", "stop:base"}; !slices.Equal(got, want) {
		t.Fatalf("lifecycle events = %v, want %v", got, want)
	}
}

func TestHostAddRefusesDuplicateAndUnknownDependency(t *testing.T) {
	t.Parallel()

	host := plugin.NewHost()
	if err := host.Register(&fakeModule{manifest: validManifest("base")}); err != nil {
		t.Fatal(err)
	}
	if err := host.Start(context.Background()); err != nil {
		t.Fatal(err)
	}

	if err := host.Add(context.Background(), &fakeModule{manifest: validManifest("base")}); err == nil {
		t.Fatal("Add() of a duplicate id succeeded")
	}
	if err := host.Add(context.Background(), &fakeModule{manifest: validManifest("dependent", "missing")}); err == nil {
		t.Fatal("Add() with an unregistered dependency succeeded")
	}
	if host.Has("dependent") {
		t.Error("a refused Add() left the module registered")
	}
}

func TestHostAddRollsBackItsOwnStartFailure(t *testing.T) {
	t.Parallel()

	events := &eventLog{}
	host := plugin.NewHost()
	if err := host.Register(&fakeModule{manifest: validManifest("base"), events: events}); err != nil {
		t.Fatal(err)
	}
	if err := host.Start(context.Background()); err != nil {
		t.Fatal(err)
	}

	broken := &fakeModule{manifest: validManifest("late"), events: events, startErr: errors.New("boom")}
	if err := host.Add(context.Background(), broken); err == nil {
		t.Fatal("Add() of a module whose Start fails succeeded")
	}
	if host.Has("late") {
		t.Error("Add() left a module whose Start failed registered")
	}
	if got, want := events.snapshot(), []string{"start:base", "start:late"}; !slices.Equal(got, want) {
		t.Fatalf("lifecycle events = %v, want the base module untouched by the failed add: %v", got, want)
	}
}

func TestHostReplaceStopsTheOldAndStartsTheNew(t *testing.T) {
	t.Parallel()

	events := &eventLog{}
	host := plugin.NewHost()
	oldManifest := validManifest("module")
	oldManifest.Version = "1.0.0"
	if err := host.Register(&fakeModule{manifest: oldManifest, events: events}); err != nil {
		t.Fatal(err)
	}
	if err := host.Start(context.Background()); err != nil {
		t.Fatal(err)
	}

	newManifest := validManifest("module")
	newManifest.Version = "2.0.0"
	if err := host.Replace(context.Background(), &fakeModule{manifest: newManifest, events: events}); err != nil {
		t.Fatalf("Replace() error = %v", err)
	}
	if got, want := events.snapshot(), []string{"start:module", "stop:module", "start:module"}; !slices.Equal(got, want) {
		t.Fatalf("lifecycle events = %v, want %v", got, want)
	}
	manifests := host.Manifests()
	if len(manifests) != 1 || manifests[0].Version != "2.0.0" {
		t.Fatalf("manifests after replace = %+v, want the new 2.0.0", manifests)
	}
}

func TestHostReplaceRestartsTheOldWhenTheNewStartFails(t *testing.T) {
	t.Parallel()

	events := &eventLog{}
	host := plugin.NewHost()
	if err := host.Register(&fakeModule{manifest: validManifest("module"), events: events}); err != nil {
		t.Fatal(err)
	}
	if err := host.Start(context.Background()); err != nil {
		t.Fatal(err)
	}

	broken := &fakeModule{manifest: validManifest("module"), events: events, startErr: errors.New("boom")}
	err := host.Replace(context.Background(), broken)
	if err == nil {
		t.Fatal("Replace() with a failing new module succeeded")
	}
	if got, want := events.snapshot(), []string{"start:module", "stop:module", "start:module", "start:module"}; !slices.Equal(got, want) {
		t.Fatalf("lifecycle events = %v, want the old module restarted after the failed start: %v", got, want)
	}
	if !host.Has("module") {
		t.Error("a refused Replace() unregistered the module")
	}
}

func TestHostReplaceRefusesAModuleARunningDependentNeeds(t *testing.T) {
	t.Parallel()

	events := &eventLog{}
	host := plugin.NewHost()
	if err := host.Register(&fakeModule{manifest: validManifest("base"), events: events}); err != nil {
		t.Fatal(err)
	}
	if err := host.Register(&fakeModule{manifest: validManifest("dependent", "base"), events: events}); err != nil {
		t.Fatal(err)
	}
	if err := host.Start(context.Background()); err != nil {
		t.Fatal(err)
	}

	err := host.Replace(context.Background(), &fakeModule{manifest: validManifest("base"), events: events})
	if err == nil || !strings.Contains(err.Error(), "dependent") {
		t.Fatalf("Replace() of a depended-on module = %v, want a refusal naming the dependent", err)
	}
	if got, want := events.snapshot(), []string{"start:base", "start:dependent"}; !slices.Equal(got, want) {
		t.Fatalf("lifecycle events = %v, want the dependent left running: %v", got, want)
	}
}

func TestHostReplaceRefusesAnUnregisteredModule(t *testing.T) {
	t.Parallel()

	host := plugin.NewHost()
	if err := host.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := host.Replace(context.Background(), &fakeModule{manifest: validManifest("absent")}); err == nil {
		t.Fatal("Replace() of an unregistered module succeeded")
	}
}

func TestHostAddBeforeStartBehavesLikeRegister(t *testing.T) {
	t.Parallel()

	events := &eventLog{}
	host := plugin.NewHost()
	if err := host.Add(context.Background(), &fakeModule{manifest: validManifest("early"), events: events}); err != nil {
		t.Fatalf("Add() before Start error = %v", err)
	}
	if err := host.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got, want := events.snapshot(), []string{"start:early"}; !slices.Equal(got, want) {
		t.Fatalf("lifecycle events = %v, want the module started by Start(): %v", got, want)
	}
}
