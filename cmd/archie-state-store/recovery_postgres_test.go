package main

import (
	"path/filepath"
	"strings"
	"testing"
)

// Without -db every recovery command targets the configured PostgreSQL
// database, so each one accepts -config and refuses, naming the key, when the
// configuration names no database.
func TestRecoveryWithoutDBUsesTheConfiguredDatabase(t *testing.T) {
	dir := t.TempDir()
	configPath := writeMinimalConfig(t, dir)
	tests := [][]string{
		{"backup", "-out", filepath.Join(dir, "snapshot.dump")},
		{"restore", "-from", filepath.Join(dir, "snapshot.dump")},
		{"validate"},
		{"rollback", "-kind", "settings"},
	}
	for _, args := range tests {
		t.Run(args[0], func(t *testing.T) {
			code, _, stderr := runRecoveryCmd(t, append(args, "-config", configPath)...)
			if code != 1 || !strings.Contains(stderr, "database_url") {
				t.Fatalf("%s exited %d, stderr %q; want 1 naming database_url", args[0], code, stderr)
			}
		})
	}
}
