// Package kitrun starts a Kit task: it composes the profile's Kits, opens
// the task's egress session and sandbox network, and starts the container
// with archie's worker as root and the harness spec the worker runs stages on.
package kitrun

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"sync"

	"github.com/docker/sandbox-kit-spec/v3/fetch"
	"github.com/docker/sandbox-kit-spec/v3/spec"

	"github.com/samcharles93/archie-core/internal/agentexec"
	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/container"
	"github.com/samcharles93/archie-core/internal/infrastructure/egress"
	"github.com/samcharles93/archie-core/internal/infrastructure/kit"
)

// SecretResolver resolves a credential binding's secret value. *secret.Registry
// satisfies it; the interface stays narrow so kitrun does not need the whole
// secret engine surface, and a test can fake it with no engine at all.
type SecretResolver interface {
	Resolve(config.SecretRef) (string, error)
}

// Grants is what Launch and Release tell the egress proxy's resolver about a
// run's credentials: exactly the intersection Launch computed, and nothing
// once the run ends. *egress.GrantResolver satisfies it.
type Grants interface {
	Grant(run string, secrets map[string]string)
	RevokeGrant(run string)
}

const (
	agentPath = "/opt/archie/archie-agent"
	caPath    = "/opt/archie/ca.pem"
)

// Launcher starts Kit task containers.
type Launcher struct {
	Pool     *container.Pool
	Fetch    *fetch.Client
	Proxy    *egress.Proxy
	Networks *egress.Networks
	// AgentBinary and CAFile are host paths mounted read-only into every
	// Kit container: the worker's own binary and the egress CA.
	AgentBinary string
	CAFile      string
	// Config is read fresh on every Launch, never captured once at
	// construction: credential bindings are their own live control-plane
	// resource (controlplane.CredentialBindingsKind), so a binding added or
	// changed after this daemon started must take effect on the very next
	// dispatch, the same way an agent profile already does
	// (docs/prds/external-agent-harness.md, "Selection": applies without a
	// restart). A nil Secrets or Grants degrades every credential to unbound
	// rather than panicking: a daemon with no Kit profile configured wires
	// neither.
	Config  *config.Holder
	Secrets SecretResolver
	Grants  Grants

	mu      sync.Mutex
	started bool
}

// Request is one Kit task to start.
type Request struct {
	// Execution keys the task's Kit volumes and names its container and
	// network.
	Execution string
	Kit       []string
	Adapter   string
	WorkDir   string
	WorkerEnv []string
	// GateRetries is the workflow's declared gate-retry budget
	// (task.WorkflowNeeds.GateRetries); zero means no stage gates its
	// result. A composition with no agent-sessions resume verb cannot meet
	// a positive one, and Launch refuses it rather than starting a
	// container no gate failure could ever resume.
	GateRetries int
	// Org and GrantedServices are the dispatching identity's own facts
	// (config.Config.Org, config.Config.GrantedCredentials), carried here
	// because kitrun holds no store or config of its own. Launch resolves a
	// Kit credential only where these agree with a configured
	// CredentialBinding and the Kit's own declared service -- the
	// declared-and-granted intersection docs/prds/external-agent-harness.md
	// and orgs-and-access.md both require; neither side widens the other.
	Org             string
	GrantedServices []string
}

// Run is a started Kit task.
type Run struct {
	Container *container.Container
	Harness   agentexec.HarnessSpec
	// Volumes outlive the run; remove them with RemoveVolumes once the
	// execution has ended.
	Volumes   []kit.Volume
	network   string
	token     string
	execution string
}

// Launch composes the request's Kits and starts its container.
func (l *Launcher) Launch(ctx context.Context, req Request) (*Run, error) {
	if len(req.Kit) == 0 {
		return nil, errors.New("kit profile names no kit")
	}
	adapter, ok := agentexec.LookupHarnessAdapter(req.Adapter)
	if req.Adapter != "" && !ok {
		return nil, fmt.Errorf("harness output adapter %q is unknown", req.Adapter)
	}
	if err := l.startEgress(ctx); err != nil {
		return nil, err
	}
	plan, img, err := l.compose(ctx, req.Kit)
	if err != nil {
		return nil, err
	}
	if err := kit.ValidateNeeds(plan, req.GateRetries); err != nil {
		return nil, err
	}
	network, err := spec.NetworkPolicyOf(plan.Capabilities)
	if err != nil {
		return nil, err
	}
	creds, err := spec.CredentialsOf(plan.Capabilities)
	if err != nil {
		return nil, err
	}
	granted, bound := l.resolveCredentials(req, creds)
	if l.Grants != nil {
		l.Grants.Grant(req.Execution, granted)
	}
	session, err := l.Proxy.Register(egress.SessionOptions{Run: req.Execution, Network: network, Credentials: creds})
	if err != nil {
		if l.Grants != nil {
			l.Grants.RevokeGrant(req.Execution)
		}
		return nil, err
	}
	run := &Run{network: "archie-kit-" + req.Execution, token: session.Token(), execution: req.Execution}
	launch, err := kit.Assemble(plan, img, kit.LaunchParams{Execution: req.Execution, ProxyToken: session.Token(), CAPath: caPath, Bound: bound})
	if err != nil {
		l.release(run)
		return nil, err
	}
	launch.Harness.Adapter, launch.Harness.MCPConfig = req.Adapter, adapter.MCPConfig
	run.Harness, run.Volumes = launch.Harness, launch.Volumes
	if err := l.Networks.Create(ctx, run.network); err != nil {
		l.release(run)
		return nil, err
	}
	run.Container, err = l.Pool.AcquireKit(ctx, container.KitSpec{
		Name:      "archie-kit-" + req.Execution,
		Image:     req.Kit[0],
		Network:   run.network,
		Worker:    []string{agentPath},
		WorkerEnv: req.WorkerEnv,
		Binds: []string{
			l.AgentBinary + ":" + agentPath + ":ro",
			l.CAFile + ":" + caPath + ":ro",
			req.WorkDir + ":" + kit.WorkspaceDir,
		},
		Launch:      launch,
		InstallDone: session.EnterRuntime,
	})
	if err != nil {
		l.release(run)
		return nil, errors.Join(err, l.Networks.Remove(context.WithoutCancel(ctx), run.network))
	}
	return run, nil
}

