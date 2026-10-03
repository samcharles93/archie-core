package configuration

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/samcharles93/archie-core/internal/domain/agent"
)

// SoulFilename is the SOUL document's file name in the configuration
// directory. The root agent owns it directly; per-agent directories are a
// later concern and are not invented here.
const SoulFilename = "SOUL.md"

// maxSoulReadBytes bounds how much of an existing SOUL file the seed check
// reads. A SOUL is at most 8 KiB once validated, so a larger file cannot be a
// shipped template and is treated as a user edit without reading it in full.
const maxSoulReadBytes = 64 << 10

// SoulSeedAction is what SeedSoul did to a SOUL file.
type SoulSeedAction string

const (
	// SoulCreated wrote the starter because no file existed.
	SoulCreated SoulSeedAction = "created"
	// SoulCurrent found the build's current document already in place.
	SoulCurrent SoulSeedAction = "current"
	// SoulPreserved left a user edit (or an unreadable/oversized file) alone.
	SoulPreserved SoulSeedAction = "preserved"
)

// SoulSeedResult records the file SeedSoul acted on and what it did, so boot
// can log the decision without re-reading the file.
type SoulSeedResult struct {
	Path   string
	Action SoulSeedAction
}

// ConfigDir returns configPath if it is a directory, else its parent.
func ConfigDir(configPath string) string {
	if info, err := os.Stat(configPath); err == nil && info.IsDir() {
		return configPath
	}
	return filepath.Dir(configPath)
}

// SoulPath returns the SOUL file path for a selected config path.
func SoulPath(configPath string) string {
	return filepath.Join(ConfigDir(configPath), SoulFilename)
}

// SeedSoul writes the starter SOUL file when none exists. It never
// overwrites an existing file.
func SeedSoul(configPath, shipped string) (SoulSeedResult, error) {
	dir := ConfigDir(configPath)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return SoulSeedResult{}, fmt.Errorf("soul: create config dir %s: %w", dir, err)
	}
	return seedSoulFile(filepath.Join(dir, SoulFilename), shipped)
}

// seedSoulFile applies the seed/preserve decision to one path.
func seedSoulFile(path, shipped string) (SoulSeedResult, error) {
	info, err := os.Lstat(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		if err := createSoulExclusive(path, shipped); err != nil {
			if !errors.Is(err, fs.ErrExist) {
				return SoulSeedResult{Path: path}, err
			}
			// Another process (the daemon and the standalone Gateway can both
			// seed) created the file between the check and the create. Classify
			// what is now there rather than reporting a failure for a file that
			// is in fact present.
			info, err = os.Lstat(path)
			if err != nil {
				return SoulSeedResult{Path: path}, fmt.Errorf("soul: stat %s: %w", path, err)
			}
		} else {
			return SoulSeedResult{Path: path, Action: SoulCreated}, nil
		}
	case err != nil:
		return SoulSeedResult{Path: path}, fmt.Errorf("soul: stat %s: %w", path, err)
	}
	return classifyExistingSoul(path, info, shipped)
}

// classifyExistingSoul decides what to do with a SOUL file that is already on
// disk: leave the build's current document in place and preserve everything
// else.
func classifyExistingSoul(path string, info fs.FileInfo, shipped string) (SoulSeedResult, error) {
	// A symlink or any other non-regular file was not created by this seeder.
	// A rewrite would destroy a user-managed indirection, so it is preserved.
	if !info.Mode().IsRegular() {
		return SoulSeedResult{Path: path, Action: SoulPreserved}, nil
	}
	if info.Size() > maxSoulReadBytes {
		return SoulSeedResult{Path: path, Action: SoulPreserved}, nil
	}

	content, err := os.ReadFile(path)
	if err != nil {
		return SoulSeedResult{Path: path}, fmt.Errorf("soul: read %s: %w", path, err)
	}

	if agent.SoulMatchesShipped(string(content), shipped) {
		return SoulSeedResult{Path: path, Action: SoulCurrent}, nil
	}
	return SoulSeedResult{Path: path, Action: SoulPreserved}, nil
}

// createSoulExclusive writes the starter without ever replacing a file that
// appeared since the caller decided the file was absent.
func createSoulExclusive(path, content string) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("soul: create %s: %w", path, err)
	}
	if _, err := file.WriteString(content); err != nil {
		_ = file.Close()
		return fmt.Errorf("soul: write %s: %w", path, err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("soul: close %s: %w", path, err)
	}
	return nil
}
