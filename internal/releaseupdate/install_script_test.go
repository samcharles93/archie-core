package releaseupdate

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestUpdateInstallGatewayOnlySkipsRuntimeWork(t *testing.T) {
	result, calls := runUpdateInstallScript(t, map[string]string{
		"ARCHIE_UPDATE_DAEMON_PREVIOUS": "1.12.0",
		"ARCHIE_UPDATE_DAEMON_VERSION":  "1.13.0",
		"ARCHIE_UPDATE_AGENT_PREVIOUS":  "1.9.9",
	})

	assertCallContains(t, calls, "git clone", "--no-checkout", "https://example.invalid/archie-core.git")
	assertCallContains(t, calls, "fetch --quiet --tags origin")
	assertCallAbsent(t, calls, "git pull")
	assertCallContains(t, calls, "git -C", "rev-parse --verify refs/tags/archied/v1.13.0^{commit}")
	assertCallContains(t, calls, "checkout --quiet --detach approved-release-commit")
	assertCallContains(t, calls, "go build", "internal/app/archied.runtimeVersion=1.9.9", "./cmd/archied")
	assertCallAbsent(t, calls, "./cmd/archie-agent")
	assertCallAbsent(t, calls, "docker compose build agent")
	if got := result.Installed[ComponentDaemon]; got != "1.13.0" {
		t.Fatalf("installed daemon = %q, want 1.13.0", got)
	}
	if _, found := result.Installed[ComponentAgent]; found {
		t.Fatalf("gateway-only result unexpectedly claims an agent install: %#v", result.Installed)
	}
	if !result.RestartRequested {
		t.Fatal("RestartRequested = false, want true for gateway update")
	}
}

func TestUpdateInstallRuntimeOnlySkipsGatewayBuild(t *testing.T) {
	result, calls := runUpdateInstallScript(t, map[string]string{
		"ARCHIE_UPDATE_DAEMON_PREVIOUS": "1.13.0",
		"ARCHIE_UPDATE_AGENT_PREVIOUS":  "1.9.9",
		"ARCHIE_UPDATE_AGENT_VERSION":   "1.10.0",
	})

	assertCallAbsent(t, calls, "./cmd/archied")
	assertCallAbsent(t, calls, "./cmd/archie-agent")
	assertCallContains(t, calls, "docker image save", "registry.example/archie-agent:stable")
	assertCallContains(t, calls, "docker build", "--build-arg RUNTIME_VERSION=1.10.0", "--tag registry.example/archie-agent:stable")
	if got := result.Installed[ComponentAgent]; got != "1.10.0" {
		t.Fatalf("installed agent = %q, want 1.10.0", got)
	}
	if _, found := result.Installed[ComponentDaemon]; found {
		t.Fatalf("runtime-only result unexpectedly claims a daemon install: %#v", result.Installed)
	}
}

func TestUpdateInstallBothComponentsBuildsDaemonAndManagedWorkerImage(t *testing.T) {
	result, calls := runUpdateInstallScript(t, map[string]string{
		"ARCHIE_UPDATE_DAEMON_PREVIOUS": "1.12.0",
		"ARCHIE_UPDATE_DAEMON_VERSION":  "1.13.0",
		"ARCHIE_UPDATE_AGENT_PREVIOUS":  "1.9.9",
		"ARCHIE_UPDATE_AGENT_VERSION":   "1.10.0",
	})

	assertCallContains(t, calls, "go build", "./cmd/archied")
	assertCallAbsent(t, calls, "./cmd/archie-agent")
	assertCallContains(t, calls, "docker image save", "registry.example/archie-agent:stable")
	assertCallContains(t, calls, "docker build", "RUNTIME_VERSION=1.10.0", "--tag registry.example/archie-agent:stable")
	if result.Installed[ComponentDaemon] != "1.13.0" || result.Installed[ComponentAgent] != "1.10.0" {
		t.Fatalf("installed = %#v, want both release versions", result.Installed)
	}
}

func TestUpdateInstallNoopDoesNotFetchBuildInstallOrRestart(t *testing.T) {
	result, calls := runUpdateInstallScript(t, map[string]string{
		"ARCHIE_UPDATE_DAEMON_PREVIOUS": "1.13.0",
		"ARCHIE_UPDATE_AGENT_PREVIOUS":  "1.9.9",
	})

	for _, forbidden := range []string{"git pull", "go build", "docker ", "systemd-run ", "install ", "cp "} {
		assertCallAbsent(t, calls, forbidden)
	}
	if len(result.Previous) != 0 || len(result.Installed) != 0 || len(result.Components) != 0 || result.RestartRequested {
		t.Fatalf("no-op result = %#v, want empty result without restart", result)
	}
}

