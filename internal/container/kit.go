package container

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"path"
	"strings"

	"github.com/docker/sandbox-kit-spec/v3/spec"
	"github.com/moby/moby/api/pkg/stdcopy"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/mount"
	"github.com/moby/moby/client"

	"github.com/samcharles93/archie-core/internal/infrastructure/kit"
)

const hookOutputTail = 2000

// KitSpec is one Kit task container: the workload image running archie's
// worker as root, on the task's sandbox network, with the Kit's launch.
type KitSpec struct {
	Name    string
	Image   string
	Network string
	// Worker is the entrypoint that runs archie-agent as the container's
	// root process, and WorkerEnv its environment, credentials included.
	Worker    []string
	WorkerEnv []string
	Binds     []string
	Labels    map[string]string
	Launch    kit.Launch
	// InstallDone runs after the install hooks and before anything else, so
	// the caller can end the install phase: the egress session moves to its
	// runtime policy and install-phase credentials stop resolving.
	InstallDone func()
}

// StartKit creates and starts a Kit container and runs its install and
// startup hooks. A failing hook removes the container and returns its
// output.
func StartKit(ctx context.Context, cli *client.Client, s KitSpec) (string, error) {
	var mounts []mount.Mount
	for _, v := range s.Launch.Volumes {
		if _, err := cli.VolumeCreate(ctx, client.VolumeCreateOptions{Name: v.Name, Labels: s.Labels}); err != nil {
			return "", fmt.Errorf("create kit volume %s: %w", v.Name, err)
		}
		mounts = append(mounts, mount.Mount{Type: mount.TypeVolume, Source: v.Name, Target: v.Path})
	}
	// Docker's own init becomes PID 1 and reaps orphans. The worker is PID 1
	// otherwise, and a detached hook that forks and exits leaves zombies the
	// worker never waits on, until the container's PID table is exhausted.
	hostInit := true
	created, err := cli.ContainerCreate(ctx, client.ContainerCreateOptions{
		Name: s.Name,
		Config: &container.Config{
			Image:      s.Image,
			Entrypoint: s.Worker,
			Cmd:        []string{},
			User:       "0",
			Env:        s.WorkerEnv,
			WorkingDir: kit.WorkspaceDir,
			Labels:     s.Labels,
		},
		HostConfig: &container.HostConfig{
			NetworkMode: container.NetworkMode(s.Network),
			Binds:       s.Binds,
			Mounts:      mounts,
			Init:        &hostInit,
		},
	})
	if err != nil {
		return "", fmt.Errorf("create kit container: %w", err)
	}
	fail := func(err error) (string, error) {
		_, rmErr := cli.ContainerRemove(context.WithoutCancel(ctx), created.ID, client.ContainerRemoveOptions{Force: true})
		return "", errors.Join(err, rmErr)
	}
	if _, err := cli.ContainerStart(ctx, created.ID, client.ContainerStartOptions{}); err != nil {
		return fail(fmt.Errorf("start kit container: %w", err))
	}
	if err := installKit(ctx, cli, created.ID, s.Launch); err != nil {
		return fail(err)
	}
	if s.InstallDone != nil {
		s.InstallDone()
	}
	for _, f := range s.Launch.Files {
		if err := writeKitFile(ctx, cli, created.ID, s.Launch.Harness.User, f.Path, f.Content, f.Mode, f.Overwrite); err != nil {
			return fail(fmt.Errorf("write kit file %s: %w", f.Path, err))
		}
	}
	for _, f := range s.Launch.Context {
		if err := writeKitContext(ctx, cli, created.ID, f); err != nil {
			return fail(fmt.Errorf("write agent context %s: %w", f.Path, err))
		}
	}
	for _, skill := range s.Launch.Skills {
		if err := stageKitSkill(ctx, cli, created.ID, s.Launch.Harness.User, skill); err != nil {
			return fail(err)
		}
	}
	for i, h := range s.Launch.Startup {
		if err := runHook(ctx, cli, created.ID, h); err != nil {
			return fail(fmt.Errorf("startup hook %d: %w", i+1, err))
		}
	}
	return created.ID, nil
}

