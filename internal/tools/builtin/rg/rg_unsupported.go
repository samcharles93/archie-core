//go:build !((darwin && amd64) || (linux && amd64) || (windows && amd64))

// Package rg embeds a statically-linked ripgrep binary and exposes a single
// entry point so the grep tool can always use authoritative rg matching.
package rg

import "errors"

// Path reports that no ripgrep binary is bundled for this platform/arch.
// Callers fall back to the pure-Go grep implementation.
func Path() (string, error) {
	return "", errors.New("no embedded ripgrep binary for this platform")
}