func TestUpdateWatchdogRollsBackManagedWorkerImageAndReportsOnlyChangedComponents(t *testing.T) {
	tests := []struct {
		name              string
		components        string
		wantDaemon        string
		wantImageRollback bool
	}{
		{name: "gateway only", components: "daemon", wantDaemon: "old daemon"},
		{name: "runtime only", components: "agent", wantDaemon: "new daemon", wantImageRollback: true},
		{name: "both", components: "daemon,agent", wantDaemon: "old daemon", wantImageRollback: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			report, daemon, calls := runUpdateWatchdogFailure(t, tt.components)
			if daemon != tt.wantDaemon {
				t.Fatalf("daemon after rollback = %q, want %q", daemon, tt.wantDaemon)
			}
			if tt.wantImageRollback {
				assertCallContains(t, calls, "docker image load", "archie-agent-image.prev.tar")
			} else {
				assertCallAbsent(t, calls, "docker image load")
			}
			if !report.RolledBack || report.HealthCheck != "failed" {
				t.Fatalf("report = %#v, want failed rollback", report)
			}
			if _, found := report.Installed[ComponentDaemon]; found != strings.Contains(tt.components, "daemon") {
				t.Fatalf("daemon report membership = %v for components %q", found, tt.components)
			}
			if _, found := report.Installed[ComponentAgent]; found != strings.Contains(tt.components, "agent") {
				t.Fatalf("agent report membership = %v for components %q", found, tt.components)
			}
		})
	}
}

func TestUpdateWatchdogRestoresTaskDatabaseOnDaemonRollback(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	work := t.TempDir()
	binDir, fakeDir := filepath.Join(work, "bin"), filepath.Join(work, "fake-bin")
	reportPath := filepath.Join(work, "report.json")
	dbPath, backupPath := filepath.Join(work, "tasks.sqlite"), filepath.Join(work, "tasks.prev.sqlite")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{"archied": "new", "archied.prev": "old"} {
		if err := os.WriteFile(filepath.Join(binDir, name), []byte(content), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for path, version := range map[string]int{dbPath: 2, backupPath: 1} {
		if output, err := exec.Command("sqlite3", path, fmt.Sprintf("PRAGMA user_version=%d; CREATE TABLE tasks (id INTEGER);", version)).CombinedOutput(); err != nil {
			t.Fatalf("create database %s: %v: %s", path, err, output)
		}
	}
	callsPath := filepath.Join(work, "calls")
	writeFakeCommand(t, fakeDir, "systemctl", `printf '%s\n' "systemctl $*" >> "$ARCHIE_TEST_CALLS"`)
	writeFakeCommand(t, fakeDir, "cp", `printf '%s\n' "cp $*" >> "$ARCHIE_TEST_CALLS"; /usr/bin/cp "$@"`)
	writeFakeCommand(t, fakeDir, "curl", `exit 1`)
	cmd := exec.CommandContext(t.Context(), filepath.Join(root, "scripts", "archie-update-watchdog"))
	cmd.Env = append(os.Environ(), "PATH="+fakeDir+":"+os.Getenv("PATH"), "ARCHIE_BIN_DIR="+binDir,
		"ARCHIE_TEST_CALLS="+callsPath,
		"ARCHIE_UPDATE_REPORT_PATH="+reportPath, "ARCHIE_UPDATE_HEALTH_TIMEOUT=0", "ARCHIE_UPDATE_COMPONENTS=daemon",
		"ARCHIE_UPDATE_PREVIOUS_GATEWAY=1.19.10", "ARCHIE_UPDATE_INSTALLED_GATEWAY=1.20.0",
		"ARCHIE_TASK_DB_PATH="+dbPath, "ARCHIE_TASK_DB_BACKUP="+backupPath)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("watchdog failed: %v\n%s", err, output)
	}
	output, err := exec.Command("sqlite3", dbPath, "PRAGMA user_version;").Output()
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(output)) != "1" {
		t.Fatalf("restored schema version = %q, want 1", output)
	}
	calls, err := os.ReadFile(callsPath)
	if err != nil {
		t.Fatal(err)
	}
	callLines := strings.Split(strings.TrimSpace(string(calls)), "\n")
	stopIndex, copyIndex, startIndex := -1, -1, -1
	for i, line := range callLines {
		switch line {
		case "systemctl --user stop archied.service":
			stopIndex = i
		case "systemctl --user restart archied.service":
			startIndex = i
		}
		if strings.HasPrefix(line, "cp ") && strings.Contains(line, "tasks.prev.sqlite") {
			copyIndex = i
		}
	}
	if stopIndex < 0 || copyIndex < 0 || stopIndex > copyIndex {
		t.Fatalf("rollback must stop service before restoring database; calls = %q", calls)
	}
	if startIndex < 0 || startIndex <= copyIndex {
		t.Fatalf("rollback must restart service after restoring database; calls = %q", calls)
	}
}

