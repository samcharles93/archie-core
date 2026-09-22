package configtemplate

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/infrastructure/configuration"
)

func readDeploymentFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestExternalNATSProfileLoadsWithManagedWorkers(t *testing.T) {
	doc, err := configuration.New(nil).File("deployments/docker-nats-stack.toml")
	if err != nil {
		t.Fatalf("load external NATS deployment: %v", err)
	}
	if doc.Config.NATS.Mode != config.NATSModeExternal {
		t.Errorf("NATS mode = %q, want %q", doc.Config.NATS.Mode, config.NATSModeExternal)
	}
	if doc.Config.Containers.Image == "" {
		t.Error("managed worker image is empty")
	}
	legacy := doc.Config.LegacyAgent
	if legacy.Mode != "" || legacy.Command != "" || len(legacy.Env) != 0 {
		t.Errorf("legacy agent selector decoded from supported profile: %#v", doc.Config.LegacyAgent)
	}
}

func TestInstallerUsesNativeDaemonAndManagedWorkerImage(t *testing.T) {
	source := readDeploymentFile(t, "install.sh")
	for _, forbidden := range []string{
		"[agent]",
		`go build -o "${ARCHIE_BIN_DIR}/archie-agent"`,
		`- archie-agent`,
		"required only for NATS container mode",
	} {
		if strings.Contains(source, forbidden) {
			t.Errorf("install.sh contains removed deployment behavior %q", forbidden)
		}
	}
	for _, required := range []string{
		`ExecStart=${ARCHIE_BIN_DIR}/archied`,
		"Docker is required for autonomous workflows",
	} {
		if !strings.Contains(source, required) {
			t.Errorf("install.sh is missing deployment contract %q", required)
		}
	}
}

func TestWorkerImageIsNotDocumentedAsAStandaloneExecutor(t *testing.T) {
	source := readDeploymentFile(t, "Dockerfile")
	if strings.Contains(source, "docker run -e NATS_URL") {
		t.Error("Dockerfile documents an unsupported manually started worker")
	}
	if !strings.Contains(source, "launched only by archied") {
		t.Error("Dockerfile does not state that archied owns worker lifecycle")
	}
}

func TestFormattingUsesOneConfiguredWriter(t *testing.T) {
	taskfile := readDeploymentFile(t, "Taskfile.yml")
	fmtStart := strings.Index(taskfile, "  fmt:\n")
	fmtEnd := strings.Index(taskfile, "  proto:install:\n")
	if fmtStart < 0 || fmtEnd < fmtStart {
		t.Fatal("Taskfile has no bounded fmt task")
	}
	fmtTask := taskfile[fmtStart:fmtEnd]

	const (
		applyFix  = "go fix ./..."
		verifyFix = "go fix -diff ./..."
	)
	fix := strings.Index(fmtTask, applyFix)
	format := strings.Index(fmtTask, "golangci-lint fmt")
	if fix < 0 || format < 0 || fix > format {
		t.Error("fmt task must run go fix before the configured golangci-lint formatters")
	}
	if strings.Contains(fmtTask, "gofumpt -") {
		t.Error("fmt task bypasses the configured formatter set with standalone gofumpt")
	}
	// One go fix pass is not a fixpoint: stditerators rewrites
	// `for i := range t.NumField()` into a field iterator that copies its loop
	// variable, and forvar only deletes that copy on the following pass. A tree
	// that stops after one pass is therefore one copyloopvar rejects, so fmt has
	// to keep applying passes and prove the plateau with a dry run that exits
	// clean only when nothing is left to fix.
	if !strings.Contains(fmtTask, verifyFix) {
		t.Error("fmt task does not prove that repeated go fix passes have reached a fixpoint")
	}
	if got := strings.Count(fmtTask, applyFix); got != 1 {
		t.Errorf("fmt task applies go fix %d times inline, want 1: the repetition belongs to the fixpoint loop", got)
	}
	if got := strings.Count(fmtTask, verifyFix); got != 1 {
		t.Errorf("fmt task verifies the go fix fixpoint %d times, want 1", got)
	}
	if got, owned := strings.Count(taskfile, applyFix), strings.Count(fmtTask, applyFix); got != owned {
		t.Errorf("Taskfile invokes go fix %d times, %d of them through task fmt: formatting has one owner", got, owned)
	}
}

func TestBuildTaskSourcesTrackEmbeddedAssets(t *testing.T) {
	taskfile := readDeploymentFile(t, "Taskfile.yml")
	start := strings.Index(taskfile, "\n  build:\n")
	end := strings.Index(taskfile, "\n  test:\n")
	if start < 0 || end < start {
		t.Fatal("Taskfile has no bounded build task")
	}
	build := taskfile[start:end]
	sourcesAt := strings.Index(build, "sources:")
	generatesAt := strings.Index(build, "generates:")
	if sourcesAt < 0 || generatesAt < sourcesAt {
		t.Fatal("build task has no bounded sources block")
	}
	sources := build[sourcesAt:generatesAt]

	for _, tc := range []struct {
		name   string
		path   string
		reason string
	}{
		{
			name:   "embedded dashboard",
			path:   "ui/dist/**",
			reason: "ui/embed.go embeds it with //go:embed all:dist, so a dashboard changing with no .go file changing must still rebuild the binaries",
		},
	} {
		if !strings.Contains(sources, tc.path) {
			t.Errorf("build sources do not track %s: %s", tc.name, tc.reason)
		}
	}
}

