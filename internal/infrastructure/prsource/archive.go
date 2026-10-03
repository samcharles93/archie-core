package prsource

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// extractTarGz extracts a .tar.gz into destDir, stripping a common top-level
// directory. It skips .git and refuses paths escaping destDir.
func extractTarGz(r io.Reader, destDir string) error {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return fmt.Errorf("prsource: open archive: %w", err)
	}
	defer gz.Close()

	dest, err := filepath.Abs(destDir)
	if err != nil {
		return fmt.Errorf("prsource: resolve destination directory: %w", err)
	}

	tr := tar.NewReader(gz)
	prefix, entries, err := planExtraction(tr)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if err := writeEntry(dest, prefix, e); err != nil {
			return err
		}
	}
	return nil
}

// tarEntry is one archive entry buffered in memory so the wrapper-prefix
// decision (which needs to see every name first) doesn't require reading
// the tar stream twice.
type tarEntry struct {
	header *tar.Header
	data   []byte
}

// planExtraction reads every entry and returns the common leading path
// component to strip, if every entry shares one.
func planExtraction(tr *tar.Reader) (prefix string, entries []tarEntry, err error) {
	var commonPrefix string
	first := true
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return "", nil, fmt.Errorf("prsource: read archive entry: %w", err)
		}
		name := path.Clean(filepath.ToSlash(hdr.Name))
		if name == "." {
			continue
		}
		top, _, hasSlash := strings.Cut(name, "/")
		switch {
		case !hasSlash && hdr.Typeflag != tar.TypeDir:
			// A top-level file means there is no wrapper directory.
			commonPrefix = ""
			first = false
		case first:
			commonPrefix = top
			first = false
		case commonPrefix != "" && top != commonPrefix:
			commonPrefix = ""
		}

		var data []byte
		if hdr.Typeflag == tar.TypeReg {
			data, err = io.ReadAll(tr)
			if err != nil {
				return "", nil, fmt.Errorf("prsource: read archive entry %q: %w", name, err)
			}
		}
		entries = append(entries, tarEntry{header: hdr, data: data})
	}
	return commonPrefix, entries, nil
}

// writeEntry writes one directory or regular file under destDir; other
// entry types are skipped.
func writeEntry(destDir, prefix string, e tarEntry) error {
	name := path.Clean(filepath.ToSlash(e.header.Name))
	if name == "." || name == prefix {
		return nil
	}
	if prefix != "" {
		cut, ok := strings.CutPrefix(name, prefix+"/")
		if !ok {
			return nil
		}
		name = cut
	}
	if name == "" || name == "." {
		return nil
	}
	if isGitPath(name) {
		return nil
	}

	// Refuse a name that is itself absolute, and one that escapes destDir
	// through "..". The escape is checked on the joined target rather than
	// on a relative path derived from it: filepath.Join folds a leading
	// ".." away, so only the joined result catches a name like "../x".
	if path.IsAbs(name) {
		return fmt.Errorf("prsource: archive entry %q escapes the destination directory", e.header.Name)
	}
	target := filepath.Join(destDir, filepath.FromSlash(name))
	destRoot := strings.TrimSuffix(destDir, string(filepath.Separator)) + string(filepath.Separator)
	if !strings.HasPrefix(target, destRoot) {
		return fmt.Errorf("prsource: archive entry %q escapes the destination directory", e.header.Name)
	}

	switch e.header.Typeflag {
	case tar.TypeDir:
		return os.MkdirAll(target, 0o755)
	case tar.TypeReg:
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		return os.WriteFile(target, e.data, 0o644)
	default:
		// Symlinks, devices, etc: not followed, not written.
		return nil
	}
}

// isGitPath reports whether name is ".git" or lives under it.
func isGitPath(name string) bool {
	return name == ".git" || strings.HasPrefix(name, ".git/")
}
