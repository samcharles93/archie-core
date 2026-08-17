//go:build ignore

// vault secret engine plugin for archied.
//
// Resolves a SecretRef key to its plaintext value from HashiCorp Vault or
// OpenBao. Requires the vault CLI on PATH and the VAULT_ADDR + VAULT_TOKEN
// env vars, which the CLI reads natively (OpenBao's CLI accepts the same
// variables for compatibility).
//
// Install: copy this file into the configured secret_engine_dir (default
// ~/.config/archie/secret-engines) and restart archied. The daemon
// evaluates it with Yaegi and registers engine "vault". The //go:build
// ignore tag is a comment to the interpreter, so this file loads normally;
// the Go toolchain skips it, so it may live inside the module tree.
//
// The CLI is run through enginehost.Run rather than os/exec: VAULT_ADDR
// and VAULT_TOKEN must reach the child process, and yaegi's interpreted
// os/exec cannot pass an environment to children. enginehost.Run inherits
// the full host environment, so the vault CLI sees both vars.
//
// Key format: "path/field" resolves field within the KV path (e.g.
// "secret/archie/DB_PASSWORD"). A bare key resolves within the
// VAULT_SECRET_PATH prefix, which is required in that case. KV v1/v2 path
// translation is delegated to the CLI.
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
	WName:    func() string { return "vault" },
	WVersion: func() string { return "1.0.0" },
	WResolve: resolveVault,
}

func resolveVault(key string) (string, error) {
	if os.Getenv("VAULT_ADDR") == "" || os.Getenv("VAULT_TOKEN") == "" {
		return "", errors.New("vault engine: VAULT_ADDR and VAULT_TOKEN must be set")
	}
	path, field, err := vaultTarget(key)
	if err != nil {
		return "", err
	}
	value, err := enginehost.Run("vault", []string{"kv", "get", "-field=" + field, path})
	if err != nil {
		return "", fmt.Errorf("vault engine: read %s/%s: %w", path, field, err)
	}
	if value == "" {
		return "", fmt.Errorf("vault engine: key %q not found at %s", field, path)
	}
	return value, nil
}

// vaultTarget splits a key into a KV path and field name. A key containing
// a slash names the full location ("secret/archie/DB_PASSWORD"); a bare key
// resolves within VAULT_SECRET_PATH.
func vaultTarget(key string) (path, field string, err error) {
	if i := strings.LastIndex(key, "/"); i >= 0 {
		return key[:i], key[i+1:], nil
	}
	prefix := os.Getenv("VAULT_SECRET_PATH")
	if prefix == "" {
		return "", "", errors.New("vault engine: key has no path and VAULT_SECRET_PATH is not set")
	}
	return prefix, key, nil
}
