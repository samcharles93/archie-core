package releaseupdate

import (
	"context"
	"path/filepath"
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

type catalogStub struct{ snapshot Snapshot }

func (s *catalogStub) Check(context.Context) (Snapshot, error) { return s.snapshot, nil }

type installerStub struct{ called bool }

func (s *installerStub) Install(_ context.Context, progress func(string)) error {
	s.called = true
	progress("Pulling release...")
	return nil
}
