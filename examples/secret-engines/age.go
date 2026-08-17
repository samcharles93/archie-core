//go:build ignore

// age secret engine plugin for archied.
//
// Resolves a SecretRef key from an age-encrypted file of "key=value"
// lines. Requires the age CLI on PATH and the AGE_FILE (encrypted file
// path) plus AGE_IDENTITY (age private-key file) env vars.
//
// Install: copy this file into the configured secret_engine_dir (default
// ~/.config/archie/secret-engines) and restart archied. The daemon
// evaluates it with Yaegi and registers engine "age". The //go:build
// ignore tag is a comment to the interpreter, so this file loads normally;
// the Go toolchain skips it, so it may live inside the module tree.
//
// The CLI is run through enginehost.Run rather than os/exec because
// yaegi's interpreted os/exec cannot pass an environment to child
// processes; env config is read with os.Getenv, which the daemon seeds
// with the host environment.
//
// Create the encrypted file with:
//
//	age -e -r <recipient> secrets.txt -o secrets.age
//
// where secrets.txt holds lines like "DB_PASSWORD=..." — blank lines and
// "#" comments are skipped, the value may contain "=" (split at the first
// "="), and an empty value is treated as missing.
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
	WName:    func() string { return "age" },
	WVersion: func() string { return "1.0.0" },
	WResolve: resolveAge,
}

func resolveAge(key string) (string, error) {
	file := os.Getenv("AGE_FILE")
	identity := os.Getenv("AGE_IDENTITY")
	if file == "" || identity == "" {
		return "", errors.New("age engine: AGE_FILE and AGE_IDENTITY must be set")
	}
	out, err := enginehost.Run("age", []string{"-d", "-i", identity, file})
	if err != nil {
		return "", fmt.Errorf("age engine: decrypt %s: %w", file, err)
	}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		i := strings.Index(line, "=")
		if i < 0 {
			continue // malformed line, skip
		}
		if strings.TrimSpace(line[:i]) != key {
			continue
		}
		value := strings.TrimSpace(line[i+1:])
		if value == "" {
			return "", fmt.Errorf("age engine: key %q is empty in %s", key, file)
		}
		return value, nil
	}
	return "", fmt.Errorf("age engine: key %q not found in %s", key, file)
}
