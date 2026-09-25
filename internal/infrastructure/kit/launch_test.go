package kit

import (
	"slices"
	"strings"
	"testing"

	"github.com/docker/sandbox-kit-spec/v3/spec"
)

const launchKit = `
  - type: com.docker.sandbox/agent-sessions@1
    config:
      prompt: [-p, "{{.Prompt}}"]
      resume: [--resume, "{{.SessionID}}"]
  - type: com.docker.sandbox/network-policy@1
    config: {install: {allow: [registry.example.com]}, runtime: {allow: [api.example.com]}}
  - type: com.docker.sandbox/credential@1
    config:
      service: example
      phase: runtime
      apiKey: {name: EXAMPLE_KEY, proxyManaged: true, inject: [{domain: api.example.com, header: x-api-key, format: "%s"}]}
  - type: com.docker.sandbox/volume@1
    config: {path: /home/agent/.cli/sessions, size: 1g}
  - type: com.docker.sandbox/volume@1
    config: {path: /home/agent/.cli/history}
  - type: com.docker.sandbox/lifecycle@1
    config:
      install:
        - command: echo "$WORKSPACE_DIR" > /etc/seeded
          env: [WORKSPACE_DIR]
      startup:
        - command: [cli, warm-up]
          env: [SBX_CRED_EXAMPLE_MODE, NOT_A_RUNTIME_VARIABLE]
        - command: [cli, daemon]
          user: "0"
          background: true`

func plan(t *testing.T) *Plan {
	t.Helper()
	p, err := Compose([]spec.Contribution{{Reference: "kit", Descriptor: descriptor(t, spec.KindWorkload, launchKit)}})
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	return p
}

var image = ImageConfig{
	Entrypoint: []string{"/usr/local/bin/cli"},
	Cmd:        []string{"--no-color"},
	Env:        []string{"PATH=/usr/local/bin:/usr/bin:/bin", "HOME=/home/agent", "IMAGE_ONLY=kept"},
	User:       "agent",
}

var params = LaunchParams{Execution: "exec-42", ProxyToken: "tok", CAPath: "/etc/archie/ca.pem", Bound: []string{"example"}}

func TestAssembleHarness(t *testing.T) {
	l, err := Assemble(plan(t), image, params)
	if err != nil {
		t.Fatal(err)
	}
	h := l.Harness
	if !slices.Equal(h.Launch, []string{"/usr/local/bin/cli", "--no-color"}) {
		t.Errorf("launch %v, want the image entrypoint then cmd", h.Launch)
	}
	if !slices.Equal(h.Prompt, []string{"-p", "{{.Prompt}}"}) || !slices.Equal(h.Resume, []string{"--resume", "{{.SessionID}}"}) {
		t.Errorf("verbs prompt=%v resume=%v, want the Kit's agent-sessions verbs", h.Prompt, h.Resume)
	}
	if h.User != "agent" {
		t.Errorf("user %q, want the image's user", h.User)
	}
	for _, want := range []string{
		"PATH=/usr/local/bin:/usr/bin:/bin", "IMAGE_ONLY=kept",
		"WORKSPACE_DIR=/workspace",
		"HTTPS_PROXY=http://archie:tok@archie-egress:3128",
		"SSL_CERT_FILE=/etc/archie/ca.pem",
		"EXAMPLE_KEY=archie-proxy-managed",
	} {
		if !slices.Contains(h.Env, want) {
			t.Errorf("harness env lacks %q:\n%s", want, strings.Join(h.Env, "\n"))
		}
	}
}

func TestAssembleNeverRunsTheHarnessAsRoot(t *testing.T) {
	for _, user := range []string{"", "root", "0", "0:0"} {
		img := image
		img.User = user
		l, err := Assemble(plan(t), img, params)
		if err != nil {
			t.Fatal(err)
		}
		if l.Harness.User != DefaultHarnessUser {
			t.Errorf("image user %q gave harness user %q, want %q", user, l.Harness.User, DefaultHarnessUser)
		}
	}
}

func TestAssembleHooks(t *testing.T) {
	l, err := Assemble(plan(t), image, params)
	if err != nil {
		t.Fatal(err)
	}
	if len(l.Install) != 1 || len(l.Startup) != 2 {
		t.Fatalf("install %d startup %d, want 1 and 2", len(l.Install), len(l.Startup))
	}
	install := l.Install[0]
	if !slices.Equal(install.Argv, []string{"sh", "-c", `echo "$WORKSPACE_DIR" > /etc/seeded`}) {
		t.Errorf("a string command runs through the shell, as the Kit spec decodes it; got %v", install.Argv)
	}
	if install.User != "0" {
		t.Errorf("install hook user %q, want root by default", install.User)
	}
	if !slices.Contains(install.Env, "WORKSPACE_DIR=/workspace") || !slices.Contains(install.Env, "HTTPS_PROXY=http://archie:tok@archie-egress:3128") {
		t.Errorf("install env %v, want its declared variable and the egress baseline", install.Env)
	}
	warm := l.Startup[0]
	if warm.User != DefaultHarnessUser {
		t.Errorf("startup hook user %q, want the agent user by default", warm.User)
	}
	if !slices.Contains(warm.Env, "SBX_CRED_EXAMPLE_MODE=apikey") {
		t.Errorf("startup env %v, want the bound credential's mode", warm.Env)
	}
	for _, e := range warm.Env {
		if strings.HasPrefix(e, "NOT_A_RUNTIME_VARIABLE=") || strings.HasPrefix(e, "IMAGE_ONLY=") || strings.HasPrefix(e, "EXAMPLE_KEY=") {
			t.Errorf("startup env carries %q, which it neither declared nor is baseline", e)
		}
	}
	if daemon := l.Startup[1]; daemon.User != "0" || !daemon.Background {
		t.Errorf("startup hook %+v, want its declared root user and background flag", daemon)
	}
}

func TestAssembleVolumesArePerExecution(t *testing.T) {
	a, err := Assemble(plan(t), image, params)
	if err != nil {
		t.Fatal(err)
	}
	again, _ := Assemble(plan(t), image, params)
	other := params
	other.Execution = "exec-43"
	b, _ := Assemble(plan(t), image, other)
	if len(a.Volumes) != 2 {
		t.Fatalf("volumes %v, want 2", a.Volumes)
	}
	if !slices.Equal(a.Volumes, again.Volumes) {
		t.Error("volume names are not stable for one execution; a retry would lose its sessions")
	}
	if a.Volumes[0].Name == a.Volumes[1].Name {
		t.Error("two paths share a volume")
	}
	for i := range a.Volumes {
		if a.Volumes[i].Name == b.Volumes[i].Name {
			t.Errorf("executions share volume %q", a.Volumes[i].Name)
		}
	}
}
