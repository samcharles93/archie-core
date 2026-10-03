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

// Installer applies an approved update. The restart outcome is reported
// later as a Report.
type Installer interface {
	Install(context.Context, Snapshot, InstallMeta, func(string)) (Result, error)
}

type Component struct {
	ID        string
	Label     string
	Installed string
	Available string
	Changelog string
	// InstallType and Reference describe how this component is deployed, filled
	// by Service.Enrich.
	InstallType string
	Reference   string
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

	// InstallType must be a stamped install type, or Install refuses to run.
	InstallType string

	// Enrich returns a component's install type and reference. Nil or an empty
	// result leaves the check command's values.
	Enrich func(componentID string) (installType, reference string)

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
	if s.Enrich != nil {
		for index := range snapshot.Components {
			component := &snapshot.Components[index]
			if installType, reference := s.Enrich(component.ID); installType != "" {
				component.InstallType = installType
				component.Reference = reference
			}
		}
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
		if component.Available != "" && deferred[component.ID] == component.Available {
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

// ErrInstallInProgress is returned when another install is running.
var ErrInstallInProgress = errors.New("an update is already in progress")

// installTimeout bounds an install after it has been deliberately detached
// from the caller's context (see below) -- otherwise a hung installer
// script would hold the in-progress lock forever.
const installTimeout = 30 * time.Minute

// Install runs the Installer under installTimeout, detached from ctx so a
// cancelled caller cannot kill an install mid-copy.
func (s *Service) Install(ctx context.Context, snapshot Snapshot, meta InstallMeta, progress func(string)) (Result, error) {
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
	return s.Installer.Install(installCtx, snapshot, meta, progress)
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

// writeFileAtomic writes data to path through a synced temp file and rename.
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
