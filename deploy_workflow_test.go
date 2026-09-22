package configtemplate

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func TestDeployWorkflowOnlyPublishesRuntimeImageForRuntimeTag(t *testing.T) {
	source := readDeploymentFile(t, ".github/workflows/deploy.yml")

	// The fallback remains necessary because Dockerfile.archied accepts a
	// runtime version, but it must not be used as the image-publish selector.
	for _, required := range []string{
		`RUNTIME_TAG="$(git tag --points-at HEAD --list 'archie/v*' | sort -V | tail -1)"`,
		`echo "runtime_tag=$RUNTIME_TAG" >> "$GITHUB_OUTPUT"`,
		"if: steps.versions.outputs.runtime_tag != ''",
	} {
		if !strings.Contains(source, required) {
			t.Errorf("deploy workflow is missing runtime-image guard %q", required)
		}
	}

	// The gateway build remains unconditional, while the runtime build is
	// guarded. This verifies both sides of the gateway-only/dual-tag contract.
	agentMarker := "      - name: Build and push archie-agent\n"
	agentStart := strings.Index(source, agentMarker)
	if agentStart < 0 {
		t.Fatal("deploy workflow has no archie-agent build step")
	}
	if !strings.Contains(source[agentStart:], agentMarker+"        if: steps.versions.outputs.runtime_tag != ''\n") {
		t.Fatal("archie-agent build step is not conditionally enabled")
	}

	gatewayMarker := "      - name: Build and push archied\n"
	gatewayStart := strings.Index(source, gatewayMarker)
	if gatewayStart < 0 {
		t.Fatal("deploy workflow has no archied build step")
	}
	if strings.Contains(source[gatewayStart:agentStart], "\n        if:") {
		t.Fatal("archied build step is unexpectedly conditional")
	}
}

func TestDeployWorkflowUsesConfiguredFormatters(t *testing.T) {
	source := readDeploymentFile(t, ".github/workflows/deploy.yml")
	for _, required := range []string{
		"github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.13.2",
		"golangci-lint fmt",
		"git diff --exit-code",
		"golangci-lint run ./...",
	} {
		if !strings.Contains(source, required) {
			t.Errorf("deploy workflow is missing formatter gate %q", required)
		}
	}
	if strings.Contains(source, "gofumpt -w .") {
		t.Error("deploy workflow bypasses the configured formatter set")
	}
}

func TestPullRequestsRunTheDefinitiveGateWithoutPublishAccess(t *testing.T) {
	source := readDeploymentFile(t, ".github/workflows/quality.yml")
	for _, required := range []string{
		"pull_request:",
		"contents: read",
		"task check",
		"git diff --exit-code",
	} {
		if !strings.Contains(source, required) {
			t.Errorf("quality workflow is missing %q", required)
		}
	}
	for _, forbidden := range []string{
		"packages: write",
		"docker/login-action",
		"docker/build-push-action",
	} {
		if strings.Contains(source, forbidden) {
			t.Errorf("quality workflow has publishing capability %q", forbidden)
		}
	}
}

// unpackagedCommands are commands that deliberately never ship as a host binary.
// Naming each one with its reason keeps an omission a decision rather than an
// oversight -- which is exactly how archied's channels were lost.
var unpackagedCommands = map[string]string{
	"archie-agent": "runs only inside the Docker image build-and-push publishes, never as a host binary",
}

// cliOnlyCommands ship in the zip but must never be restarted by the updater:
// they are not services.
var cliOnlyCommands = map[string]bool{"archie-playbooks": true}