func TestBuildTaskStampsInstallTypeOnEveryCommand(t *testing.T) {
	taskfile := readDeploymentFile(t, "Taskfile.yml")
	start := strings.Index(taskfile, "\n  build:\n")
	end := strings.Index(taskfile, "\n  test:\n")
	if start < 0 || end < start {
		t.Fatal("Taskfile has no bounded build task")
	}
	build := taskfile[start:end]
	cmdsAt := strings.Index(build, "\n    cmds:\n")
	if cmdsAt < 0 {
		t.Fatal("build task has no cmds block")
	}
	cmds := build[cmdsAt:]

	buildLine := regexp.MustCompile(`^      - go build .*\./cmd/([a-z0-9-]+)`)
	found := 0
	for line := range strings.SplitSeq(cmds, "\n") {
		m := buildLine.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		found++
		if !strings.Contains(line, "internal/installtype.buildType=") {
			t.Errorf("build task does not stamp installtype.buildType on %s: %s", m[1], strings.TrimSpace(line))
		}
	}
	if found == 0 {
		t.Fatal("build task has no go build commands under ./cmd/: this guard would assert nothing")
	}
}

func TestFormattersExcludeInterpretedSecretEngines(t *testing.T) {
	config := readDeploymentFile(t, ".golangci.yml")
	if !strings.Contains(config, `^examples/secret-engines/(age|sops|vault)\.go$`) {
		t.Fatal("formatter config does not protect the interpreted secret engines")
	}
}

func TestSupportedProfilesUseOneExecutionTopology(t *testing.T) {
	profiles := []string{
		"deployments/single-forge-github.toml",
		"deployments/multi-forge-github-gitea.toml",
		"deployments/local-ollama-standalone.toml",
		"deployments/docker-nats-stack.toml",
	}
	for _, path := range profiles {
		source := readDeploymentFile(t, path)
		if strings.Contains(source, "\n[agent]\n") {
			t.Errorf("%s still selects a legacy agent executor", path)
		}
	}

	for _, path := range profiles[:2] {
		source := readDeploymentFile(t, path)
		for _, required := range []string{`mode = "embedded"`, "[containers]"} {
			if !strings.Contains(source, required) {
				t.Errorf("%s is missing default topology marker %q", path, required)
			}
		}
	}

	external := readDeploymentFile(t, "deployments/docker-nats-stack.toml")
	if !strings.Contains(external, `mode = "external"`) {
		t.Error("docker-nats-stack.toml does not explicitly select external NATS")
	}

	systemd := readDeploymentFile(t, "deployments/systemd-user-service.md")
	if !strings.Contains(systemd, "Embedded NATS is the default") {
		t.Error("systemd runbook does not identify embedded NATS as the default")
	}
	if strings.Contains(systemd, "still needs NATS") {
		t.Error("systemd runbook still requires an external Compose NATS service")
	}
}

// TestInstallerDelegatesConfigGenerationToArchiedSetup pins the contract the
// installer already drifted from once: it hand-rolled a config.toml, hardcoded
// the GitHub token key and the Gitea host, and choosing Gitea then produced a
// config naming a token it never wrote. The durable fix is that the code writing
// the config is the code reading it -- archied setup renders it, archied loads it
// (docs/architecture/configuration.md) -- and nothing stopped the generating
// branch growing back until this.
func TestInstallerDelegatesConfigGenerationToArchiedSetup(t *testing.T) {
	source := readDeploymentFile(t, "install.sh")

	for _, required := range []string{
		// It runs the binary it just built, so the installer cannot hold a
		// second idea of what a valid config looks like.
		`"${ARCHIE_BIN_DIR}/archied" setup`,
		// For a fresh install only: an existing config is the operator's.
		`if [ ! -f "${ARCHIE_CONFIG_DIR}/config.toml" ]; then`,
		"already exists (preserving user config)",
		// The setup run is allowed to fail, and a failure is loud. A config that
		// never gets written is the only honest outcome after that.
		"could not generate",
	} {
		if !strings.Contains(source, required) {
			t.Errorf("install.sh is missing %q", required)
		}
	}

	// Nothing here renders TOML or splices it afterwards. The splice check is
	// per line so a legitimate sed/awk elsewhere in the installer stays allowed.
	if strings.Contains(source, "forge_block") {
		t.Error("install.sh still carries the forge_block helper: it is what hardcoded the GitHub token key and Gitea host")
	}
	redirection := regexp.MustCompile(`>>?\s*"?[^"'\s]*config\.toml`)
	for index, line := range strings.Split(source, "\n") {
		if !strings.Contains(line, "config.toml") {
			continue
		}
		if strings.Contains(line, "sed ") || strings.Contains(line, "awk ") {
			t.Errorf("install.sh:%d splices config.toml (%s)", index+1, strings.TrimSpace(line))
		}
		if redirection.MatchString(line) {
			t.Errorf("install.sh:%d writes config.toml itself (%s)", index+1, strings.TrimSpace(line))
		}
	}
	// A TOML table header in the installer is a template by definition. The
	// systemd unit it also writes is INI, whose only sections are Unit/Service/
	// Install, so those are no part of this list.
	template := regexp.MustCompile(`(?m)^\s*\[(forge|models|providers|runners|containers|nats|web|budgets|notify|identit|chat)\]`)
	if found := template.FindString(source); found != "" {
		t.Errorf("install.sh carries a TOML template (%q): the installer must not hold its own idea of the config schema", strings.TrimSpace(found))
	}
}
