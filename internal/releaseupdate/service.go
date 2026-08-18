// Package releaseupdate provides deployment-neutral release discovery,
// per-user deferrals, and installation orchestration.
package releaseupdate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/samcharles93/archie-core/internal/installtype"
)

// Catalog discovers the currently installed and newest available releases.
// Deployment-specific adapters (a package manager, image registry, or custom
// release feed) implement it outside the chat gateway.
type Catalog interface {
	Check(context.Context) (Snapshot, error)
}

// Installer applies an approved update. It reports only work it has actually
// started; callers remain responsible for telling users the final result.
// The returned Result covers only the synchronous phase (fetch/build/
// install) -- whether a subsequent restart actually came up healthy is
// reported later, out of band, via a Report (see pending_report.go).
type Installer interface {
	Install(context.Context, InstallMeta, func(string)) (Result, error)
}

type Component struct {
	ID        string
	Label     string
	Installed string
	Available string
	Changelog string
}

type Snapshot struct {
	Components []Component
	Deferred   bool
}

func (s Snapshot) Available() []Component {
	available := make([]Component, 0, len(s.Components))
	for _, component := range s.Components {
		if component.Available != "" && component.Available != component.Installed {
			available = append(available, component)
		}
	}
	return available
}

// SameAvailable reports whether two snapshots offer the same component
// versions. Metadata such as changelogs may change without changing the
// release the operator is approving, so only installable versions matter.
func SameAvailable(left, right Snapshot) bool {
	leftAvailable, rightAvailable := left.Available(), right.Available()
	if len(leftAvailable) != len(rightAvailable) {
		return false
	}
	versions := make(map[string]string, len(leftAvailable))
	for _, component := range leftAvailable {
		versions[component.ID] = component.Available
	}
	for _, component := range rightAvailable {
		if versions[component.ID] != component.Available {
			return false
		}
	}
	return true
}

// Service persists a user's decision to defer an exact available version. A
// later version is deliberately shown again rather than being silently hidden.
type Service struct {
	Catalog   Catalog
	Installer Installer
	StatePath string

	// InstallType gates Install: it must be a value the release pipeline
	// actually stamps (see package installtype), never "" or
	// installtype.Unknown, or Install refuses to run the configured
	// Installer at all. The composition root wires this from
	// installtype.Type() -- Service itself never reads that package
	// directly, so a test can exercise every InstallType without touching
	// process-wide state.
	InstallType string

	mu         sync.Mutex
	installing bool
}

