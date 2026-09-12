package releaseupdate

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// checkRun configures one end-to-end run of archie-update-check. The fake git
// and curl placed on PATH are what make the test exercise the script's real
// source-selection logic rather than the network.
type checkRun struct {
	// git is the fake `git` body. Empty leaves git off PATH entirely, which
	// is how the API-only path is reached.
	git string
	// curl is the fake `curl` body. Empty means curl always fails, so the
	// git-tag path is reached.
	curl string
	// binVersion is what the fake archied reports for -version.
	binVersion string
}

func runUpdateCheckScript(t *testing.T, run checkRun) (Snapshot, []string, string, error) {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	work := t.TempDir()
	binDir := filepath.Join(work, "bin")
	fakeDir := filepath.Join(work, "fake-bin")
	callsPath := filepath.Join(work, "calls")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	version := run.binVersion
	if version == "" {
		version = "1.22.0"
	}
	writeFakeCommand(t, binDir, "archied", `printf 'archied %s\narchie-agent %s\n' "`+version+`" "1.19.9"`)
	if run.git != "" {
		writeFakeCommand(t, fakeDir, "git", run.git)
	}
	if run.curl != "" {
		writeFakeCommand(t, fakeDir, "curl", run.curl)
	}

	cmd := exec.CommandContext(t.Context(), filepath.Join(root, "scripts", "archie-update-check"))
	cmd.Env = append(
		os.Environ(),
		"PATH="+fakeDir+":"+binDir+":"+os.Getenv("PATH"),
		"ARCHIE_BIN="+filepath.Join(binDir, "archied"),
		"ARCHIE_TEST_CALLS="+callsPath,
		// A remote that cannot be reached in this environment: anything the
		// script resolves came from a fake, never from the real network.
		"ARCHIE_SOURCE_URL=https://example.invalid/archie-core.git",
		"ARCHIE_FORGE_API=https://api.example.invalid",
		"ARCHIE_FORGE_TIMEOUT=5",
	)
	output, err := cmd.Output()
	var stderr string
	if exitErr, ok := err.(*exec.ExitError); ok {
		stderr = string(exitErr.Stderr)
	}
	var calls []string
	if data, readErr := os.ReadFile(callsPath); readErr == nil && len(data) > 0 {
		calls = strings.Split(strings.TrimSpace(string(data)), "\n")
	}
	if err != nil {
		return Snapshot{}, calls, stderr, err
	}
	var snapshot Snapshot
	if err := json.Unmarshal(output, &snapshot); err != nil {
		t.Fatalf("decode check output %q: %v", output, err)
	}
	return snapshot, calls, stderr, nil
}

// The update check must not depend on a local checkout existing. It used to
// resolve tags with `git -C "$REPO" ls-remote`, so a host with no clone --
// or a clone under a different name -- silently reported no available
// releases, which the /update command then rendered as "Archie is up to
// date." This is the failure test for that: it must resolve from the remote
// alone, and must not shell into any working tree.
func TestUpdateCheckNeedsNoLocalCheckout(t *testing.T) {
	snapshot, calls, _, err := runUpdateCheckScript(t, checkRun{
		git: `
printf '%s\n' "git $*" >> "$ARCHIE_TEST_CALLS"
case "$*" in
  *archied/v*) printf '%s\n' refs/tags/archied/v1.22.0 refs/tags/archied/v1.23.0 ;;
  *archie/v*)  printf '%s\n' refs/tags/archie/v1.21.0 ;;
esac
`,
	})
	if err != nil {
		t.Fatalf("check failed: %v", err)
	}
	for _, fragment := range []string{"ls-remote", "example.invalid/archie-core.git"} {
		assertCallContains(t, calls, fragment)
	}
	// -C is how the old script targeted a checkout. Any use of it means the
	// checkout dependency came back.
	assertCallAbsent(t, calls, "git -C")
	assertCallAbsent(t, calls, "clone")

	if got, want := componentAvailable(snapshot, ComponentDaemon), "1.23.0"; got != want {
		t.Errorf("daemon available = %q, want %q", got, want)
	}
	if got, want := componentAvailable(snapshot, ComponentAgent), "1.21.0"; got != want {
		t.Errorf("agent available = %q, want %q", got, want)
	}
}

