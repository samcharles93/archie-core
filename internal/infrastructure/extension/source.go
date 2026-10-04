package extension

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"

	"github.com/samcharles93/archie-core/internal/domain/storepkg"
	"github.com/samcharles93/archie-core/internal/infrastructure/controlplanerpc"
)

// Packages reads installed packages, as *staterpc.Client does.
type Packages interface {
	ListInstalled(ctx context.Context) ([]storepkg.Installed, error)
	GetInstalled(ctx context.Context, name string) (storepkg.Installed, error)
}

// Source is what an extension supervisor reads: the extension-settings
// resource and the installed packages.
type Source struct {
	// Query reads a control-plane resource, as *controlplanerpc.Client does.
	Query    controlplanerpc.ResourceReader
	Packages Packages
}

// Enabled reads the extension-settings resource, keyed by extension name, and
// its version.
func (s Source) Enabled(ctx context.Context) (map[string]controlplanerpc.ExtensionSetting, int64, error) {
	byName := make(map[string]controlplanerpc.ExtensionSetting)
	version, _, err := s.Query.Query(ctx, controlplanerpc.ExtensionSettingsKind, func(value []byte) error {
		var doc controlplanerpc.ExtensionSettings
		if err := json.Unmarshal(value, &doc); err != nil {
			return err
		}
		for _, setting := range doc.Extensions {
			byName[setting.Name] = setting
		}
		return nil
	})
	if err != nil {
		return nil, 0, fmt.Errorf("read extension settings: %w", err)
	}
	return byName, version, nil
}

// Materialize writes the package's extension binary under cacheDir, keyed by
// package digest, and returns its path and sha256. A binary already there for
// this digest is reused.
func Materialize(ctx context.Context, packages Packages, cacheDir string, pkg storepkg.Installed, file string) (string, string, error) {
	full, err := packages.GetInstalled(ctx, pkg.Name)
	if err != nil {
		return "", "", fmt.Errorf("get installed package: %w", err)
	}
	files, err := storepkg.FilesFromLayer(full.Layer)
	if err != nil {
		return "", "", err
	}
	content, ok := files[file]
	if !ok {
		return "", "", fmt.Errorf("package layer has no %q", file)
	}
	sum := sha256.Sum256(content)
	dir := filepath.Join(cacheDir, pkg.Name, hex.EncodeToString(sum[:8]))
	target := filepath.Join(dir, path.Base(file))
	if existing, err := os.ReadFile(target); err == nil && sha256.Sum256(existing) == sum {
		return target, hex.EncodeToString(sum[:]), nil
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", "", err
	}
	tmp, err := os.CreateTemp(dir, ".extract-*")
	if err != nil {
		return "", "", err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.Write(content); err != nil {
		_ = tmp.Close()
		return "", "", err
	}
	if err := tmp.Chmod(0o700); err != nil {
		_ = tmp.Close()
		return "", "", err
	}
	if err := tmp.Close(); err != nil {
		return "", "", err
	}
	if err := os.Rename(tmp.Name(), target); err != nil {
		return "", "", err
	}
	return target, hex.EncodeToString(sum[:]), nil
}