// TestDistZipShipsEveryHostCommand is why archie-core-1c01 went unnoticed for
// two days. The v1.30.0 extraction moved Telegram, email and webhook out of
// archied into a new cmd/archie-messaging that nothing packaged: the release zip
// omitted it, scripts/archie-update-install omitted it from both of its lists,
// and install.sh's build loop omitted it too, so a host install ran on with a
// dead Telegram bot and no error logged anywhere. The daemon looked healthy
// throughout.
//
// The host component set was enumerated independently in four places (the zip's
// build loop, the zip's copy list, install.sh's build loop, and the updater's
// two lists), so two coordinated edits would have drifted again. Every list is
// pinned to cmd/ here instead: adding a command fails this test until it is
// packaged, and extracting one fails it until the previous command is removed.
// The updater's service list is separately required to have a documented unit,
// and its runtime guard refuses a host unit the list omits.
func TestDistZipShipsEveryHostCommand(t *testing.T) {
	workflow := readDeploymentFile(t, ".github/workflows/deploy.yml")
	installer := readDeploymentFile(t, filepath.Join("scripts", "archie-update-install"))
	installSh := readDeploymentFile(t, "install.sh")

	want := hostRunCommands(t)
	wantBinaries := want
	wantServices := withoutCLIOnly(want)

	built := wordsAfter(t, workflow, "for cmd in ", "; do", nil)
	// The marker consumes the prefix of the first word only, so restore it for
	// whichever word the marker ended inside.
	var shipped []string
	for _, word := range wordsAfter(t, workflow, "cp dist/", ` "$name/"`, nil) {
		if !strings.HasPrefix(word, "dist/") {
			word = "dist/" + word
		}
		shipped = append(shipped, strings.TrimPrefix(word, "dist/"))
	}
	services := wordsAfter(t, installer, `GATEWAY_SERVICES="`, `"`, nil)
	binaries := wordsAfter(t, installer, `GATEWAY_BINARIES="`, `"`, map[string][]string{"$GATEWAY_SERVICES": services})
	installBuilt := installBuildCommands(t, installSh)

	for _, list := range []struct {
		name string
		got  []string
		want []string
	}{
		{"deploy.yml dist-zip build loop", built, wantBinaries},
		{"deploy.yml dist-zip copy list", shipped, wantBinaries},
		{"install.sh build loop", installBuilt, wantBinaries},
		{"archie-update-install GATEWAY_BINARIES", binaries, wantBinaries},
		{"archie-update-install GATEWAY_SERVICES", services, wantServices},
	} {
		if diff := setDifference(list.want, list.got); diff != "" {
			t.Errorf("%s does not match the host-run commands in cmd/: %s", list.name, diff)
		}
	}

	// The installer now REFUSES an update when a service unit is missing and
	// tells the operator to create it from this runbook, so a service the
	// runbook never shows is a dead end rather than a documentation gap.
	// Stamping is its own list, and a third build site made it three: the zip,
	// install.sh and the updater each build the host binaries. A binary that
	// cannot say what it is cannot be compared (archie-core-k94o), so a seventh
	// command added unstamped fails here rather than waiting to be noticed.
	for name, source := range map[string]string{
		"deploy.yml":            workflow,
		"install.sh":            installSh,
		"archie-update-install": installer,
	} {
		if !strings.Contains(source, "internal/buildinfo.Version=") {
			t.Errorf("%s does not stamp internal/buildinfo.Version", name)
		}
	}
	for _, command := range wantBinaries {
		// archied's flag lives in the app package it delegates to, not its thin
		// cmd main; the rest report through buildinfo in their own main.
		source := filepath.Join("cmd", command, "main.go")
		if command == "archied" {
			source = filepath.Join("internal", "app", "archied", "main.go")
		}
		main := readDeploymentFile(t, source)
		if !strings.Contains(main, "buildinfo") && !strings.Contains(main, "-version") && !strings.Contains(main, "\"version\"") {
			t.Errorf("%s has no way to report its version", source)
		}
	}

	// The installer writes the units, so the runbook no longer carries their
	// contents; it must still name each unit, or an operator cannot find one to
	// inspect or override.
	runbook := readDeploymentFile(t, filepath.Join("deployments", "systemd-user-service.md"))
	var undocumented []string
	for _, service := range wantServices {
		if !strings.Contains(runbook, service+".service") {
			undocumented = append(undocumented, service)
		}
	}
	if len(undocumented) > 0 {
		t.Errorf("deployments/systemd-user-service.md names no unit for: %s", strings.Join(undocumented, " "))
	}
}