func installKit(ctx context.Context, cli *client.Client, id string, launch kit.Launch) error {
	for _, f := range launch.InstallFiles {
		if err := writeKitFile(ctx, cli, id, launch.Harness.User, f.Path, f.Content, f.Mode, f.Overwrite); err != nil {
			return fmt.Errorf("write install credential file %s: %w", f.Path, err)
		}
	}
	for i, h := range launch.Install {
		if err := runHook(ctx, cli, id, h); err != nil {
			return fmt.Errorf("install hook %d: %w", i+1, err)
		}
	}
	for _, f := range launch.InstallFiles {
		code, out, err := ExecIn(ctx, cli, id, launch.Harness.User, []string{"KIT_PATH=" + f.Path}, []string{"sh", "-c", `rm -f -- "$KIT_PATH"`})
		if err != nil {
			return fmt.Errorf("remove install credential file %s: %w", f.Path, err)
		}
		if code != 0 {
			return fmt.Errorf("remove install credential file %s: exit %d: %s", f.Path, code, out)
		}
	}
	return nil
}

func stageKitSkill(ctx context.Context, cli *client.Client, id, user string, skill kit.Skill) error {
	code, out, err := ExecIn(ctx, cli, id, "0", []string{"SKILL_SOURCE=" + skill.Source, "SKILL_TARGET=" + skill.Target, "SKILL_USER=" + user}, []string{"sh", "-ec", `
[ -d "$SKILL_SOURCE" ] && [ -f "$SKILL_SOURCE/SKILL.md" ] || exit 1
[ "$SKILL_SOURCE" != "$SKILL_TARGET" ] || exit 0
at="$SKILL_TARGET"
while [ "$at" != / ]; do
 [ ! -L "$at" ] || exit 1
 at=$(dirname "$at")
done
[ ! -e "$SKILL_TARGET" ] || exit 0
mkdir -p "$(dirname "$SKILL_TARGET")"
cp -R "$SKILL_SOURCE" "$SKILL_TARGET"
chown -R "$SKILL_USER" "$SKILL_TARGET"
`})
	if err != nil {
		return fmt.Errorf("stage bundled skill %s: %w", skill.Source, err)
	}
	if code != 0 {
		return fmt.Errorf("stage bundled skill %s: exit %d: %s", skill.Source, code, out)
	}
	return nil
}

// Replace only Archie's managed guidance, retaining the image's instructions.
func writeKitContext(ctx context.Context, cli *client.Client, id string, f spec.File) error {
	code, out, err := ExecIn(ctx, cli, id, "0", []string{"KIT_PATH=" + f.Path, "KIT_CONTENT=" + base64.StdEncoding.EncodeToString([]byte(f.Content))}, []string{"sh", "-ec", `
at="$KIT_PATH"
while [ "$at" != / ]; do
 [ ! -L "$at" ] || exit 1
 at=$(dirname "$at")
done
[ ! -e "$KIT_PATH" ] || [ -f "$KIT_PATH" ] || exit 1
mkdir -p "$(dirname "$KIT_PATH")"
tmp=$(mktemp "$KIT_PATH.XXXXXX")
trap 'rm -f "$tmp"' 0
if [ -f "$KIT_PATH" ]; then
 awk '/^<!-- BEGIN ARCHIE -->$/ { managed=1; next } /^<!-- END ARCHIE -->$/ { managed=0; next } !managed { print }' "$KIT_PATH" > "$tmp"
fi
printf '%s\n' '<!-- BEGIN ARCHIE -->' >> "$tmp"
printf %s "$KIT_CONTENT" | base64 -d >> "$tmp"
printf '\n%s\n' '<!-- END ARCHIE -->' >> "$tmp"
chmod 0644 "$tmp"
mv "$tmp" "$KIT_PATH"
`})
	if err != nil {
		return err
	}
	if code != 0 {
		return fmt.Errorf("exited %d: %s", code, out)
	}
	return nil
}

