package daemon

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/domain/workflow"
	"github.com/samcharles93/archie-core/internal/infrastructure/kit"
	"github.com/samcharles93/archie-core/internal/infrastructure/kitrun"
	"github.com/samcharles93/archie-core/internal/taskstate"
)

// KitLauncher starts and ends the container of a task whose agent profile
// is a Kit.
type KitLauncher interface {
	Launch(ctx context.Context, req kitrun.Request) (*kitrun.Run, error)
	Release(ctx context.Context, run *kitrun.Run) error
	RemoveVolumes(ctx context.Context, volumes []kit.Volume) error
}

// runKitTask runs a task in its Kit's container, every agent stage on the
// Kit's harness. The worker reaches NATS and the State Store only through
// the egress relay, and its environment carries no model provider key.
func (d *Daemon) runKitTask(ctx context.Context, task *workflow.Task, repo config.Repo, workDir string, profile config.AgentProfile) {
	park := func(reason string, err error) {
		d.Log.Error(reason, "task", task.ID, "err", err)
		d.parkRunningTask(ctx, task.ID, reason+": "+err.Error(), taskstate.ParkTransient)
	}
	if d.KitLauncher == nil {
		park("kit profile", fmt.Errorf("kit harness runs are unavailable on this daemon"))
		return
	}
	if err := writeTaskBrief(workDir, task); err != nil {
		park("task.json write failed", err)
		return
	}
	stateStoreToken, revokeStateStoreGrant, err := d.stateStoreGrantToken(task)
	if err != nil {
		park("state store grant failed", err)
		return
	}
	defer revokeStateStoreGrant()
	env, err := kitrun.WorkerEnv(kitrun.Endpoints{
		NATS: d.ConnectedNATS.URL, NATSToken: d.ConnectedNATS.Token,
		StateStore: d.ConnectedStateStore.URL, StateStoreToken: stateStoreToken,
	}, os.Getuid(), os.Getgid())
	if err != nil {
		park("kit worker environment", err)
		return
	}
	run, err := d.KitLauncher.Launch(ctx, kitrun.Request{
		Execution: fmt.Sprintf("task-%d", task.ID),
		Kit:       profile.Kit,
		Adapter:   profile.Adapter,
		WorkDir:   workDir,
		WorkerEnv: env,
	})
	if err != nil {
		park("kit launch failed", err)
		return
	}

	limitCtx, stopLimit := withTaskTimeLimit(ctx, d.configFor(task).Budgets.TaskWallClock.Std())
	runCtx, stopWatch := withContainerExit(limitCtx, run.Container.Exited())
	d.runViaAgent(runCtx, task, repo, profile, &run.Harness)
	stopWatch()
	stopLimit()

	if err := d.KitLauncher.Release(ctx, run); err != nil {
		d.Log.Warn("kit release failed", "task", task.ID, "err", err)
	}
	d.removeEndedKitVolumes(ctx, task.ID, run.Volumes)
}

// removeEndedKitVolumes removes the execution's Kit volumes unless the task
// parked: a retry of a parked task is the same execution and resumes from
// them.
func (d *Daemon) removeEndedKitVolumes(ctx context.Context, taskID int64, volumes []kit.Volume) {
	if len(volumes) == 0 || d.Store == nil {
		return
	}
	lookupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	latest, err := d.Store.TaskByID(lookupCtx, taskID)
	if err != nil || latest == nil || latest.Status == workflow.StatusParked {
		return
	}
	if err := d.KitLauncher.RemoveVolumes(lookupCtx, volumes); err != nil {
		d.Log.Warn("kit volume removal failed", "task", taskID, "err", err)
	}
}
