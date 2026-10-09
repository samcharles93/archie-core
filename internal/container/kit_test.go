package container

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/docker/sandbox-kit-spec/v3/spec"
	"github.com/moby/moby/client"

	"github.com/samcharles93/archie-core/internal/domain/agentrun"
	"github.com/samcharles93/archie-core/internal/infrastructure/kit"
)

func TestKitRuntimeDiscovery(t *testing.T) {
	if testing.Short() {
		t.Skip("requires Docker")
	}
	cli, err := client.New(client.FromEnv)
	if err != nil {
		t.Fatal(err)
	}
	defer cli.Close()
	shared := t.TempDir()
	if err := os.Chmod(shared, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(shared, "SKILL.md"), []byte("shared body"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name    string
		symlink bool
	}{
		{name: "preserves and replaces managed guidance"},
		{name: "refuses symlink guidance", symlink: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			script := `test -f /tmp/install.json; mkdir -p /tmp/discovery /tmp/bundle; printf 'SKILL body' > /tmp/bundle/SKILL.md; printf 'image instructions\n' > /tmp/discovery/AGENTS.md`
			if tc.symlink {
				script += `; mv /tmp/discovery/AGENTS.md /tmp/other; ln -s /tmp/other /tmp/discovery/AGENTS.md`
			}
			launch := kit.Launch{
				Harness: agentrun.HarnessSpec{User: "0"}, Install: []kit.Hook{{User: "0", Argv: []string{"sh", "-ec", script}}}, InstallFiles: []spec.File{{Path: "/tmp/install.json", Content: "install sentinel"}},
				Files:   []spec.File{{Path: "/tmp/runtime.json", Content: "runtime sentinel"}},
				Startup: []kit.Hook{{User: "0", Argv: []string{"sh", "-ec", "test ! -e /tmp/install.json; test -f /tmp/runtime.json"}}},
				Context: []spec.File{{Path: "/tmp/discovery/AGENTS.md", Content: "first guidance"}}, Skills: []kit.Skill{{Source: "/tmp/bundle", Target: "/tmp/skills/bundle"}},
			}
			id, err := StartKit(t.Context(), cli, KitSpec{Image: "alpine:3", Worker: []string{"tail", "-f", "/dev/null"}, Binds: []string{shared + ":/tmp/skills/shared:ro,z"}, Launch: launch})
			if tc.symlink {
				if err == nil {
					_, _ = cli.ContainerRemove(context.Background(), id, client.ContainerRemoveOptions{Force: true})
					t.Fatal("symlink was followed")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				_, _ = cli.ContainerRemove(context.Background(), id, client.ContainerRemoveOptions{Force: true})
			})
			if err := writeKitContext(t.Context(), cli, id, spec.File{Path: "/tmp/discovery/AGENTS.md", Content: "replacement guidance"}); err != nil {
				t.Fatal(err)
			}
			code, out, err := ExecIn(t.Context(), cli, id, "0", nil, []string{"sh", "-ec", "cat /tmp/discovery/AGENTS.md; cat /tmp/skills/bundle/SKILL.md; cat /tmp/skills/shared/SKILL.md; if echo overwrite > /tmp/skills/shared/SKILL.md 2>/dev/null; then exit 1; fi"})
			if err != nil || code != 0 {
				t.Fatalf("read discovery: %d %q %v", code, out, err)
			}
			for _, want := range []string{"image instructions", "replacement guidance", "SKILL body", "shared body"} {
				if !strings.Contains(out, want) {
					t.Errorf("missing %q in %q", want, out)
				}
			}
			if strings.Contains(out, "first guidance") || strings.Count(out, "<!-- BEGIN ARCHIE -->") != 1 {
				t.Fatalf("managed guidance was not replaced: %q", out)
			}
		})
	}
}
