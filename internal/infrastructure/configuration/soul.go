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

// maxSoulReadBytes bounds how much of an existing SOUL file the upgrade check
// reads. A SOUL is at most 8 KiB once validated, so a larger file cannot be a
// shipped template and is treated as a user edit without reading it in full.
const maxSoulReadBytes = 64 << 10

// SoulSeedAction is what SeedSoul did to a SOUL file.
type SoulSeedAction string

const (
	// SoulCreated wrote the starter because no file existed.
	SoulCreated SoulSeedAction = "created"
	// SoulUpgraded replaced a file that was still exactly a shipped template.
	SoulUpgraded SoulSeedAction = "upgraded"
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

// ConfigDir resolves the configuration directory for a selected config path:
// the path itself when it is a directory, otherwise the directory containing
// the selected file. It is how the SOUL file, which sits beside the config,
// follows an explicitly selected `-config` file as well as the default
// directory.
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

// SeedSoul writes the starter SOUL into the configuration directory when no
// file exists, upgrades a file whose content is still exactly a shipped
// template, and leaves anything else untouched. It never overwrites a user
// edit, including an empty file.
//
// The check is content, not provenance, because the only durable copy of a
// file-owned SOUL is the file itself: an operator who edited it by hand leaves
// no writer identity to consult, so a body that differs from every shipped
// template in any way is treated as theirs.
//
// The caller owns the decision to seed at all. A failure is returned rather
// than suppressed, because whether a missing starter is fatal belongs to the
// caller that needs the file, not to the seeder.
func SeedSoul(configPath string, doc agent.SoulDocument) (SoulSeedResult, error) {
	dir := ConfigDir(configPath)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return SoulSeedResult{}, fmt.Errorf("soul: create config dir %s: %w", dir, err)
	}
	return seedSoulFile(filepath.Join(dir, SoulFilename), doc)
}

// seedSoulFile applies the seed/upgrade/preserve decision to one path.
func seedSoulFile(path string, doc agent.SoulDocument) (SoulSeedResult, error) {
	info, err := os.Lstat(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		if err := createSoulExclusive(path, doc.Default); err != nil {
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
	return classifyExistingSoul(path, info, doc)
}

// classifyExistingSoul decides what to do with a SOUL file that is already on
// disk: leave the build's current document, upgrade an untouched shipped
// template, and preserve everything else.
func classifyExistingSoul(path string, info fs.FileInfo, doc agent.SoulDocument) (SoulSeedResult, error) {
	// A symlink or any other non-regular file was not created by this seeder.
	// Replacing it (an upgrade writes by rename) would destroy a user-managed
	// indirection, so it is preserved.
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

	switch doc.Match(string(content)) {
	case agent.SoulCurrent:
		return SoulSeedResult{Path: path, Action: SoulCurrent}, nil
	case agent.SoulSuperseded:
		if err := replaceSoul(path, doc.Default); err != nil {
			return SoulSeedResult{Path: path}, err
		}
		return SoulSeedResult{Path: path, Action: SoulUpgraded}, nil
	default:
		return SoulSeedResult{Path: path, Action: SoulPreserved}, nil
	}
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

// replaceSoul swaps in the current document atomically, so a reader never
// observes a half-written SOUL and an interrupted upgrade leaves the previous
// content intact.
func replaceSoul(path, content string) error {
	dir := filepath.Dir(path)
	temp, err := os.CreateTemp(dir, ".SOUL.md.*")
	if err != nil {
		return fmt.Errorf("soul: create temp beside %s: %w", path, err)
	}
	tempName := temp.Name()
	// Best-effort: a successful rename has already moved the file away.
	defer func() { _ = os.Remove(tempName) }()

	if err := temp.Chmod(0o600); err != nil {
		_ = temp.Close()
		return fmt.Errorf("soul: chmod %s: %w", tempName, err)
	}
	if _, err := temp.WriteString(content); err != nil {
		_ = temp.Close()
		return fmt.Errorf("soul: write %s: %w", tempName, err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("soul: close %s: %w", tempName, err)
	}
	if err := os.Rename(tempName, path); err != nil {
		return fmt.Errorf("soul: replace %s: %w", path, err)
	}
	return nil
}
