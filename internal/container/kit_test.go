package container

import (
	"context"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/docker/sandbox-kit-spec/v3/spec"
	"github.com/moby/moby/client"

	"github.com/samcharles93/archie-core/internal/agentexec"
	"github.com/samcharles93/archie-core/internal/infrastructure/kit"
)

const kitProbeImage = "alpine:3"

func kitClient(t *testing.T) *client.Client {
	t.Helper()
	cli, err := client.New(client.FromEnv)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cli.Close() })
	if _, err := cli.ImageInspect(t.Context(), kitProbeImage); err != nil {
		rc, err := cli.ImagePull(t.Context(), kitProbeImage, client.ImagePullOptions{})
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.Copy(io.Discard, rc)
		_ = rc.Close()
	}
	return cli
}

func kitSpec(t *testing.T, install ...kit.Hook) KitSpec {
	t.Helper()
	name := fmt.Sprintf("archie-kit-test-%d", time.Now().UnixNano())
	overwrite := false
	return KitSpec{
		Name:      name,
		Image:     kitProbeImage,
		Network:   "none",
		Worker:    []string{"sleep", "300"},
		WorkerEnv: []string{"STATE_STORE_TOKEN=worker-secret", "NATS_TOKEN=worker-secret"},
		Labels:    map[string]string{"archie-kit-test": name},
		Launch: kit.Launch{
			Harness: agentexec.HarnessSpec{User: kit.DefaultHarnessUser},
			Install: install,
			Startup: []kit.Hook{{Argv: []string{"sh", "-c", "id -u > /tmp/startup-uid"}, User: kit.DefaultHarnessUser}},
			Files: []spec.File{
				{Path: "/tmp/kit/settings.json", Content: `{"theme":1}`, Mode: "0600"},
				{Path: "/tmp/kit/keep.json", Content: "second", Overwrite: &overwrite},
			},
			Volumes: []kit.Volume{{Name: name + "-state", Path: "/data"}},
		},
	}
}

func startKit(t *testing.T, cli *client.Client, s KitSpec) string {
	t.Helper()
	id, err := StartKit(t.Context(), cli, s)
	t.Cleanup(func() {
		ctx := context.WithoutCancel(t.Context())
		if id != "" {
			_, _ = cli.ContainerRemove(ctx, id, client.ContainerRemoveOptions{Force: true})
		}
		_ = RemoveKitVolumes(ctx, cli, s.Launch.Volumes)
	})
	if err != nil {
		t.Fatalf("StartKit: %v", err)
	}
	return id
}

func run(t *testing.T, cli *client.Client, id, user string, argv ...string) (int, string) {
	t.Helper()
	code, out, err := ExecIn(t.Context(), cli, id, user, []string{"PATH=/usr/bin:/bin"}, argv)
	if err != nil {
		t.Fatal(err)
	}
	return code, out
}

// TestKitContainerSeparatesTheHarnessFromTheWorker proves the one-container
// design's boundary on a real container: the worker's credentials are in
// its environment, and the harness user cannot read them.
func TestKitContainerSeparatesTheHarnessFromTheWorker(t *testing.T) {
	cli := kitClient(t)
	id := startKit(t, cli, kitSpec(t, kit.Hook{
		Argv: []string{"sh", "-c", `echo "$WORKSPACE_DIR" > /etc/seeded; echo first > /tmp/kit-keep-seed`},
		User: "0", Env: []string{"WORKSPACE_DIR=/workspace"},
	}))

	inspected, err := cli.ContainerInspect(t.Context(), id, client.ContainerInspectOptions{})
	if err != nil {
		t.Fatal(err)
	}
	workerUser := inspected.Container.Config.User
	if code, out := run(t, cli, id, workerUser, "cat", "/proc/1/environ"); code != 0 || !strings.Contains(out, "worker-secret") {
		t.Fatalf("the worker's own user cannot see its environment (%d %q); the probe below would prove nothing", code, out)
	}
	if code, out := run(t, cli, id, kit.DefaultHarnessUser, "cat", "/proc/1/environ"); code == 0 || strings.Contains(out, "worker-secret") {
		t.Fatalf("the harness user read the worker's environment: %q", out)
	}

	if _, out := run(t, cli, id, "0", "cat", "/etc/seeded"); out != "/workspace" {
		t.Errorf("install hook did not run as root with its declared env: /etc/seeded = %q", out)
	}
	if _, out := run(t, cli, id, "0", "cat", "/tmp/startup-uid"); out != kit.DefaultHarnessUser {
		t.Errorf("startup hook ran as uid %q, want %s", out, kit.DefaultHarnessUser)
	}
	if _, out := run(t, cli, id, "0", "stat", "-c", "%u %a", "/tmp/kit/settings.json"); out != kit.DefaultHarnessUser+" 600" {
		t.Errorf("kit file owner and mode %q, want the harness user and 600", out)
	}
	if _, out := run(t, cli, id, "0", "cat", "/tmp/kit/keep.json"); out != "second" {
		t.Errorf("kit file content %q", out)
	}
}

func TestKitContainerFailingInstallHookIsRemoved(t *testing.T) {
	cli := kitClient(t)
	s := kitSpec(t, kit.Hook{Argv: []string{"sh", "-c", "echo broken registry >&2; exit 3"}, User: "0"})
	id, err := StartKit(t.Context(), cli, s)
	t.Cleanup(func() { _ = RemoveKitVolumes(context.WithoutCancel(t.Context()), cli, s.Launch.Volumes) })
	if err == nil || !strings.Contains(err.Error(), "install hook 1") || !strings.Contains(err.Error(), "broken registry") {
		t.Fatalf("StartKit error %v, want the failing hook and its output", err)
	}
	if id != "" {
		t.Fatalf("StartKit returned container %s for a failed start", id)
	}
	list, err := cli.ContainerList(t.Context(), client.ContainerListOptions{All: true, Filters: client.Filters{}.Add("label", "archie-kit-test="+s.Name)})
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Items) != 0 {
		t.Fatalf("a failed Kit start left %d container(s) behind", len(list.Items))
	}
}