func runUpdateInstallScript(t *testing.T, environment map[string]string) (Result, []string) {
	t.Helper()
	ctx := t.Context()

	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	work := t.TempDir()
	binDir := filepath.Join(work, "bin")
	configPath := filepath.Join(work, "config.toml")
	fakeDir := filepath.Join(work, "fake-bin")
	callsPath := filepath.Join(work, "calls")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, []byte("[containers]\nimage = 'registry.example/archie-agent:stable'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"archied", "archie-agent"} {
		if err := os.WriteFile(filepath.Join(binDir, name), []byte("old "+name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeFakeCommand(t, fakeDir, "git", `
printf '%s\n' "git $*" >> "$ARCHIE_TEST_CALLS"
case "$*" in
  *"clone "*)
    source_dir="${@: -1}"
    mkdir -p "$source_dir"
    /usr/bin/cp -R "$ARCHIE_TEST_SOURCE_DIR/scripts" "$source_dir/scripts"
    : > "$source_dir/docker-compose.yml"
    ;;
  *"rev-parse --verify refs/tags/"*) echo approved-release-commit ;;
esac
`)
	writeFakeCommand(t, fakeDir, "go", `
printf '%s\n' "go $*" >> "$ARCHIE_TEST_CALLS"
out=""
while [ "$#" -gt 0 ]; do
  if [ "$1" = "-o" ]; then out="$2"; break; fi
  shift
done
mkdir -p "$(dirname "$out")"
printf 'built\n' > "$out"
`)
	writeFakeCommand(t, fakeDir, "docker", `
printf '%s\n' "docker $*" >> "$ARCHIE_TEST_CALLS"
if [ "$1" = image ] && [ "$2" = save ]; then
  while [ "$#" -gt 0 ]; do
    if [ "$1" = --output ]; then
      : > "$2"
      break
    fi
    shift
  done
fi
`)
	writeFakeCommand(t, fakeDir, "systemd-run", `printf '%s\n' "systemd-run $*" >> "$ARCHIE_TEST_CALLS"`)
	writeFakeCommand(t, fakeDir, "install", `
printf '%s\n' "install $*" >> "$ARCHIE_TEST_CALLS"
/usr/bin/install "$@"
`)
	writeFakeCommand(t, fakeDir, "cp", `
printf '%s\n' "cp $*" >> "$ARCHIE_TEST_CALLS"
/usr/bin/cp "$@"
`)

	// The production adapter prepends /usr/local/go/bin. Run an otherwise
	// byte-for-byte copy with the harness PATH left first so fake go records
	// build intent without compiling or touching Docker/systemd.
	scriptData, err := os.ReadFile(filepath.Join(root, "scripts", "archie-update-install"))
	if err != nil {
		t.Fatal(err)
	}
	scriptPath := filepath.Join(work, "archie-update-install")
	scriptData = []byte(strings.Replace(string(scriptData), `export PATH="/usr/local/go/bin:$PATH"`, `export PATH="$PATH"`, 1))
	if err := os.WriteFile(scriptPath, scriptData, 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(ctx, scriptPath)
	cmd.Env = append(
		os.Environ(),
		"PATH="+fakeDir+":"+os.Getenv("PATH"),
		"ARCHIE_SOURCE_URL=https://example.invalid/archie-core.git",
		"ARCHIE_TEST_SOURCE_DIR="+root,
		"ARCHIE_BIN_DIR="+binDir,
		"ARCHIE_CONFIG_PATH="+configPath,
		"ARCHIE_TEST_CALLS="+callsPath,
	)
	for key, value := range environment {
		cmd.Env = append(cmd.Env, key+"="+value)
	}
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("archie-update-install failed: %v\n%s", err, output)
	}
	var result Result
	scanner := bufio.NewScanner(strings.NewReader(string(output)))
	found := false
	for scanner.Scan() {
		if payload, ok := strings.CutPrefix(scanner.Text(), updateResultSentinel); ok {
			found = true
			if err := json.Unmarshal([]byte(payload), &result); err != nil {
				t.Fatalf("decode result %q: %v", payload, err)
			}
		}
	}
	if !found {
		t.Fatalf("installer output has no result sentinel:\n%s", output)
	}
	callsData, err := os.ReadFile(callsPath)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	var calls []string
	if len(callsData) > 0 {
		calls = strings.Split(strings.TrimSpace(string(callsData)), "\n")
	}
	return result, calls
}

func writeFakeCommand(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("#!/usr/bin/env bash\nset -euo pipefail\n"+body), 0o755); err != nil {
		t.Fatal(err)
	}
}

func runUpdateWatchdogFailure(t *testing.T, components string) (Report, string, []string) {
	t.Helper()
	ctx := t.Context()

	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	work := t.TempDir()
	binDir := filepath.Join(work, "bin")
	fakeDir := filepath.Join(work, "fake-bin")
	reportPath := filepath.Join(work, "report.json")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"archied": "new daemon", "archied.prev": "old daemon",
		"archie-agent-image.prev.tar": "saved old image",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(binDir, name), []byte(content), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeFakeCommand(t, fakeDir, "systemctl", `exit 0`)
	writeFakeCommand(t, fakeDir, "curl", `exit 1`)
	writeFakeCommand(t, fakeDir, "docker", `printf '%s\n' "docker $*" >> "$ARCHIE_TEST_CALLS"`)
	callsPath := filepath.Join(work, "calls")

	cmd := exec.CommandContext(ctx, filepath.Join(root, "scripts", "archie-update-watchdog"))
	cmd.Env = append(
		os.Environ(),
		"PATH="+fakeDir+":"+os.Getenv("PATH"),
		"ARCHIE_BIN_DIR="+binDir,
		"ARCHIE_TEST_CALLS="+callsPath,
		"ARCHIE_AGENT_IMAGE=registry.example/archie-agent:stable",
		"ARCHIE_UPDATE_REPORT_PATH="+reportPath,
		"ARCHIE_UPDATE_HEALTH_TIMEOUT=0",
		"ARCHIE_UPDATE_COMPONENTS="+components,
		"ARCHIE_UPDATE_PREVIOUS_GATEWAY=1.12.0",
		"ARCHIE_UPDATE_INSTALLED_GATEWAY=1.13.0",
		"ARCHIE_UPDATE_PREVIOUS_RUNTIME=1.9.9",
		"ARCHIE_UPDATE_INSTALLED_RUNTIME=1.10.0",
	)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("watchdog failed: %v\n%s", err, output)
	}
	reportData, err := os.ReadFile(reportPath)
	if err != nil {
		t.Fatal(err)
	}
	var report Report
	if err := json.Unmarshal(reportData, &report); err != nil {
		t.Fatal(err)
	}
	daemon, err := os.ReadFile(filepath.Join(binDir, "archied"))
	if err != nil {
		t.Fatal(err)
	}
	callsData, err := os.ReadFile(callsPath)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	var calls []string
	if len(callsData) > 0 {
		calls = strings.Split(strings.TrimSpace(string(callsData)), "\n")
	}
	return report, string(daemon), calls
}

func assertCallContains(t *testing.T, calls []string, fragments ...string) {
	t.Helper()
	for _, call := range calls {
		matched := true
		for _, fragment := range fragments {
			matched = matched && strings.Contains(call, fragment)
		}
		if matched {
			return
		}
	}
	t.Fatalf("calls %#v contain no call with fragments %#v", calls, fragments)
}

func assertCallAbsent(t *testing.T, calls []string, fragment string) {
	t.Helper()
	for _, call := range calls {
		if strings.Contains(call, fragment) {
			t.Fatalf("calls unexpectedly contain %q: %#v", fragment, calls)
		}
	}
}
