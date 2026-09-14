package configtemplate

import (
	"os"
	"path/filepath"
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

// selfHostConfig reconstructs the config.toml the install.sh self-host branch
// writes through its hand-rolled heredoc, with the shell expansions resolved,
// so tests can parse exactly what a fresh self-host install would produce.
func selfHostConfig(t *testing.T) string {
	t.Helper()
	source := readDeploymentFile(t, "install.sh")
	const marker = `cat <<EOF > "${ARCHIE_CONFIG_DIR}/config.toml"`
	start := strings.Index(source, marker)
	if start < 0 {
		t.Fatalf("install.sh is missing the self-host config heredoc marker %q", marker)
	}
	bodyStart := strings.IndexByte(source[start:], '\n')
	if bodyStart < 0 {
		t.Fatal("install.sh self-host config heredoc is malformed")
	}
	bodyStart += start + 1
	lines := strings.Split(source[bodyStart:], "\n")
	var body strings.Builder
	for _, line := range lines {
		if line == "EOF" {
			break
		}
		body.WriteString(line)
		body.WriteByte('\n')
	}
	out := body.String()
	// install.sh expands these two shell constructs inside the heredoc; pin
	// concrete values here so the reconstructed document parses. The forge
	// block is the GitHub output forge_block prints (install.sh's own function).
	out = strings.Replace(out, "$(forge_block)",
		"[forge]\ntype = \"github\"\nhost = \"https://github.com\"\ntoken = { engine = \"env\", key = \"ARCHIE_GITHUB_TOKEN\" }\n\n", 1)
	out = strings.ReplaceAll(out, "ollama/${OLLAMA_MODEL}", "ollama/llama3")
	return out
}

func TestInstallerGeneratedConfigHasServiceTargets(t *testing.T) {
	// Non-self-host branch: install.sh replaces only the [forge] block in
	// config.example.toml, so every other section -- including the required
	// [services.*] targets -- must already be active there.
	example, err := configuration.New(nil).File("config.example.toml")
	if err != nil {
		t.Fatalf("load config.example.toml: %v", err)
	}
	if example.Config.Services.State.Target == "" {
		t.Error("config.example.toml leaves services.state.target empty; the install.sh awk branch would emit an unbootable config")
	}
	exampleSource := readDeploymentFile(t, "config.example.toml")
	if !strings.Contains(exampleSource, "\n[services.gateway]\n") {
		t.Error("config.example.toml does not declare an active [services.gateway] section")
	}

	// Self-host branch: install.sh writes a hand-rolled heredoc. Reconstruct
	// it the way the installer would and require it to parse with the State
	// Store target the daemon's openStateStoreAdapter enforces.
	selfHost := selfHostConfig(t)
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(selfHost), 0o600); err != nil {
		t.Fatal(err)
	}
	generated, err := configuration.New(nil).File(path)
	if err != nil {
		t.Fatalf("load reconstructed self-host install config: %v", err)
	}
	if generated.Config.Services.State.Target == "" {
		t.Error("install.sh self-host heredoc leaves services.state.target empty; generated config cannot boot")
	}
	if !strings.Contains(selfHost, "\n[services.gateway]\n") {
		t.Error("install.sh self-host heredoc does not declare an active [services.gateway] section")
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
