package container

import (
	"context"
	"fmt"
	"strconv"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/mount"
	"github.com/moby/moby/client"
)

// Reown hands dir back to uid:gid with a throwaway root container. A task
// container that was SIGKILLed never restores ownership of the files it
// created as root, and the non-root daemon has no privilege to do it itself.
func (p *Pool) Reown(ctx context.Context, dir string, uid, gid int) error {
	cfg := p.current()
	if err := p.ensureImage(ctx, cfg, cfg.Image); err != nil {
		return err
	}
	resp, err := p.cli.ContainerCreate(ctx, client.ContainerCreateOptions{
		Config: &container.Config{
			Image:      cfg.Image,
			Entrypoint: []string{"chown", "-R", strconv.Itoa(uid) + ":" + strconv.Itoa(gid), "/w"},
			Labels:     map[string]string{"archie-daemon": "true"},
		},
		HostConfig: &container.HostConfig{
			NetworkMode: "none",
			Mounts:      []mount.Mount{{Type: mount.TypeBind, Source: dir, Target: "/w"}},
		},
	})
	if err != nil {
		return fmt.Errorf("create reown container: %w", err)
	}
	defer func() {
		_, _ = p.cli.ContainerRemove(context.WithoutCancel(ctx), resp.ID, client.ContainerRemoveOptions{Force: true})
	}()
	wait := p.cli.ContainerWait(ctx, resp.ID, client.ContainerWaitOptions{Condition: container.WaitConditionNextExit})
	if _, err := p.cli.ContainerStart(ctx, resp.ID, client.ContainerStartOptions{}); err != nil {
		return fmt.Errorf("start reown container: %w", err)
	}
	select {
	case res := <-wait.Result:
		if res.StatusCode != 0 {
			return fmt.Errorf("reown %s: chown exited %d", dir, res.StatusCode)
		}
		return nil
	case err := <-wait.Error:
		return fmt.Errorf("wait for reown container: %w", err)
	case <-ctx.Done():
		return ctx.Err()
	}
}
