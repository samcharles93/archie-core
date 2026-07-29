//go:build (darwin && amd64) || (linux && amd64) || (windows && amd64)

// Package rg embeds a statically-linked ripgrep binary and exposes a single
// entry point so the grep tool can always use authoritative rg matching.
package rg

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

var (
	extractOnce sync.Once
	extractPath string
	extractErr  error
)

// Path returns the filesystem path to the embedded rg binary, extracting it
// to a temporary file on first call. The caller must not remove the file.
func Path() (string, error) {
	extractOnce.Do(func() {
		extractPath, extractErr = extract()
	})
	if extractErr != nil {
		return "", extractErr
	}
	return extractPath, nil
}

func extract() (string, error) {
	dir, err := os.MkdirTemp("", "tau-rg-*")
	if err != nil {
		return "", fmt.Errorf("create rg temp dir: %w", err)
	}
	exe := filepath.Join(dir, "rg")
	if isWindows {
		exe += ".exe"
	}
	if err := os.WriteFile(exe, binary, 0o755); err != nil {
		return "", fmt.Errorf("write embedded rg: %w", err)
	}
	return exe, nil
}
