//go:build ignore

// sops secret engine plugin for archied.
//
// Resolves a SecretRef key to its plaintext value from a sops-encrypted
// file. Requires the sops CLI on PATH and the SOPS_FILE env var naming the
// encrypted file. Decryption keys are handled by sops itself via .sops.yaml
// or --age/--pgp configuration; no credentials are configured here.
//
// Install: copy this file into the configured secret_engine_dir (default
// ~/.config/archie/secret-engines) and restart archied. The daemon
// evaluates it with Yaegi and registers engine "sops". The //go:build
// ignore tag is a comment to the interpreter, so this file loads normally;
// the Go toolchain skips it, so it may live inside the module tree.
//
// The CLI is run through enginehost.Run rather than os/exec because
// yaegi's interpreted os/exec cannot pass an environment to child
// processes; env config is read with os.Getenv, which the daemon seeds
// with the host environment.
//
// Key names must be plain leaf names (e.g. "DB_PASSWORD"). Nested
// sops --extract JSON paths are not supported; quotes, backslashes and
// brackets in keys are rejected with a clear error.
package main

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/samcharles93/archie-core/internal/secret"
	"github.com/samcharles93/archie-core/internal/secret/enginehost"
)

var Engine = secret._Engine{
	WName:    func() string { return "sops" },
	WVersion: func() string { return "1.0.0" },
	WResolve: resolveSops,
}

func resolveSops(key string) (string, error) {
	file := os.Getenv("SOPS_FILE")
	if file == "" {
		return "", errors.New("sops engine: SOPS_FILE is not set")
	}
	if strings.ContainsAny(key, `"\[`) {
		return "", fmt.Errorf("sops engine: key %q is not a valid --extract path", key)
	}
	value, err := enginehost.Run("sops", []string{"-d", "--extract", `["` + key + `"]`, file})
	if err != nil {
		return "", fmt.Errorf("sops engine: decrypt %s: %w", file, err)
	}
	if value == "" {
		return "", fmt.Errorf("sops engine: key %q not found in %s", key, file)
	}
	return value, nil
}