// installBuildCommands reads install.sh's build loop. The file has a second
// `for cmd in` (the git/go preflight), so the loop is located by the go build it
// drives rather than by the first marker -- and read from the real list, so a
// missing command is named instead of the marker merely going absent.
func installBuildCommands(t *testing.T, source string) []string {
	t.Helper()
	before, _, ok := strings.Cut(source, `go build -ldflags "${LDFLAGS}"`)
	if !ok {
		t.Fatal("install.sh has no go build loop to read")
	}
	prefix := before
	start := strings.LastIndex(prefix, "for cmd in ")
	if start < 0 {
		t.Fatal("install.sh build loop has no for cmd list")
	}
	rest := prefix[start+len("for cmd in "):]
	before0, _, ok0 := strings.Cut(rest, "; do")
	if !ok0 {
		t.Fatal("install.sh build loop is unterminated")
	}
	return strings.Fields(before0)
}

// hostRunCommands is every command under cmd/, minus the deliberately
// unpackaged ones. Deriving it is the point: a list written out here would drift
// from cmd/ exactly as the packaged lists did.
func hostRunCommands(t *testing.T) []string {
	t.Helper()
	entries, err := os.ReadDir("cmd")
	if err != nil {
		t.Fatalf("read cmd/: %v", err)
	}
	var commands []string
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if _, skip := unpackagedCommands[entry.Name()]; skip {
			continue
		}
		commands = append(commands, entry.Name())
	}
	if len(commands) == 0 {
		t.Fatal("cmd/ holds no commands: this guard would assert nothing")
	}
	sort.Strings(commands)
	return commands
}

func withoutCLIOnly(commands []string) []string {
	out := make([]string, 0, len(commands))
	for _, command := range commands {
		if cliOnlyCommands[command] {
			continue
		}
		out = append(out, command)
	}
	return out
}

// wordsAfter returns the whitespace-separated words between two markers on the
// same line. expand maps a word to the words it stands for, which is how the
// installer's "$GATEWAY_SERVICES" reference is followed to what it names.
func wordsAfter(t *testing.T, source, marker, terminator string, expand map[string][]string) []string {
	t.Helper()
	start := strings.Index(source, marker)
	if start < 0 {
		t.Fatalf("no %q in the file under test", marker)
	}
	rest := source[start+len(marker):]
	end := strings.Index(rest, terminator)
	if end < 0 {
		t.Fatalf("no %q after %q", terminator, marker)
	}
	var words []string
	for word := range strings.FieldsSeq(rest[:end]) {
		if replacement, ok := expand[word]; ok {
			words = append(words, replacement...)
			continue
		}
		words = append(words, word)
	}
	return words
}

// setDifference names what is missing from and extra in got, so a failure says
// which side moved rather than that two lists differ.
func setDifference(want, got []string) string {
	present := make(map[string]bool, len(got))
	for _, item := range got {
		present[item] = true
	}
	var missing []string
	for _, item := range want {
		if !present[item] {
			missing = append(missing, item)
		}
	}
	expected := make(map[string]bool, len(want))
	for _, item := range want {
		expected[item] = true
	}
	var extra []string
	for _, item := range got {
		if !expected[item] {
			extra = append(extra, item)
		}
	}
	sort.Strings(missing)
	sort.Strings(extra)
	var problems []string
	if len(missing) > 0 {
		problems = append(problems, "missing "+strings.Join(missing, " "))
	}
	if len(extra) > 0 {
		problems = append(problems, "unexpected "+strings.Join(extra, " "))
	}
	return strings.Join(problems, "; ")
}
