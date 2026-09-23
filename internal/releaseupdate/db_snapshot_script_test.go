package releaseupdate

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const snapshotConfig = "database_url = 'postgres://archie:hunter2@db.example/archie'\n\n[containers]\nimage = 'registry.example/archie-agent:stable'\n"

// The new State Store migrates on start and nothing undoes a migration
// automatically, so a daemon update snapshots the whole database first,
// through the release's own recovery subcommand, and hands the snapshot's
// path to the watchdog. The connection string stays inside the config.
func TestUpdateInstallSnapshotsTheDatabaseBeforeMigrating(t *testing.T) {
	snapshot := filepath.Join(t.TempDir(), "pre-update.dump")
	_, calls, _ := runUpdateInstallWithConfig(t, map[string]string{
		"ARCHIE_UPDATE_DAEMON_PREVIOUS": "1.22.0",
		"ARCHIE_UPDATE_DAEMON_VERSION":  "1.23.0",
		"ARCHIE_UPDATE_AGENT_PREVIOUS":  "1.21.0",
		"ARCHIE_DB_SNAPSHOT":            snapshot,
	}, snapshotConfig, false)

	assertCallContains(t, calls, "go run ./cmd/archie-state-store backup", "-config ", "-out "+snapshot)
	assertCallContains(t, calls, "systemd-run", "--setenv=ARCHIE_DB_SNAPSHOT="+snapshot)
	assertCallAbsent(t, calls, "hunter2")
}

func TestUpdateInstallSnapshotScope(t *testing.T) {
	tests := []struct {
		name         string
		env          map[string]string
		wantSnapshot bool
		wantRefusal  bool
	}{
		{name: "agent-only update does not touch the database", env: map[string]string{
			"ARCHIE_UPDATE_AGENT_PREVIOUS": "1.21.0", "ARCHIE_UPDATE_AGENT_VERSION": "1.22.0",
		}},
		{name: "daemon update refuses when the snapshot fails", wantSnapshot: true, wantRefusal: true, env: map[string]string{
			"ARCHIE_UPDATE_DAEMON_PREVIOUS": "1.22.0", "ARCHIE_UPDATE_DAEMON_VERSION": "1.23.0",
			"ARCHIE_UPDATE_AGENT_PREVIOUS": "1.21.0", "ARCHIE_TEST_GO_FAILS_SNAPSHOT": "1",
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, calls, output := runUpdateInstallWithConfig(t, tt.env, snapshotConfig, tt.wantRefusal)
			snapshotted := false
			for _, call := range calls {
				if strings.Contains(call, "archie-state-store backup") && strings.Contains(call, "-config ") {
					snapshotted = true
				}
			}
			if snapshotted != tt.wantSnapshot {
				t.Fatalf("snapshot attempted = %v, want %v; calls = %q", snapshotted, tt.wantSnapshot, calls)
			}
			if !tt.wantRefusal {
				return
			}
			if !strings.Contains(output, "could not snapshot the database") {
				t.Errorf("refusal must say the snapshot failed; output = %q", output)
			}
			for _, call := range calls {
				if strings.Contains(call, "install -m755") && !strings.Contains(call, "/scripts/") {
					t.Errorf("refused update still installed %q", call)
				}
			}
		})
	}
}

// A failed daemon update rolls the binaries back but never the database, and
// says so, naming the explicit offline restore rather than running one.
func TestUpdateWatchdogNeverRestoresTheDatabaseItself(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	work := t.TempDir()
	binDir, fakeDir := filepath.Join(work, "bin"), filepath.Join(work, "fake-bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{"archied": "new", "archied.prev": "old"} {
		if err := os.WriteFile(filepath.Join(binDir, name), []byte(content), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	callsPath := filepath.Join(work, "calls")
	writeFakeCommand(t, fakeDir, "systemctl", `exit 0`)
	writeFakeCommand(t, fakeDir, "curl", `exit 1`)
	for _, tool := range []string{"pg_restore", "psql", "go", "archie-state-store"} {
		writeFakeCommand(t, fakeDir, tool, `printf '%s\n' "`+tool+` $*" >> "$ARCHIE_TEST_CALLS"`)
	}
	snapshot := filepath.Join(work, "pre-update.dump")
	cmd := exec.CommandContext(t.Context(), filepath.Join(root, "scripts", "archie-update-watchdog"))
	cmd.Env = append(os.Environ(), "PATH="+fakeDir+":"+os.Getenv("PATH"), "ARCHIE_BIN_DIR="+binDir,
		"ARCHIE_TEST_CALLS="+callsPath, "ARCHIE_UPDATE_REPORT_PATH="+filepath.Join(work, "report.json"),
		"ARCHIE_UPDATE_HEALTH_TIMEOUT=0", "ARCHIE_UPDATE_COMPONENTS=daemon",
		"ARCHIE_DB_SNAPSHOT="+snapshot, "ARCHIE_CONFIG_PATH=/etc/archie/config.toml")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("watchdog failed: %v\n%s", err, output)
	}
	if calls, _ := os.ReadFile(callsPath); len(calls) != 0 {
		t.Errorf("watchdog ran database tooling itself: %q", calls)
	}
	for _, want := range []string{"NOT rolled back", "archie-state-store restore -config /etc/archie/config.toml -from " + snapshot, "lost"} {
		if !strings.Contains(string(output), want) {
			t.Errorf("output lacks %q:\n%s", want, output)
		}
	}
}
