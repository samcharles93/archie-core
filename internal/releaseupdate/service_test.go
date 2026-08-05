package releaseupdate

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func TestServiceDefersOnlyTheVersionsShownToThatRecipient(t *testing.T) {
	catalog := &catalogStub{snapshot: Snapshot{Components: []Component{
		{ID: "gateway", Label: "THE GATEWAY", Installed: "v0.1.0", Available: "v0.1.1", Changelog: "- Clearer help"},
		{ID: "runtime", Label: "THE RUNTIME", Installed: "v0.1.0"},
	}}}
	service := Service{Catalog: catalog, StatePath: filepath.Join(t.TempDir(), "deferrals.json")}

	first, err := service.Check(context.Background(), 42)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(first.Available()); got != 1 {
		t.Fatalf("available before defer = %d, want 1", got)
	}
	if err := service.Defer(context.Background(), 42, first); err != nil {
		t.Fatal(err)
	}

	deferred, err := service.Check(context.Background(), 42)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(deferred.Available()); got != 0 {
		t.Fatalf("available after defer = %d, want 0", got)
	}
	if !deferred.Deferred {
		t.Error("Deferred = false, want true")
	}

	catalog.snapshot.Components[0].Available = "v0.1.2"
	newer, err := service.Check(context.Background(), 42)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(newer.Available()); got != 1 {
		t.Fatalf("available for newer version = %d, want 1", got)
	}
}

func TestServiceInstallForwardsProgressAndFailure(t *testing.T) {
	installer := &installerStub{}
	service := Service{Installer: installer}
	var progress []string
	if err := service.Install(context.Background(), func(message string) { progress = append(progress, message) }); err != nil {
		t.Fatal(err)
	}
	if !installer.called {
		t.Error("installer was not called")
	}
	if len(progress) != 1 || progress[0] != "Pulling release..." {
		t.Errorf("progress = %#v", progress)
	}
}

func TestNilServiceReturnsConfigurationErrors(t *testing.T) {
	var service *Service

	if _, err := service.Check(context.Background(), 0); err == nil {
		t.Fatal("nil service check returned nil error")
	}
	if err := service.Defer(context.Background(), 0, Snapshot{}); err == nil {
		t.Fatal("nil service defer returned nil error")
	}
	if err := service.Install(context.Background(), nil); err == nil {
		t.Fatal("nil service install returned nil error")
	}
	if service.CanInstall() {
		t.Fatal("nil service reports that installation is available")
	}
}

func TestFormatSnapshotReportsAvailableAndUnchangedComponents(t *testing.T) {
	got := FormatSnapshot(Snapshot{Components: []Component{
		{Label: "THE GATEWAY", Installed: "v1", Available: "v2", Changelog: "- safer updates"},
		{Label: "THE RUNTIME", Installed: "v1"},
	}})
	for _, want := range []string{"Archie has an update available", "v2 available", "safer updates", "No runtime changes"} {
		if !strings.Contains(got, want) {
			t.Errorf("formatted snapshot missing %q: %s", want, got)
		}
	}
}

func TestSameAvailableIgnoresMetadataAndDetectsVersionChanges(t *testing.T) {
	left := Snapshot{Components: []Component{{ID: "gateway", Installed: "v1", Available: "v2", Changelog: "old"}}}
	right := Snapshot{Components: []Component{{ID: "gateway", Installed: "v1", Available: "v2", Changelog: "new"}}}
	if !SameAvailable(left, right) {
		t.Fatal("matching available versions should be equal")
	}
	right.Components[0].Available = "v3"
	if SameAvailable(left, right) {
		t.Fatal("changed available versions should not be equal")
	}
}

type catalogStub struct{ snapshot Snapshot }

func (s *catalogStub) Check(context.Context) (Snapshot, error) { return s.snapshot, nil }

type installerStub struct{ called bool }

func (s *installerStub) Install(_ context.Context, progress func(string)) error {
	s.called = true
	progress("Pulling release...")
	return nil
}
