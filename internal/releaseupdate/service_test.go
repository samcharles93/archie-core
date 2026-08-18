package releaseupdate

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/samcharles93/archie-core/internal/installtype"
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
	service := Service{Installer: installer, InstallType: "binary"}
	var progress []string
	meta := InstallMeta{Channel: "telegram", ChatID: 42}
	result, err := service.Install(context.Background(), meta, func(message string) { progress = append(progress, message) })
	if err != nil {
		t.Fatal(err)
	}
	if !installer.called {
		t.Error("installer was not called")
	}
	if installer.gotMeta != meta {
		t.Errorf("installer received meta = %#v, want %#v", installer.gotMeta, meta)
	}
	if len(progress) != 1 || progress[0] != "Pulling release..." {
		t.Errorf("progress = %#v", progress)
	}
	if result.Installed["daemon"] != "1.1.0" {
		t.Errorf("result = %#v", result)
	}
}

// TestServiceInstallRefusesAnUnknownInstallType is the fail-closed guarantee
// that makes InstallType worth having: a binary the release pipeline never
// stamped (or a caller that never wired InstallType at all) must not run
// whatever install command happens to be configured -- there is no way to
// know, at that point, whether it is even the right kind of update for how
// this instance was actually deployed.
func TestServiceInstallRefusesAnUnknownInstallType(t *testing.T) {
	tests := []struct {
		name        string
		installType string
	}{
		{"unset InstallType", ""},
		{"explicit unknown", installtype.Unknown},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			installer := &installerStub{}
			service := Service{Installer: installer, InstallType: tt.installType}

			_, err := service.Install(context.Background(), InstallMeta{}, func(string) {})

			if err == nil {
				t.Fatal("Install() returned nil error, want a refusal")
			}
			if installer.called {
				t.Error("installer was called despite an unknown install type")
			}
		})
	}
}

// TestServiceInstallSurvivesCallerContextCancellation: the caller's context
// belongs to the request/handler that triggered the update (a Telegram
// message handler cancelled by /restart, or an HTTP request cancelled by a
// client disconnect) -- it must not reach into the install child process.
// Killing that process mid-run can leave binaries half-overwritten with no
// rollback ever attempted, which is exactly the failure mode this feature
// exists to prevent.
func TestServiceInstallSurvivesCallerContextCancellation(t *testing.T) {
	installer := &blockingInstallerStub{unblock: make(chan struct{}), started: make(chan struct{})}
	service := Service{Installer: installer, InstallType: "binary"}
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() {
		_, err := service.Install(ctx, InstallMeta{}, func(string) {})
		done <- err
	}()

	<-installer.started
	cancel() // simulate the triggering request/handler being torn down mid-install

	select {
	case <-installer.gotCancelled:
		t.Fatal("installer observed its context as cancelled after only the caller's context was cancelled")
	case <-time.After(50 * time.Millisecond):
	}
	close(installer.unblock)

	if err := <-done; err != nil {
		t.Fatalf("Install() error = %v, want nil", err)
	}
}

// TestServiceInstallRefusesConcurrentInstalls: Telegram and the web
// dashboard are two different callers of the same Service, each with its
// own local "already in progress" flag -- neither knows about the other.
// The authoritative guard against two installs racing the same binaries
// and .prev backups has to live here, not duplicated per caller.
func TestServiceInstallRefusesConcurrentInstalls(t *testing.T) {
	installer := &blockingInstallerStub{unblock: make(chan struct{}), started: make(chan struct{})}
	service := Service{Installer: installer, InstallType: "binary"}

	done := make(chan error, 1)
	go func() {
		_, err := service.Install(context.Background(), InstallMeta{}, func(string) {})
		done <- err
	}()
	<-installer.started

	if _, err := service.Install(context.Background(), InstallMeta{}, func(string) {}); !errors.Is(err, ErrInstallInProgress) {
		t.Fatalf("second Install() error = %v, want ErrInstallInProgress", err)
	}

	close(installer.unblock)
	if err := <-done; err != nil {
		t.Fatalf("first Install() error = %v, want nil", err)
	}
	if installer.calls != 1 {
		t.Fatalf("installer.calls = %d, want exactly 1", installer.calls)
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
	if _, err := service.Install(context.Background(), InstallMeta{}, nil); err == nil {
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

// blockingInstallerStub lets a test hold Install() open until it chooses to
// release it, and records whether the context it was given ever reported
// cancellation.
type blockingInstallerStub struct {
	unblock      chan struct{}
	started      chan struct{}
	gotCancelled chan struct{}
	calls        int
}

func (s *blockingInstallerStub) Install(ctx context.Context, _ InstallMeta, _ func(string)) (Result, error) {
	s.calls++
	close(s.started)
	if s.gotCancelled == nil {
		s.gotCancelled = make(chan struct{})
	}
	go func() {
		<-ctx.Done()
		close(s.gotCancelled)
	}()
	<-s.unblock
	return Result{}, nil
}

type installerStub struct {
	called  bool
	gotMeta InstallMeta
}

func (s *installerStub) Install(_ context.Context, meta InstallMeta, progress func(string)) (Result, error) {
	s.called = true
	s.gotMeta = meta
	progress("Pulling release...")
	return Result{Installed: map[string]string{"daemon": "1.1.0"}}, nil
}
