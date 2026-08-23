package releaseupdate

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeFixtureScript creates an executable shell script and returns its path.
func writeFixtureScript(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "fixture.sh")
	script := "#!/usr/bin/env bash\nset -euo pipefail\n" + body
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fixture script: %v", err)
	}
	return path
}

// TestCommandInstallerStreamsProgressLive confirms every stdout line the
// script prints reaches progress() as it is emitted, not buffered until the
// process exits -- this is the difference between silent-until-done and
// live status.
func TestCommandInstallerStreamsProgressLive(t *testing.T) {
	script := writeFixtureScript(t, `
echo "==> fetching"
echo "==> building"
echo 'ARCHIE_UPDATE_RESULT {"previous":{"daemon":"1.0.0"},"installed":{"daemon":"1.1.0"},"components":[{"id":"daemon","label":"daemon","status":"updated"}],"restart_requested":true}'
`)
	installer := CommandInstaller{Command: []string{script}}
	var progress []string
	result, err := installer.Install(context.Background(), Snapshot{}, InstallMeta{}, func(line string) { progress = append(progress, line) })
	if err != nil {
		t.Fatalf("Install() error = %v", err)
	}
	if len(progress) != 2 || progress[0] != "==> fetching" || progress[1] != "==> building" {
		t.Fatalf("progress = %#v, want the two stage lines (sentinel excluded)", progress)
	}
	if result.Previous["daemon"] != "1.0.0" || result.Installed["daemon"] != "1.1.0" {
		t.Fatalf("result versions = %#v", result)
	}
	if len(result.Components) != 1 || result.Components[0].Status != "updated" {
		t.Fatalf("result components = %#v", result.Components)
	}
	if !result.RestartRequested {
		t.Error("RestartRequested = false, want true")
	}
}

// TestCommandInstallerMissingSentinelReturnsEmptyResult: a script that never
// prints the sentinel line (e.g. an operator's older custom script) must not
// crash the parse -- it just reports nothing structured, and progress lines
// are still delivered.
func TestCommandInstallerMissingSentinelReturnsEmptyResult(t *testing.T) {
	script := writeFixtureScript(t, `echo "==> done, no sentinel"`)
	installer := CommandInstaller{Command: []string{script}}
	var progress []string
	result, err := installer.Install(context.Background(), Snapshot{}, InstallMeta{}, func(line string) { progress = append(progress, line) })
	if err != nil {
		t.Fatalf("Install() error = %v", err)
	}
	if len(progress) != 1 || progress[0] != "==> done, no sentinel" {
		t.Fatalf("progress = %#v", progress)
	}
	if result.Previous != nil || result.Installed != nil || result.Components != nil {
		t.Fatalf("result = %#v, want zero value", result)
	}
}

// TestCommandInstallerMalformedSentinelReturnsError: a sentinel line present
// but with invalid JSON is a script bug -- it must surface as an error
// rather than silently reporting an empty (and therefore misleadingly
// "nothing changed") result.
func TestCommandInstallerMalformedSentinelReturnsError(t *testing.T) {
	script := writeFixtureScript(t, `echo 'ARCHIE_UPDATE_RESULT {not valid json}'`)
	installer := CommandInstaller{Command: []string{script}}
	_, err := installer.Install(context.Background(), Snapshot{}, InstallMeta{}, func(string) {})
	if err == nil {
		t.Fatal("Install() error = nil, want a decode error")
	}
}

// TestCommandInstallerNonZeroExitReturnsPartialResultAndError: a script that
// fails partway through should still surface whatever progress and result it
// managed to emit before failing, alongside the error -- callers need to
// know how far the update got.
func TestCommandInstallerNonZeroExitReturnsPartialResultAndError(t *testing.T) {
	script := writeFixtureScript(t, `
echo "==> fetching"
echo "build failed" >&2
exit 1
`)
	installer := CommandInstaller{Command: []string{script}}
	var progress []string
	_, err := installer.Install(context.Background(), Snapshot{}, InstallMeta{}, func(line string) { progress = append(progress, line) })
	if err == nil {
		t.Fatal("Install() error = nil, want non-zero exit error")
	}
	if !strings.Contains(err.Error(), "build failed") {
		t.Errorf("error = %v, want it to include stderr", err)
	}
	if len(progress) != 1 || progress[0] != "==> fetching" {
		t.Fatalf("progress = %#v, want the line emitted before failure", progress)
	}
}

// TestCommandInstallerForwardsMetaAsEnv: the script needs to know who to
// report back to once the daemon restarts, since the process that requested
// the update is not the process that observes the restart succeeding.
func TestCommandInstallerForwardsMetaAsEnv(t *testing.T) {
	script := writeFixtureScript(t, `
echo "channel=$ARCHIE_UPDATE_CHANNEL chat=$ARCHIE_UPDATE_CHAT_ID thread=$ARCHIE_UPDATE_THREAD_ID report=$ARCHIE_UPDATE_REPORT_PATH"
`)
	installer := CommandInstaller{Command: []string{script}}
	meta := InstallMeta{Channel: "telegram", ChatID: 123, ThreadID: 7, ReportPath: "/var/lib/archie/update-report.json"}
	var progress []string
	if _, err := installer.Install(context.Background(), Snapshot{}, meta, func(line string) { progress = append(progress, line) }); err != nil {
		t.Fatalf("Install() error = %v", err)
	}
	want := "channel=telegram chat=123 thread=7 report=/var/lib/archie/update-report.json"
	if len(progress) != 1 || progress[0] != want {
		t.Fatalf("progress = %#v, want [%q]", progress, want)
	}
}

func TestCommandInstallerForwardsApprovedComponentPlanAsEnv(t *testing.T) {
	script := writeFixtureScript(t, `
echo "daemon=$ARCHIE_UPDATE_DAEMON_PREVIOUS->$ARCHIE_UPDATE_DAEMON_VERSION agent=$ARCHIE_UPDATE_AGENT_PREVIOUS->$ARCHIE_UPDATE_AGENT_VERSION"
`)
	installer := CommandInstaller{Command: []string{script}}
	snapshot := Snapshot{Components: []Component{
		{ID: ComponentDaemon, Installed: "1.12.0", Available: "1.13.0"},
		{ID: ComponentAgent, Installed: "1.9.9", Available: "1.9.9"},
	}}
	var progress []string
	if _, err := installer.Install(context.Background(), snapshot, InstallMeta{}, func(line string) { progress = append(progress, line) }); err != nil {
		t.Fatalf("Install() error = %v", err)
	}
	want := "daemon=1.12.0->1.13.0 agent=1.9.9->"
	if len(progress) != 1 || progress[0] != want {
		t.Fatalf("progress = %#v, want [%q]", progress, want)
	}
}

func TestCommandInstallerEmptyCommandReturnsError(t *testing.T) {
	installer := CommandInstaller{}
	if _, err := installer.Install(context.Background(), Snapshot{}, InstallMeta{}, func(string) {}); err == nil {
		t.Fatal("Install() error = nil, want empty-command error")
	}
}