// resolveCredentials computes the run's granted secrets -- the intersection
// of what the Kit composition declares, what config.CredentialBinding
// entries exist, and what the dispatching identity is granted and belongs
// to -- and resolves each to a real value. granted is the run/service ->
// value map for Grants.Grant; bound is the service-name list kit.Assemble
// renders sentinel-mode env vars from. A service failing any part of the
// intersection, or whose binding's secret does not resolve, is simply
// absent from both: it stays unbound, exactly as if credential@1 named a
// service nobody configured.
func (l *Launcher) resolveCredentials(req Request, creds []spec.CredentialCapability) (granted map[string]string, bound []string) {
	granted = map[string]string{}
	if l.Secrets == nil || l.Config == nil {
		return granted, bound
	}
	current := l.Config.Get().Containers.Credentials
	if len(current) == 0 {
		return granted, bound
	}
	declared := make([]string, len(creds))
	for i, c := range creds {
		declared[i] = c.Service
	}
	bindings := config.ContainerConfig{Credentials: current}.BoundCredentials(req.Org, req.GrantedServices, declared)
	for service, binding := range bindings {
		value, err := l.Secrets.Resolve(binding.Secret)
		if err != nil {
			continue
		}
		granted[service] = value
		bound = append(bound, service)
	}
	return granted, bound
}

// release undoes what Launch already did for run: the egress grant, then the
// session. Call sites past this point pass the network/container errors
// through their own cleanup; release only ever needs to run once per Launch
// failure, so it takes no error to join.
func (l *Launcher) release(run *Run) {
	if l.Grants != nil {
		l.Grants.RevokeGrant(run.execution)
	}
	l.Proxy.Revoke(run.token)
}

// compose assembles the profile's Kits and reads the workload image's
// config, the first Kit being the workload.
func (l *Launcher) compose(ctx context.Context, refs []string) (*kit.Plan, kit.ImageConfig, error) {
	reqs := make([]fetch.Request, len(refs))
	for i, ref := range refs {
		reqs[i] = fetch.Request{Reference: ref}
	}
	merged, err := l.Fetch.Assemble(ctx, reqs, kit.MergeOptions)
	if err != nil {
		return nil, kit.ImageConfig{}, fmt.Errorf("assemble kits: %w", err)
	}
	plan, err := kit.FromMerge(merged.MergeResult)
	if err != nil {
		return nil, kit.ImageConfig{}, err
	}
	img, err := l.imageConfig(ctx, refs[0])
	if err != nil {
		return nil, kit.ImageConfig{}, err
	}
	for _, name := range slices.Sorted(maps.Keys(merged.Env)) {
		img.Env = append(img.Env, name+"="+merged.Env[name])
	}
	return plan, img, nil
}

// Release stops the run's container, then removes its network, revokes its
// credential grant and ends its egress session.
func (l *Launcher) Release(ctx context.Context, run *Run) error {
	l.Pool.Release(ctx, run.Container)
	l.release(run)
	return l.Networks.Remove(context.WithoutCancel(ctx), run.network)
}

// RemoveVolumes removes an ended execution's Kit volumes.
func (l *Launcher) RemoveVolumes(ctx context.Context, volumes []kit.Volume) error {
	return container.RemoveKitVolumes(ctx, l.Pool.Client(), volumes)
}

// startEgress starts the relay on first use, so a daemon with no Kit
// profile runs no relay.
func (l *Launcher) startEgress(ctx context.Context) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.started {
		return nil
	}
	if err := l.Networks.Start(ctx); err != nil {
		return err
	}
	l.started = true
	return nil
}

func (l *Launcher) imageConfig(ctx context.Context, ref string) (kit.ImageConfig, error) {
	if err := l.Pool.EnsureImage(ctx, ref); err != nil {
		return kit.ImageConfig{}, err
	}
	inspected, err := l.Pool.Client().ImageInspect(ctx, ref)
	if err != nil {
		return kit.ImageConfig{}, fmt.Errorf("inspect kit image %s: %w", ref, err)
	}
	cfg := inspected.Config
	if cfg == nil {
		return kit.ImageConfig{}, fmt.Errorf("kit image %s has no config", ref)
	}
	return kit.ImageConfig{
		Entrypoint: slices.Clone(cfg.Entrypoint), Cmd: slices.Clone(cfg.Cmd),
		Env: slices.Clone(cfg.Env), User: cfg.User,
	}, nil
}