// RemoveKitVolumes removes an execution's Kit volumes once the execution
// has ended; a retry of the same execution reuses them until then.
func RemoveKitVolumes(ctx context.Context, cli *client.Client, volumes []kit.Volume) error {
	var errs []error
	for _, v := range volumes {
		if _, err := cli.VolumeRemove(ctx, v.Name, client.VolumeRemoveOptions{Force: true}); err != nil {
			errs = append(errs, fmt.Errorf("remove kit volume %s: %w", v.Name, err))
		}
	}
	return errors.Join(errs...)
}

func runHook(ctx context.Context, cli *client.Client, id string, h kit.Hook) error {
	if h.Background {
		exec, err := cli.ExecCreate(ctx, id, client.ExecCreateOptions{User: h.User, Env: h.Env, Cmd: h.Argv, WorkingDir: kit.WorkspaceDir})
		if err != nil {
			return err
		}
		_, err = cli.ExecStart(ctx, exec.ID, client.ExecStartOptions{Detach: true})
		return err
	}
	code, out, err := ExecIn(ctx, cli, id, h.User, h.Env, h.Argv)
	if err != nil {
		return err
	}
	if code != 0 {
		return fmt.Errorf("%s exited %d: %s", strings.Join(h.Argv, " "), code, out)
	}
	return nil
}

// writeKitFile writes a lifecycle file as the harness user, so the file
// belongs to the agent as the Kit spec requires.
func writeKitFile(ctx context.Context, cli *client.Client, id, user, target, content, mode string, overwrite *bool) error {
	script := `mkdir -p "$(dirname "$KIT_PATH")" && printf %s "$KIT_CONTENT" | base64 -d > "$KIT_PATH"`
	if mode != "" {
		script += ` && chmod "$KIT_MODE" "$KIT_PATH"`
	}
	if overwrite != nil && !*overwrite {
		script = `[ -e "$KIT_PATH" ] || { ` + script + `; }`
	}
	env := []string{
		"KIT_PATH=" + path.Clean(target),
		"KIT_CONTENT=" + base64.StdEncoding.EncodeToString([]byte(content)),
		"KIT_MODE=" + mode,
	}
	code, out, err := ExecIn(ctx, cli, id, user, env, []string{"sh", "-c", script})
	if err != nil {
		return err
	}
	if code != 0 {
		return fmt.Errorf("exited %d: %s", code, out)
	}
	return nil
}

// ExecIn runs argv in a container as user with exactly env, and returns its
// exit code and the tail of its combined output.
func ExecIn(ctx context.Context, cli *client.Client, id, user string, env, argv []string) (int, string, error) {
	exec, err := cli.ExecCreate(ctx, id, client.ExecCreateOptions{
		User: user, Env: env, Cmd: argv, WorkingDir: kit.WorkspaceDir,
		AttachStdout: true, AttachStderr: true,
	})
	if err != nil {
		return 0, "", err
	}
	attached, err := cli.ExecAttach(ctx, exec.ID, client.ExecAttachOptions{})
	if err != nil {
		return 0, "", err
	}
	defer attached.Close()
	var out bytes.Buffer
	if _, err := stdcopy.StdCopy(&out, &out, attached.Reader); err != nil {
		return 0, "", err
	}
	inspected, err := cli.ExecInspect(ctx, exec.ID, client.ExecInspectOptions{})
	if err != nil {
		return 0, "", err
	}
	text := strings.TrimSpace(out.String())
	if len(text) > hookOutputTail {
		text = text[len(text)-hookOutputTail:]
	}
	return inspected.ExitCode, text, nil
}
