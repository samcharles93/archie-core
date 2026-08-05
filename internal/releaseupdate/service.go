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
)

// Catalog discovers the currently installed and newest available releases.
// Deployment-specific adapters (a package manager, image registry, or custom
// release feed) implement it outside the chat gateway.
type Catalog interface {
	Check(context.Context) (Snapshot, error)
}

// Installer applies an approved update. It reports only work it has actually
// started; callers remain responsible for telling users the final result.
type Installer interface {
	Install(context.Context, func(string)) error
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
	mu        sync.Mutex
}

func (s *Service) Check(ctx context.Context, recipient int64) (Snapshot, error) {
	if s.Catalog == nil {
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
	if s.Catalog == nil {
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

func (s *Service) Install(ctx context.Context, progress func(string)) error {
	if s.Installer == nil {
		return errors.New("update installation is not configured")
	}
	return s.Installer.Install(ctx, progress)
}

func (s *Service) CanInstall() bool { return s.Installer != nil }

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
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create update state directory: %w", err)
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".update-deferrals-*")
	if err != nil {
		return fmt.Errorf("create update state: %w", err)
	}
	tempPath := file.Name()
	defer func() { _ = os.Remove(tempPath) }()
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return fmt.Errorf("protect update state: %w", err)
	}
	if err := json.NewEncoder(file).Encode(state); err != nil {
		_ = file.Close()
		return fmt.Errorf("encode update deferrals: %w", err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return fmt.Errorf("sync update deferrals: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close update state: %w", err)
	}
	if err := os.Rename(tempPath, path); err != nil {
		return fmt.Errorf("replace update state: %w", err)
	}
	return nil
}