func (s *Service) Check(ctx context.Context, recipient int64) (Snapshot, error) {
	if s == nil || s.Catalog == nil {
		return Snapshot{}, errors.New("update discovery is not configured")
	}
	snapshot, err := s.Catalog.Check(ctx)
	if err != nil {
		return Snapshot{}, fmt.Errorf("check for updates: %w", err)
	}
	if s.StatePath == "" {
		return snapshot, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	state, err := loadDeferrals(s.StatePath)
	if err != nil {
		return Snapshot{}, err
	}
	deferred := state[strconv.FormatInt(recipient, 10)]
	for index := range snapshot.Components {
		component := &snapshot.Components[index]
		if deferred[component.ID] == component.Available {
			component.Available = ""
			snapshot.Deferred = true
		}
	}
	return snapshot, nil
}

// Defer records only the exact versions the caller displayed. It refuses a
// changed catalog rather than silently suppressing a release the user never saw.
func (s *Service) Defer(ctx context.Context, recipient int64, expected Snapshot) error {
	if s == nil || s.Catalog == nil {
		return errors.New("update discovery is not configured")
	}
	if s.StatePath == "" {
		return errors.New("update deferral storage is not configured")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	snapshot, err := s.Catalog.Check(ctx)
	if err != nil {
		return fmt.Errorf("check updates before deferring: %w", err)
	}
	state, err := loadDeferrals(s.StatePath)
	if err != nil {
		return err
	}
	key := strconv.FormatInt(recipient, 10)
	if state[key] == nil {
		state[key] = make(map[string]string)
	}
	if !SameAvailable(snapshot, expected) {
		return errors.New("available releases changed; check again")
	}
	for _, component := range expected.Available() {
		state[key][component.ID] = component.Available
	}
	return saveDeferrals(s.StatePath, state)
}

// ErrUnknownInstallType is returned by Install when InstallType is unset or
// installtype.Unknown -- there is no way to know, without it, whether the
// configured Installer is even the right kind of update for how this
// instance was actually deployed.
var ErrUnknownInstallType = errors.New("refusing to install: install type is unknown")

// ErrInstallInProgress is returned by Install when another install is
// already running. Telegram and the web dashboard each keep their own
// local "already in progress" flag for a quick UI response, but both are
// wired to the same Service in production -- this is the one guard that
// actually sees both, since a caller-local bool would let one adapter start
// a second install while the other's is still copying binaries.
var ErrInstallInProgress = errors.New("an update is already in progress")

// installTimeout bounds an install after it has been deliberately detached
// from the caller's context (see below) -- otherwise a hung installer
// script would hold the in-progress lock forever.
const installTimeout = 30 * time.Minute

// Install runs the configured Installer. The context it hands the
// Installer is intentionally NOT ctx: ctx belongs to whatever triggered the
// update (a chat message handler cancelled by /restart, an HTTP request
// cancelled by a client disconnect), and exec.CommandContext SIGKILLs its
// child the instant that context is cancelled. The install script backs up
// and overwrites the live binaries with plain, non-atomic copies -- a kill
// mid-copy can leave the daemon unable to start, with the one thing that
// could roll that back (the watchdog) never launched because the script
// never reached that line. Detaching the install from ctx, bounded by
// installTimeout instead, is what actually makes the caller's own
// lifecycle safe to interrupt without corrupting an in-flight update.
func (s *Service) Install(ctx context.Context, meta InstallMeta, progress func(string)) (Result, error) {
	if s == nil || s.Installer == nil {
		return Result{}, errors.New("update installation is not configured")
	}
	if s.InstallType == "" || s.InstallType == installtype.Unknown {
		return Result{}, ErrUnknownInstallType
	}
	s.mu.Lock()
	if s.installing {
		s.mu.Unlock()
		return Result{}, ErrInstallInProgress
	}
	s.installing = true
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.installing = false
		s.mu.Unlock()
	}()

	installCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), installTimeout)
	defer cancel()
	return s.Installer.Install(installCtx, meta, progress)
}

func (s *Service) CanInstall() bool { return s != nil && s.Installer != nil }

type deferrals map[string]map[string]string

func loadDeferrals(path string) (deferrals, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return make(deferrals), nil
	}
	if err != nil {
		return nil, fmt.Errorf("read update deferrals: %w", err)
	}
	var state deferrals
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("decode update deferrals: %w", err)
	}
	return state, nil
}

func saveDeferrals(path string, state deferrals) error {
	data, err := json.Marshal(state)
	if err != nil {
		return fmt.Errorf("encode update deferrals: %w", err)
	}
	return writeFileAtomic(path, data)
}

// writeFileAtomic writes data to path via a temp file in the same
// directory, synced and renamed into place, so a reader never observes a
// partially written file. Shared by saveDeferrals and WritePendingReport --
// both are small JSON state files under the same operational directory
// (cfg.WorkDir) with the same durability requirement.
func writeFileAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create state directory: %w", err)
	}
	file, err := os.CreateTemp(dir, ".update-state-*")
	if err != nil {
		return fmt.Errorf("create temp state file: %w", err)
	}
	tempPath := file.Name()
	defer func() { _ = os.Remove(tempPath) }()
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return fmt.Errorf("protect state file: %w", err)
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return fmt.Errorf("write state file: %w", err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return fmt.Errorf("sync state file: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close state file: %w", err)
	}
	if err := os.Rename(tempPath, path); err != nil {
		return fmt.Errorf("replace state file: %w", err)
	}
	return nil
}
