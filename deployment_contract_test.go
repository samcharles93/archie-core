package configtemplate

import (
	"os"
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
