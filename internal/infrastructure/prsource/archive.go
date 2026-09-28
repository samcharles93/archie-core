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

// extractTarGz extracts a gzipped tar stream into destDir, stripping a
// single common leading path component when every entry shares one (the
// wrapper directory GitHub's and Gitea's repository archives both add,
// named after the repo and ref -- e.g. "acme-widget-deadbeef/"). A .git
// entry is never written: PRSource's isolation contract (docs/prds/
// pr-review-agent.md) requires the pipeline to read a .git-free snapshot,
// and a malicious PR's own tarball is exactly the input this cannot trust,
// so an entry escaping destDir via ".." or an absolute path is refused
// rather than silently written outside it.
func extractTarGz(r io.Reader, destDir string) error {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return fmt.Errorf("prsource: open archive: %w", err)
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	prefix, entries, err := planExtraction(tr)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if err := writeEntry(destDir, prefix, e); err != nil {
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
			// A bare top-level file (no directory component at all) proves
			// there is no single wrapper directory containing everything. A
			// bare top-level *directory* entry is not this evidence -- it is
			// exactly the wrapper directory's own header, which real
			// archives include alongside the files nested under it.
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

// writeEntry writes one archive entry under destDir, with prefix (if any)
// stripped from its name. Directories, regular files, and nothing else are
// written: a symlink or device entry from an untrusted PR archive is
// skipped rather than followed.
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

	target := filepath.Join(destDir, filepath.FromSlash(name))
	rel, err := filepath.Rel(destDir, target)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
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
