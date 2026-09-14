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
