package prreview

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path/filepath"
	"strings"
)

// maxScannedFileBytes bounds how much of one file either read quotes. A
// generated bundle is not evidence, and a review's budget is not its to spend.
const maxScannedFileBytes = 1 << 20

// skippedDirectories are the directories a snapshot's reads never enter:
// dependencies that are not this change's to review, and caches.
var skippedDirectories = map[string]bool{
	".git":         true,
	"node_modules": true,
	"__pycache__":  true,
	".venv":        true,
	"venv":         true,
	"vendor":       true,
}

// walkPaths lists the files of a snapshot a read wants, in lexical order. That
// order is what makes every result derived from the walk -- an import graph, a
// caller search -- the same result on every run.
func walkPaths(fsys fs.FS, wanted func(string) bool) ([]string, error) {
	var paths []string
	err := fs.WalkDir(fsys, ".", func(file string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if file != "." && skippedDirectories[entry.Name()] {
				return fs.SkipDir
			}
			return nil
		}
		if wanted(file) {
			paths = append(paths, file)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return paths, nil
}

// isQuotablePath reports whether a file may be quoted as evidence. A file with
// no extension is decided by its content instead, so a LICENSE or a Makefile
// is still read.
func isQuotablePath(file string) bool {
	extension := strings.ToLower(filepath.Ext(file))
	return extension == "" || quotableExtensions[extension]
}

// quotableExtensions are the file types this package quotes code from.
var quotableExtensions = map[string]bool{
	".py": true, ".js": true, ".jsx": true, ".ts": true, ".tsx": true,
	".go": true, ".rs": true, ".java": true, ".rb": true, ".php": true,
	".c": true, ".h": true, ".cpp": true, ".hpp": true, ".cs": true,
	".swift": true, ".kt": true, ".scala": true, ".sh": true,
	".yaml": true, ".yml": true, ".json": true, ".toml": true, ".ini": true,
	".cfg": true, ".md": true, ".sql": true, ".html": true, ".css": true,
	".scss": true, ".txt": true,
}

// readLines reads the lines of a file for quoting. A file the snapshot does not
// have, one larger than the scan bound, and a binary file all read as nothing:
// none of them is code a reviewer can be shown.
func readLines(fsys fs.FS, file string) ([]string, error) {
	if !isQuotablePath(file) {
		return nil, nil
	}
	data, err := readBounded(fsys, file)
	if err != nil || data == nil {
		return nil, err
	}
	if strings.IndexByte(string(data), 0) >= 0 {
		return nil, nil
	}
	lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	// A file that ends with a newline ends with a line, not with an empty one:
	// the trailing element the split leaves behind is not a line any position
	// can be anchored to.
	if last := len(lines) - 1; last >= 0 && lines[last] == "" {
		lines = lines[:last]
	}
	return lines, nil
}

// readBounded reads at most the scan bound of a file and answers nil for a file
// that is larger, or that the snapshot does not have.
func readBounded(fsys fs.FS, file string) ([]byte, error) {
	handle, err := fsys.Open(file)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("open %s: %w", file, err)
	}
	defer handle.Close()

	data, err := io.ReadAll(io.LimitReader(handle, maxScannedFileBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", file, err)
	}
	if len(data) > maxScannedFileBytes {
		return nil, nil
	}
	return data, nil
}

// appendUnique adds value to values unless it is already there, which keeps the
// order each caller built.
func appendUnique(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}
