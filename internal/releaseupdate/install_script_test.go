package releaseupdate

import (
	"bufio"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
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
	assertCallContains(t, calls, "checkout --quiet --detach archied-v1.13.0")
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

// Components are independently versioned (RELEASING.md), so the newest
// archied/v* and archie/v* tags usually sit on different commits -- gateway-only
// releases are the norm. Each changed component must be built from its own
// approved tag; forcing both tags onto one commit refuses every update once
// the two components' newest releases diverge.
func TestUpdateInstallBuildsEachComponentFromItsOwnTag(t *testing.T) {
	result, calls := runUpdateInstallScript(t, map[string]string{
		"ARCHIE_UPDATE_DAEMON_PREVIOUS": "1.12.0",
		"ARCHIE_UPDATE_DAEMON_VERSION":  "1.13.0",
		"ARCHIE_UPDATE_AGENT_PREVIOUS":  "1.9.9",
		"ARCHIE_UPDATE_AGENT_VERSION":   "1.10.0",
	})

	if result.Installed[ComponentDaemon] != "1.13.0" || result.Installed[ComponentAgent] != "1.10.0" {
		t.Fatalf("installed = %#v, want both release versions", result.Installed)
	}
	// The derived fake commits differ per tag, so these assertions fail if
	// either component is built from the other's release.
	daemonCheckout := indexOfCallContaining(t, calls, "checkout --quiet --detach archied-v1.13.0")
	agentCheckout := indexOfCallContaining(t, calls, "checkout --quiet --detach archie-v1.10.0")
	daemonBuild := indexOfCallContaining(t, calls, "go build", "./cmd/archied")
	agentImageBuild := indexOfCallContaining(t, calls, "docker build")
	if daemonCheckout < 0 || agentCheckout < 0 || daemonBuild < 0 || agentImageBuild < 0 {
		t.Fatalf("missing expected step: daemon checkout %d, daemon build %d, agent checkout %d, agent image build %d",
			daemonCheckout, daemonBuild, agentCheckout, agentImageBuild)
	}
	if daemonCheckout > daemonBuild {
		t.Errorf("daemon binaries must be built from the daemon tag's commit; calls = %#v", calls)
	}
	if agentCheckout > agentImageBuild {
		t.Errorf("the managed worker image must be built from the agent tag's commit; calls = %#v", calls)
	}
}

func indexOfCallContaining(t *testing.T, calls []string, fragments ...string) int {
	t.Helper()
	for i, call := range calls {
		matched := true
		for _, fragment := range fragments {
			matched = matched && strings.Contains(call, fragment)
		}
		if matched {
			return i
		}
	}
	return -1
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

// TestUpdateInstallForwardsHealthURLToTheWatchdogUnit closes the link between
// the two: systemd-run starts the watchdog with a clean environment, so a
// health address the daemon derived reaches the probe only if the install
// script hands it over explicitly (archie-core-1r4g).
func TestUpdateInstallForwardsHealthURLToTheWatchdogUnit(t *testing.T) {
	_, calls := runUpdateInstallScript(t, map[string]string{
		"ARCHIE_UPDATE_DAEMON_PREVIOUS": "1.12.0",
		"ARCHIE_UPDATE_DAEMON_VERSION":  "1.13.0",
		"ARCHIE_UPDATE_AGENT_PREVIOUS":  "1.9.9",
		"ARCHIE_HEALTH_URL":             "http://127.0.0.1:9000",
	})

	assertCallContains(t, calls, "systemd-run ", "--setenv=ARCHIE_HEALTH_URL=http://127.0.0.1:9000")
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

// TestUpdateWatchdogPassesHealthCheckAgainstRealListener exercises the branch
// no other test reaches: a daemon that comes back up. Every other watchdog
// test fakes curl as an immediate failure, so before this one the probe URL
// the script actually builds was never dialled and its default could point
// anywhere (archie-core-1r4g).
func TestUpdateWatchdogPassesHealthCheckAgainstRealListener(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	daemon := httptest.NewServer(mux)
	defer daemon.Close()

	report, installed, calls := runUpdateWatchdog(t, watchdogRun{
		components: "daemon",
		env: map[string]string{
			"ARCHIE_HEALTH_URL":            daemon.URL,
			"ARCHIE_UPDATE_HEALTH_TIMEOUT": "10",
		},
	})

	if report.HealthCheck != "passed" || report.RolledBack {
		t.Fatalf("report = %#v, want a passed health check with no rollback", report)
	}
	if installed != "new daemon" {
		t.Fatalf("installed daemon = %q, want the new binary left in place", installed)
	}
	assertCallAbsent(t, calls, "docker image load")
}

// TestUpdateWatchdogDefaultHealthURLTargetsTheDaemon pins the fallback the
// script uses when nothing exports one. It has to be the address archied
// itself serves /healthz on ([health] listen); anything else times out and
// rolls back a healthy release.
func TestUpdateWatchdogDefaultHealthURLTargetsTheDaemon(t *testing.T) {
	report, _, calls := runUpdateWatchdog(t, watchdogRun{
		components: "daemon",
		curl:       `printf '%s\n' "curl $*" >> "$ARCHIE_TEST_CALLS"; exit 0`,
		env:        map[string]string{"ARCHIE_UPDATE_HEALTH_TIMEOUT": "10"},
	})

	assertCallContains(t, calls, "curl ", "http://127.0.0.1:8485/healthz")
	if report.HealthCheck != "passed" || report.RolledBack {
		t.Fatalf("report = %#v, want a passed health check with no rollback", report)
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
		createSQLiteDatabase(t, path, version)
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
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var version int
	if err := db.QueryRowContext(t.Context(), "PRAGMA user_version").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != 1 {
		t.Fatalf("restored schema version = %d, want 1", version)
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

func createSQLiteDatabase(t *testing.T, path string, version int) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open database %s: %v", path, err)
	}
	defer db.Close()
	if _, err := db.ExecContext(t.Context(), "PRAGMA user_version = "+strconv.Itoa(version)); err != nil {
		t.Fatalf("set database version for %s: %v", path, err)
	}
	if _, err := db.ExecContext(t.Context(), "CREATE TABLE tasks (id INTEGER)"); err != nil {
		t.Fatalf("create tasks table in %s: %v", path, err)
	}
}

func runUpdateInstallScript(t *testing.T, environment map[string]string) (Result, []string) {
	t.Helper()
	result, calls, _ := runUpdateInstall(t, environment, false)
	return result, calls
}

// runUpdateInstall runs the adapter, optionally expecting it to refuse. It
// returns the parsed result (empty when the run was expected to fail), the
// recorded calls, and the combined output.
func runUpdateInstall(t *testing.T, environment map[string]string, wantErr bool) (Result, []string, string) {
	t.Helper()
	return runUpdateInstallWithConfig(t, environment, "[containers]\nimage = 'registry.example/archie-agent:stable'\n", wantErr)
}

// runUpdateInstallWithConfig is runUpdateInstall with the operator's config
// file under the caller's control. Config-derived behaviour -- which database
// the updater backs up, which image it rebuilds -- is otherwise untestable,
// which is how the task-database backup path came to be wrong.
func runUpdateInstallWithConfig(t *testing.T, environment map[string]string, configBody string, wantErr bool) (Result, []string, string) {
	t.Helper()
	return runUpdateInstallEdited(t, environment, configBody, nil, wantErr)
}

// runUpdateInstallEdited is runUpdateInstallWithConfig with the ability to edit
// the installer's text before it runs. The service list is hardcoded, so the
// only way to test the coverage guard against the stale list it exists for is to
// change that text -- which is exactly the state the guard must catch.
func runUpdateInstallEdited(t *testing.T, environment map[string]string, configBody string, edit func(string) string, wantErr bool) (Result, []string, string) {
	t.Helper()
	return runUpdateInstallFull(t, environment, configBody, nil, edit, wantErr)
}

// runUpdateInstallFull is runUpdateInstallEdited with control over the versions
// already installed in the bin directory, so a skewed host can be built on
// purpose (archie-core-k94o).
func runUpdateInstallFull(t *testing.T, environment map[string]string, configBody string, installed map[string]string, edit func(string) string, wantErr bool) (Result, []string, string) {
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
	if err := os.WriteFile(configPath, []byte(configBody), 0o600); err != nil {
		t.Fatal(err)
	}
	previousVersion := environment["ARCHIE_UPDATE_DAEMON_PREVIOUS"]
	if previousVersion == "" {
		previousVersion = "dev"
	}
	writeFakeBinary(t, binDir, "archied", previousVersion)
	if err := os.WriteFile(filepath.Join(binDir, "archie-agent"), []byte("old archie-agent"), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, version := range installed {
		writeFakeBinary(t, binDir, name, version)
	}
	writeFakeCommand(t, fakeDir, "git", `
printf '%s\n' "git $*" >> "$ARCHIE_TEST_CALLS"
case "$*" in
  *"clone "*)
    source_dir="${@: -1}"
    mkdir -p "$source_dir"
    /usr/bin/cp -R "$ARCHIE_TEST_SOURCE_DIR/scripts" "$source_dir/scripts"
    for c in archied archie-gateway archie-state-store archie-ui archie-messaging archie-playbooks archie-agent; do
      mkdir -p "$source_dir/cmd/$c"
    done
    : > "$source_dir/docker-compose.yml"
    ;;
  *"rev-parse --verify refs/tags/"*)
    tag="$(printf '%s' "$*" | sed 's/.*refs\/tags\///; s/\^.*//')"
    echo "${tag//\//-}"
    ;;
esac
`)
	writeFakeCommand(t, fakeDir, "go", `
printf '%s\n' "go $*" >> "$ARCHIE_TEST_CALLS"
# The pre-flight task-database backup runs through the release's own
# archie-state-store subcommand, so a release whose recovery path cannot run
# must refuse the update.
if [ "$1" = "run" ]; then
  [ "${ARCHIE_TEST_GO_FAILS_RUN:-}" = "1" ] && exit 1
  exit 0
fi
out=""
name=""
version="dev"
while [ "$#" -gt 0 ]; do
  if [ "$1" = "-o" ]; then out="$2"; fi
  case "$1" in
    ./cmd/*) name="${1#./cmd/}" ;;
    *buildinfo.Version=*)
      version="${1##*buildinfo.Version=}"
      version="${version%% *}"
      ;;
  esac
  shift
done
mkdir -p "$(dirname "$out")"
# The installed binary must answer -version the way a real one does, or the
# updater's skew check cannot read it.
printf '#!/usr/bin/env bash\necho "%s %s"\n' "$name" "$version" > "$out"
chmod 755 "$out"
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
	writeFakeCommand(t, fakeDir, "systemctl", `
printf '%s\n' "systemctl $*" >> "$ARCHIE_TEST_CALLS"
if [ "$2" = list-unit-files ]; then
  # The reference deployment runs exactly these five as units. A command with
  # no unit -- archie-playbooks, archie-agent -- has to look unitless, or the
  # coverage guard would mistake every cmd/ directory for a host service.
  present="${ARCHIE_TEST_PRESENT_UNITS:-archied.service archie-gateway.service archie-state-store.service archie-ui.service archie-messaging.service}"
  case " ${ARCHIE_TEST_ABSENT_UNITS:-} " in
    *" $3 "*) exit 0 ;;
  esac
  case " $present " in
    *" $3 "*) printf '%s enabled enabled\n' "$3" ;;
  esac
fi
`)
	writeFakeCommand(t, fakeDir, "install", `
printf '%s\n' "install $*" >> "$ARCHIE_TEST_CALLS"
/usr/bin/install "$@"
`)
	writeFakeCommand(t, fakeDir, "cp", `
printf '%s\n' "cp $*" >> "$ARCHIE_TEST_CALLS"
/usr/bin/cp "$@"
`)
	writeFakeCommand(t, fakeDir, "sqlite3", `
printf '%s\n' "sqlite3 $*" >> "$ARCHIE_TEST_CALLS"
[ "${ARCHIE_TEST_SQLITE3_FAILS:-}" = "1" ] && exit 1
# Reproduce ".backup '<path>'" by writing a plausible backup beside the
# source, so the watchdog's existence check behaves as it would in production.
for arg in "$@"; do
  case "$arg" in
    .backup*) target="$(printf '%s' "$arg" | sed "s/^\.backup[[:space:]]*//; s/^'//; s/'$//")" ;;
  esac
done
if [ -n "${target:-}" ]; then
  mkdir -p "$(dirname "$target")"
  printf 'backup\n' > "$target"
fi
`)

	// The production adapter prepends /usr/local/go/bin. Run an otherwise
	// byte-for-byte copy with the harness PATH left first so fake go records
	// build intent without compiling or touching Docker/systemd.
	scriptData, err := os.ReadFile(filepath.Join(root, "scripts", "archie-update-install"))
	if err != nil {
		t.Fatal(err)
	}
	script := string(scriptData)
	if edit != nil {
		script = edit(script)
	}
	scriptPath := filepath.Join(work, "archie-update-install")
	script = strings.Replace(script, `export PATH="/usr/local/go/bin:$PATH"`, `export PATH="$PATH"`, 1)
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
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
	switch {
	case wantErr && err == nil:
		t.Fatalf("archie-update-install succeeded, want refusal:\n%s", output)
	case !wantErr && err != nil:
		t.Fatalf("archie-update-install failed: %v\n%s", err, output)
	}
	callsData, err := os.ReadFile(callsPath)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	var calls []string
	if len(callsData) > 0 {
		calls = strings.Split(strings.TrimSpace(string(callsData)), "\n")
	}
	if wantErr {
		return Result{}, calls, string(output)
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
	return result, calls, string(output)
}

func writeFakeBinary(t *testing.T, dir, name, version string) {
	t.Helper()
	body := "#!/usr/bin/env bash\necho " + strconv.Quote(name+" "+version) + "\n"
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
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

// watchdogRun configures one end-to-end run of the watchdog script. curl is
// the fake curl placed on PATH; leaving it empty keeps the real curl there,
// which is what turns a health probe into a genuine test of the URL the
// watchdog builds rather than of a stub that ignores it.
type watchdogRun struct {
	components string
	curl       string
	env        map[string]string
}

func runUpdateWatchdogFailure(t *testing.T, components string) (Report, string, []string) {
	t.Helper()
	return runUpdateWatchdog(t, watchdogRun{
		components: components,
		curl:       `exit 1`,
		env:        map[string]string{"ARCHIE_UPDATE_HEALTH_TIMEOUT": "0"},
	})
}

func runUpdateWatchdog(t *testing.T, run watchdogRun) (Report, string, []string) {
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
	if run.curl != "" {
		writeFakeCommand(t, fakeDir, "curl", run.curl)
	}
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
		"ARCHIE_UPDATE_COMPONENTS="+run.components,
		"ARCHIE_UPDATE_PREVIOUS_GATEWAY=1.12.0",
		"ARCHIE_UPDATE_INSTALLED_GATEWAY=1.13.0",
		"ARCHIE_UPDATE_PREVIOUS_RUNTIME=1.9.9",
		"ARCHIE_UPDATE_INSTALLED_RUNTIME=1.10.0",
	)
	for key, value := range run.env {
		cmd.Env = append(cmd.Env, key+"="+value)
	}
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

// An archied release is five processes. Installing the daemon binary alone
// left the State Store, Gateway and UI on the previous release, so archied
// came up against a State Store nothing had started, failed its health check
// and rolled back (archie-core-hbqk).
func TestUpdateInstallBuildsEveryReleaseBinary(t *testing.T) {
	_, calls := runUpdateInstallScript(t, map[string]string{
		"ARCHIE_UPDATE_DAEMON_PREVIOUS": "1.22.0",
		"ARCHIE_UPDATE_DAEMON_VERSION":  "1.23.0",
		"ARCHIE_UPDATE_AGENT_PREVIOUS":  "1.21.0",
	})

	for _, cmd := range []string{"archied", "archie-gateway", "archie-state-store", "archie-ui", "archie-messaging", "archie-playbooks"} {
		assertCallContains(t, calls, "go build", "internal/app/archied.gatewayVersion=1.23.0", "./cmd/"+cmd)
		assertCallContains(t, calls, "install -m755", "/"+cmd)
	}
	assertCallAbsent(t, calls, "./cmd/archie-agent")
	// The watchdog cannot restart what it is not told about.
	assertCallContains(t, calls, "systemd-run",
		"--setenv=ARCHIE_UPDATE_UNITS=archie-state-store archie-gateway archied archie-ui archie-messaging")
	assertCallContains(t, calls, "systemd-run",
		"--setenv=ARCHIE_UPDATE_BINARIES=archie-state-store archie-gateway archied archie-ui archie-messaging archie-playbooks")
}

// Installing binaries for processes the host has no unit for produces exactly
// the failure this bug was: archied dials a State Store nothing starts. Refuse
// before touching the host instead, and say what is missing.
func TestUpdateInstallRefusesWhenAServiceUnitIsMissing(t *testing.T) {
	_, calls, output := runUpdateInstall(t, map[string]string{
		"ARCHIE_UPDATE_DAEMON_PREVIOUS": "1.22.0",
		"ARCHIE_UPDATE_DAEMON_VERSION":  "1.23.0",
		"ARCHIE_UPDATE_AGENT_PREVIOUS":  "1.21.0",
		"ARCHIE_TEST_ABSENT_UNITS":      "archie-state-store.service",
	}, true)

	if !strings.Contains(output, "archie-state-store.service") {
		t.Errorf("refusal must name the missing unit; output = %q", output)
	}
	t.Logf("refusal output:\n%s", strings.TrimSpace(output))
	// Nothing may be replaced on a host it would leave broken.
	assertCallAbsent(t, calls, "go build")
	assertCallAbsent(t, calls, "install -m755")
}

// The other half of the same contract, and the failure that actually happened
// (archie-core-1faq): the installed updater's GATEWAY_SERVICES predated
// archie-messaging, so the update neither backed it up nor restarted it and a
// hand-installed binary kept serving under an otherwise updated host. A unit
// this host runs must be named by the list, or the update refuses and says which.
func TestUpdateInstallRefusesWhenTheServiceListOmitsAHostUnit(t *testing.T) {
	_, calls, output := runUpdateInstallEdited(t, map[string]string{
		"ARCHIE_UPDATE_DAEMON_PREVIOUS": "1.22.0",
		"ARCHIE_UPDATE_DAEMON_VERSION":  "1.23.0",
		"ARCHIE_UPDATE_AGENT_PREVIOUS":  "1.21.0",
	}, "[containers]\nimage = 'registry.example/archie-agent:stable'\n", func(script string) string {
		// Drop archie-messaging from the list without touching its unit: the
		// stale installed updater's view of the host.
		return strings.Replace(script,
			`GATEWAY_SERVICES="archie-state-store archie-gateway archied archie-ui archie-messaging"`,
			`GATEWAY_SERVICES="archie-state-store archie-gateway archied archie-ui"`,
			1)
	}, true)

	if !strings.Contains(output, "archie-messaging.service") {
		t.Errorf("refusal must name the unmanaged unit; output = %q", output)
	}
	t.Logf("refusal output:\n%s", strings.TrimSpace(output))
	// Nothing may be replaced on a host the update would leave partial.
	assertCallAbsent(t, calls, "go build")
	assertCallAbsent(t, calls, "install -m755")
}

// Carina's actual pair (archie-core-k94o): archie-messaging 1.35.0 beside an
// archie-state-store 1.30.0, the daemon at 1.30.0, and nothing approved to
// install. The component-driven updater reported only "nothing to install"
// while the two disagreed; now it reads each installed binary and names them.
func TestUpdateInstallRefusesWhenInstalledBinariesDisagree(t *testing.T) {
	_, calls, output := runUpdateInstallFull(t, map[string]string{
		"ARCHIE_UPDATE_DAEMON_PREVIOUS": "1.30.0",
		"ARCHIE_UPDATE_AGENT_PREVIOUS":  "1.30.0",
	}, "[containers]\nimage = 'registry.example/archie-agent:stable'\n", map[string]string{
		"archie-messaging":   "1.35.0",
		"archie-state-store": "1.30.0",
	}, nil, true)

	if !strings.Contains(output, "archie-messaging=1.35.0") {
		t.Errorf("refusal must name the skewed binary and its version; output = %q", output)
	}
	if strings.Contains(output, "archie-state-store=") {
		t.Errorf("refusal named a binary that agrees; output = %q", output)
	}
	assertCallAbsent(t, calls, "go build")
	assertCallAbsent(t, calls, "install -m755")
	t.Logf("refusal output:\n%s", strings.TrimSpace(output))
}

// A binary built outside a release reports "dev"; on a released host that is
// skew too, and it must be named rather than read as agreement.
func TestUpdateInstallRefusesAnUnstampedBinary(t *testing.T) {
	_, _, output := runUpdateInstallFull(t, map[string]string{
		"ARCHIE_UPDATE_DAEMON_PREVIOUS": "1.30.0",
		"ARCHIE_UPDATE_AGENT_PREVIOUS":  "1.30.0",
	}, "[containers]\nimage = 'registry.example/archie-agent:stable'\n", map[string]string{
		"archie-ui": "dev",
	}, nil, true)

	if !strings.Contains(output, "archie-ui=dev") {
		t.Errorf("refusal must name the unstamped binary; output = %q", output)
	}
}

// The deployment is several processes, so the watchdog must cycle all of them,
// and in dependency order: the State Store owns archie.db and everything dials
// it, so it starts first and stops last (archie-core-hbqk).
func TestUpdateWatchdogCyclesEveryUnitInDependencyOrder(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	work := t.TempDir()
	binDir, fakeDir := filepath.Join(work, "bin"), filepath.Join(work, "fake-bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// archie-ui is new in this release: it has no .prev, so rolling back must
	// remove it rather than leave a binary from a release that was withdrawn.
	for name, content := range map[string]string{
		"archied": "new", "archied.prev": "old",
		"archie-state-store": "new", "archie-state-store.prev": "old",
		"archie-ui": "new",
	} {
		if err := os.WriteFile(filepath.Join(binDir, name), []byte(content), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	callsPath := filepath.Join(work, "calls")
	writeFakeCommand(t, fakeDir, "systemctl", `printf '%s\n' "systemctl $*" >> "$ARCHIE_TEST_CALLS"`)
	writeFakeCommand(t, fakeDir, "curl", `exit 1`)
	cmd := exec.CommandContext(t.Context(), filepath.Join(root, "scripts", "archie-update-watchdog"))
	cmd.Env = append(os.Environ(), "PATH="+fakeDir+":"+os.Getenv("PATH"), "ARCHIE_BIN_DIR="+binDir,
		"ARCHIE_TEST_CALLS="+callsPath, "ARCHIE_UPDATE_HEALTH_TIMEOUT=0",
		"ARCHIE_UPDATE_COMPONENTS=daemon",
		"ARCHIE_UPDATE_UNITS=archie-state-store archied archie-ui",
		"ARCHIE_UPDATE_BINARIES=archie-state-store archied archie-ui",
		"ARCHIE_UPDATE_PREVIOUS_GATEWAY=1.22.0", "ARCHIE_UPDATE_INSTALLED_GATEWAY=1.23.0")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("watchdog failed: %v\n%s", err, output)
	}
	calls, err := os.ReadFile(callsPath)
	if err != nil {
		t.Fatal(err)
	}
	indexOf := func(want string) int {
		for i, line := range strings.Split(strings.TrimSpace(string(calls)), "\n") {
			if line == want {
				return i
			}
		}
		return -1
	}
	startStore := indexOf("systemctl --user restart archie-state-store.service")
	startDaemon := indexOf("systemctl --user restart archied.service")
	stopUI := indexOf("systemctl --user stop archie-ui.service")
	stopStore := indexOf("systemctl --user stop archie-state-store.service")
	if startStore < 0 || startDaemon < 0 || startStore > startDaemon {
		t.Errorf("State Store must start before archied; calls =\n%s", calls)
	}
	if stopUI < 0 || stopStore < 0 || stopUI > stopStore {
		t.Errorf("UI must stop before the State Store; calls =\n%s", calls)
	}
	if _, err := os.Stat(filepath.Join(binDir, "archie-ui")); !os.IsNotExist(err) {
		t.Errorf("archie-ui had no .prev and must be removed on rollback, stat err = %v", err)
	}
	for _, name := range []string{"archied", "archie-state-store"} {
		got, err := os.ReadFile(filepath.Join(binDir, name))
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != "old" {
			t.Errorf("%s = %q after rollback, want the previous binary", name, got)
		}
	}
}

// The updater must back up the database the daemon actually uses, which is
// `<db_path>-tasks.sqlite` (taskDBPath in internal/app/archied/main.go), not
// the configured path itself. The configured path may be a zero-byte
// placeholder while the real store sits beside it -- so backing the literal
// value up produces an empty backup, and a rollback then restores that empty
// file over the database and deletes its -wal/-shm. A rollback that destroys
// the task store is worse than no rollback.
//
// The snapshot is taken by the release's own recovery subcommand rather than
// by hand-rolled shell, so the installer and an operator run the same code and
// the two cannot drift apart.
func TestUpdateInstallBacksUpTheSiblingTaskDatabase(t *testing.T) {
	work := t.TempDir()
	configured := filepath.Join(work, "archie.db")
	realStore := configured + "-tasks.sqlite"
	if err := os.WriteFile(configured, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(realStore, []byte("real task data"), 0o600); err != nil {
		t.Fatal(err)
	}
	backup := filepath.Join(work, "backup.sqlite")

	_, calls, _ := runUpdateInstallWithConfig(t, map[string]string{
		"ARCHIE_UPDATE_DAEMON_PREVIOUS": "1.22.0",
		"ARCHIE_UPDATE_DAEMON_VERSION":  "1.23.0",
		"ARCHIE_UPDATE_AGENT_PREVIOUS":  "1.21.0",
		"ARCHIE_TASK_DB_BACKUP":         backup,
	}, "db_path = \""+configured+"\"\n\n[containers]\nimage = 'registry.example/archie-agent:stable'\n", false)

	assertCallContains(t, calls, "go run ./cmd/archie-state-store backup", "-db "+realStore, "-out "+backup)
	assertCallAbsent(t, calls, "sqlite3")
	assertCallContains(t, calls, "systemd-run", "--setenv=ARCHIE_TASK_DB_PATH="+realStore)
	// The zero-byte placeholder is not the store, and must never be the thing
	// handed to the watchdog as ARCHIE_TASK_DB_PATH.
	assertCallAbsent(t, calls, "--setenv=ARCHIE_TASK_DB_PATH="+configured+" ")
}

// db_path can also appear under a TOML table -- the retired [indexing] section
// once owned its own database there, and a future section could do the same.
// Reading the first match in the file can select an unrelated database, so the
// value must come from the daemon's own top-level key.
func TestUpdateInstallIgnoresDbPathFromOtherSections(t *testing.T) {
	work := t.TempDir()
	configured := filepath.Join(work, "archie.db")
	realStore := configured + "-tasks.sqlite"
	indexStore := filepath.Join(work, "workspace-indexes.db")
	for path, content := range map[string]string{realStore: "real task data", indexStore: "index data"} {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	backup := filepath.Join(work, "backup.sqlite")

	_, calls, _ := runUpdateInstallWithConfig(t, map[string]string{
		"ARCHIE_UPDATE_DAEMON_PREVIOUS": "1.22.0",
		"ARCHIE_UPDATE_DAEMON_VERSION":  "1.23.0",
		"ARCHIE_UPDATE_AGENT_PREVIOUS":  "1.21.0",
		"ARCHIE_TASK_DB_BACKUP":         backup,
	}, "db_path = \""+configured+"\"\n\n[indexing]\ndb_path = \""+indexStore+"\"\n\n[containers]\nimage = 'registry.example/archie-agent:stable'\n", false)

	assertCallContains(t, calls, "go run ./cmd/archie-state-store backup", "-db "+realStore)
	for _, call := range calls {
		if strings.Contains(call, "go run ./cmd/archie-state-store backup") && strings.Contains(call, indexStore) {
			t.Errorf("backed up the other section's database %s: %q", indexStore, call)
		}
	}
	assertCallAbsent(t, calls, "--setenv=ARCHIE_TASK_DB_PATH="+indexStore)
}

// A store that cannot be backed up must fail the update before anything is
// replaced. Installing with no usable rollback is how a failed update becomes
// a destroyed task store.
func TestUpdateInstallRefusesWhenTaskDatabaseCannotBeBackedUp(t *testing.T) {
	work := t.TempDir()
	configured := filepath.Join(work, "archie.db")
	if err := os.WriteFile(configured+"-tasks.sqlite", []byte("data"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, calls, output := runUpdateInstallWithConfig(t, map[string]string{
		"ARCHIE_UPDATE_DAEMON_PREVIOUS": "1.22.0",
		"ARCHIE_UPDATE_DAEMON_VERSION":  "1.23.0",
		"ARCHIE_UPDATE_AGENT_PREVIOUS":  "1.21.0",
		"ARCHIE_TEST_GO_FAILS_RUN":      "1",
	}, "db_path = \""+configured+"\"\n\n[containers]\nimage = 'registry.example/archie-agent:stable'\n", true)

	if !strings.Contains(output, "task database") {
		t.Errorf("refusal must name the task database; output = %q", output)
	}
	// Nothing of the release may be placed on the host, and no backup of the
	// failure path may be attempted either.
	for _, call := range calls {
		if strings.Contains(call, "install -m755") && !strings.Contains(call, "/scripts/") {
			t.Errorf("refused update still installed %q", call)
		}
	}
	assertCallAbsent(t, calls, "arhied.prev")
}

// The sidecar is the only record of the managed agent version, and
// archie-update-check refuses anything but a bare release version, so a stray
// escape in the write leaves the installed agent reported as unknown forever.
func TestUpdateInstallWritesABareAgentVersionSidecar(t *testing.T) {
	versionFile := filepath.Join(t.TempDir(), "archie-agent.version")
	runUpdateInstallScript(t, map[string]string{
		"ARCHIE_UPDATE_DAEMON_PREVIOUS": "1.13.0",
		"ARCHIE_UPDATE_AGENT_PREVIOUS":  "1.9.9",
		"ARCHIE_UPDATE_AGENT_VERSION":   "1.10.0",
		"ARCHIE_AGENT_VERSION_FILE":     versionFile,
	})

	recorded, err := os.ReadFile(versionFile)
	if err != nil {
		t.Fatal(err)
	}
	if string(recorded) != "1.10.0\n" {
		t.Fatalf("sidecar = %q, want %q", recorded, "1.10.0\n")
	}
}