// A source that cannot be reached is a failure to check, not an absence. The
// old script exited 0 with an empty Available for every component, which is
// indistinguishable from "up to date" -- so an unreachable remote, a deleted
// checkout, or a renamed repo all read as parity. It must instead refuse.
func TestUpdateCheckFailsRatherThanReportingParity(t *testing.T) {
	_, _, stderr, err := runUpdateCheckScript(t, checkRun{
		git: `printf '%s\n' "git $*" >> "$ARCHIE_TEST_CALLS"; exit 128`,
	})
	if err == nil {
		t.Fatal("check succeeded with every source unreachable; an empty result reads as \"up to date\"")
	}
	for _, fragment := range []string{"example.invalid/archie-core.git", "api.example.invalid"} {
		if !strings.Contains(stderr, fragment) {
			t.Errorf("failure must name the source it could not read (%s); stderr = %q", fragment, stderr)
		}
	}
}

// With git unavailable, the forge API is the fallback source. It has to
// resolve the same release the git path would, or a host without git can
// never be told an update exists.
func TestUpdateCheckFallsBackToForgeAPITags(t *testing.T) {
	snapshot, _, _, err := runUpdateCheckScript(t, checkRun{
		curl: `
printf '%s\n' "curl $*" >> "$ARCHIE_TEST_CALLS"
case "$*" in
  *"/tags"*)
    printf '%s\n' '[{"name":"archied/v1.22.0"},{"name":"archied/v1.24.0"},{"name":"archie/v1.23.0"}]'
    ;;
  *) exit 22 ;;
esac
`,
	})
	if err != nil {
		t.Fatalf("check failed: %v", err)
	}
	if got, want := componentAvailable(snapshot, ComponentDaemon), "1.24.0"; got != want {
		t.Errorf("daemon available = %q, want %q", got, want)
	}
	if got, want := componentAvailable(snapshot, ComponentAgent), "1.23.0"; got != want {
		t.Errorf("agent available = %q, want %q", got, want)
	}
	if ref := componentReference(snapshot, ComponentDaemon); !strings.Contains(ref, "forge-api-tags") {
		t.Errorf("daemon reference = %q, want the API source recorded", ref)
	}
}

// A tagged release is announced whether or not it has a published artifact,
// and the Reference says which, so an operator is never sent to a tag with
// nothing to download without being told.
func TestUpdateCheckReportsArtifactState(t *testing.T) {
	tests := []struct {
		name      string
		release   string
		wantState string
	}{
		{name: "published with assets", release: `{"assets":[{"name":"zip"},{"name":"SHA256SUMS"}]}`, wantState: "2-assets"},
		{name: "tagged but never published", release: `{"assets":[]}`, wantState: "no-artifact"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			snapshot, _, _, err := runUpdateCheckScript(t, checkRun{
				git: `
printf '%s\n' "git $*" >> "$ARCHIE_TEST_CALLS"
case "$*" in
  *archied/v*) printf '%s\n' refs/tags/archied/v1.24.0 ;;
  *archie/v*)  printf '%s\n' refs/tags/archie/v1.23.0 ;;
esac
`,
				curl: `
printf '%s\n' "curl $*" >> "$ARCHIE_TEST_CALLS"
case "$*" in
  *"/releases/tags/"*) printf '%s\n' '` + tt.release + `' ;;
  *) exit 22 ;;
esac
`,
			})
			if err != nil {
				t.Fatalf("check failed: %v", err)
			}
			for _, id := range []string{ComponentDaemon, ComponentAgent} {
				ref := componentReference(snapshot, id)
				if !strings.Contains(ref, tt.wantState) {
					t.Errorf("%s reference = %q, want artifact state %q", id, ref, tt.wantState)
				}
			}
		})
	}
}

func componentAvailable(snapshot Snapshot, id string) string {
	for _, component := range snapshot.Components {
		if component.ID == id {
			return component.Available
		}
	}
	return ""
}

func componentReference(snapshot Snapshot, id string) string {
	for _, component := range snapshot.Components {
		if component.ID == id {
			return component.Reference
		}
	}
	return ""
}
