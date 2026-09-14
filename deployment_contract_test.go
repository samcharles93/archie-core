package configtemplate

import (
	"os"
	"path/filepath"
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

// heredocRe matches any `cat <<DELIM` heredoc opener in install.sh, tolerating
// <<- and a quoted delimiter. The redirect target is deliberately NOT matched:
// the config writer may name it inline, put the redirect before the delimiter,
// or factor the path into a variable, and none of those change what is written.
var heredocRe = regexp.MustCompile("(?m)^[^\\n]*\\bcat\\b[^\\n]*?<<-?[ \\t]*['\"]?([A-Za-z_][A-Za-z0-9_]*)['\"]?[ \\t]*")

// configHeredocs returns the body of every install.sh heredoc that declares the
// daemon config's [nats] table, which the systemd unit heredoc does not. The
// selector is a table the assertions below never touch, so it stays structural
// rather than becoming a search for the field under test.
//
// Command substitutions are dropped rather than expanded. Substituting a
// hand-written forge block here would duplicate install.sh's own forge_block
// output, so the test would assert against a copy that can silently diverge from
// what the installer writes; the loader defaults what their absence omits. Plain
// ${VAR} references are left in place: they are ordinary string contents once
// parsed, and removing them would empty the [models] table for no benefit.
func configHeredocs(t *testing.T) []string {
	t.Helper()
	source := readDeploymentFile(t, "install.sh")
	var out []string
	for _, m := range heredocRe.FindAllStringSubmatchIndex(source, -1) {
		delim := source[m[2]:m[3]]
		rest := source[m[1]:]
		_, after, ok := strings.Cut(rest, "\n")
		if !ok {
			continue
		}
		var body strings.Builder
		for line := range strings.SplitSeq(after, "\n") {
			if strings.TrimRight(strings.TrimLeft(line, "\t"), "\r") == delim {
				break
			}
			if strings.Contains(line, "$(") {
				continue // a command substitution install.sh resolves at run time
			}
			body.WriteString(line)
			body.WriteByte('\n')
		}
		if b := body.String(); strings.Contains(b, "\n[nats]") {
			out = append(out, b)
		}
	}
	if len(out) == 0 {
		t.Fatal("install.sh has no heredoc declaring the daemon config's [nats] table")
	}
	return out
}

func TestInstallerGeneratedConfigHasServiceTargets(t *testing.T) {
	// Non-self-host branch: install.sh replaces only the [forge] block in
	// config.example.toml, so every other section -- including the required
	// [services.state] target -- must already be active there.
	example, err := configuration.New(nil).File("config.example.toml")
	if err != nil {
		t.Fatalf("load config.example.toml: %v", err)
	}
	if example.Config.Services.State.Target == "" {
		t.Error("config.example.toml leaves services.state.target empty; the install.sh awk branch would emit an unbootable config")
	}

	// Self-host branch: install.sh writes the config through its own heredoc.
	// Reconstruct every such heredoc and require each to parse with the State
	// Store target the daemon's openStateStoreAdapter enforces.
	for _, body := range configHeredocs(t) {
		path := filepath.Join(t.TempDir(), "config.toml")
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		generated, err := configuration.New(nil).File(path)
		if err != nil {
			t.Fatalf("load reconstructed self-host install config: %v", err)
		}
		if generated.Config.Services.State.Target == "" {
			t.Error("install.sh config heredoc leaves services.state.target empty; generated config cannot boot")
		}
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
