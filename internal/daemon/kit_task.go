package daemon

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/domain/workflow"
	workflowtask "github.com/samcharles93/archie-core/internal/domain/workflow/task"
	"github.com/samcharles93/archie-core/internal/infrastructure/kit"
	"github.com/samcharles93/archie-core/internal/infrastructure/kitrun"
	"github.com/samcharles93/archie-core/internal/storage"
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
	credential, revokeCredential, err := d.runCredential(task)
	if err != nil {
		park("run credential failed", err)
		return
	}
	defer revokeCredential()
	env, err := kitrun.WorkerEnv(kitrun.Endpoints{
		NATS: d.ConnectedNATS.URL, NATSToken: d.ConnectedNATS.ContainerToken(),
		StateStore: d.ConnectedStateStore.URL, StateStoreToken: credential,
	}, os.Getuid(), os.Getgid())
	if err != nil {
		park("kit worker environment", err)
		return
	}
	// The interface is reparsed rather than threaded through pinTaskProfile's
	// return: task.WorkflowDefinitionYAML is already pinned by the time this
	// runs, parsing it is pure and cheap, and pinTaskProfile's signature stays
	// unchanged for its other callers.
	iface, err := workflowtask.ParseWorkflowInterface(task.WorkflowDefinitionYAML)
	if err != nil {
		park("kit workflow interface", err)
		return
	}
	// Org and GrantedServices are the dispatching identity's own facts, read
	// fresh here rather than cached anywhere: the same per-task
	// config.Config d.configFor already resolves for every other identity
	// override (Forge, Budgets, ...).
	taskCfg := d.configFor(task)
	credentialOrg, grantedServices := taskCfg.CredentialAccess("")
	run, err := d.KitLauncher.Launch(ctx, kitrun.Request{
		Execution:       fmt.Sprintf("task-%d", task.ID),
		Kit:             profile.Kit,
		Adapter:         profile.Adapter,
		WorkDir:         workDir,
		WorkerEnv:       env,
		GateRetries:     iface.Needs().GateRetries,
		Org:             credentialOrg,
		GrantedServices: grantedServices,
		RunCredential:   credential,
	})
	if err != nil {
		park("kit launch failed", err)
		return
	}

	limitCtx, stopLimit := withTaskTimeLimit(ctx, d.configFor(task).Budgets.TaskWallClock.Std())
	runCtx, stopWatch := withContainerExit(limitCtx, run.Container.Exited())
	d.runViaAgent(runCtx, task, repo, profile, &run.Harness, credential)
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

// ModelEgress opens a native task's model proxy session. The container holds
// a sentinel under each provider's key variable and reaches the providers
// through the egress proxy, which swaps in the org's bound key.
type ModelEgress interface {
	Open(token, org string, providers map[string]config.Provider) (ModelSession, error)
	Close(token string)
}

// TaskModels answers a task's org and the model aliases it resolves against.
type TaskModels interface {
	TaskModelAliases(ctx context.Context, taskID int64) (org string, aliases map[string]string, err error)
}

// ModelSession is what a native container needs for its model proxy session.
type ModelSession struct {
	Env    []string
	Mounts []storage.Mount
}

// openModelEgress opens task's model session, or refuses: provider keys
// never reach a container, so without the proxy no model call can run.
func (d *Daemon) openModelEgress(task *workflow.Task, credential string) (ModelSession, func(), error) {
	cfg := d.configFor(task)
	if d.ModelEgress == nil {
		for _, p := range cfg.Providers {
			if p.APIKeyEnv != "" {
				return ModelSession{}, nil, fmt.Errorf("model egress proxy is unavailable on this daemon, and provider keys never enter a container")
			}
		}
		return ModelSession{}, func() {}, nil
	}
	if credential == "" {
		return ModelSession{}, nil, fmt.Errorf("model egress needs a run credential, and no State Store issues one")
	}
	session, err := d.ModelEgress.Open(credential, task.OrgOrDefault(), cfg.Providers)
	if err != nil {
		return ModelSession{}, nil, err
	}
	return session, func() { d.ModelEgress.Close(credential) }, nil
}
