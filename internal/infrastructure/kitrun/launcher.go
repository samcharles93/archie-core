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
	"github.com/samcharles93/archie-core/internal/domain/agentrun"
	"github.com/samcharles93/archie-core/internal/domain/harnesssecret"
	"github.com/samcharles93/archie-core/internal/infrastructure/egress"
	"github.com/samcharles93/archie-core/internal/infrastructure/kit"
	"github.com/samcharles93/archie-core/internal/skill"
)

// SecretResolver resolves a credential binding's secret value. *secret.Registry
// satisfies it; the interface stays narrow so kitrun does not need the whole
// secret engine surface, and a test can fake it with no engine at all.
type SecretResolver interface {
	Resolve(config.SecretRef) (string, error)
}

// OAuthSecrets reads an org's stored OAuth token set for a bound service, so a
// Kit's credential file can render that set's expiry. It is the store's read
// half only: kitrun never writes a token set, and nothing but the expiry
// leaves oauthExpiries, so a real token cannot reach the credential file
type OAuthSecrets interface {
	GetHarnessSecret(ctx context.Context, org, service string) (harnesssecret.Secret, error)
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
	// Config and Secrets decide, at launch, which declared credentials the run
	// is bound for and of which kind. The values themselves are resolved by
	// the proxy's Resolver per request. Nil Secrets leaves every API-key
	// credential unbound.
	Config  *config.Holder
	Secrets SecretResolver
	// OAuth reads the stored token set whose expiry a Kit's credential file
	// renders. A nil OAuth refuses the launch of a Kit that renders one, the
	// same way a nil OAuthStore makes the proxy refuse a required OAuth
	// credential.
	OAuth OAuthSecrets

	mu      sync.Mutex
	started bool
}

// Request is one Kit task to start.
type Request struct {
	// Execution keys the task's Kit volumes and names its container and
	// network.
	Execution string
	// Kit is the digest-pinned workload Kit or published Kit set.
	Kit       string
	Adapter   string
	WorkDir   string
	WorkerEnv []string
	// GateRetries is the workflow's gate-retry budget. Launch refuses a positive
	// budget without a resume verb.
	GateRetries int
	// Org and GrantedServices are the dispatching identity's own facts. A Kit
	// credential resolves only where these agree with a configured
	// CredentialBinding and the Kit's declared service; neither side widens the
	// other.
	Org             string
	GrantedServices []string
	// RunCredential is the run's credential: the container's proxy password.
	RunCredential string
}

// Run is a started Kit task.
type Run struct {
	Container *container.Container
	Harness   agentrun.HarnessSpec
	// Volumes outlive the run; remove them with RemoveVolumes once the
	// execution has ended.
	Volumes   []kit.Volume
	network   string
	token     string
	execution string
}

// Launch reads the request's Kit and starts its container.
func (l *Launcher) Launch(ctx context.Context, req Request) (*Run, error) {
	if req.Kit == "" {
		return nil, errors.New("kit profile names no kit")
	}
	adapter, ok := agentexec.LookupHarnessAdapter(req.Adapter)
	if req.Adapter != "" && !ok {
		return nil, fmt.Errorf("harness output adapter %q is unknown", req.Adapter)
	}
	if err := l.startEgress(ctx); err != nil {
		return nil, err
	}
	k, err := l.read(ctx, req.Kit, req.GateRetries)
	if err != nil {
		return nil, err
	}
	kinds := l.boundKinds(req, k.creds)
	facts, err := l.oauthFacts(ctx, req.Org, k.creds, kinds)
	if err != nil {
		return nil, err
	}
	session, err := l.Proxy.Register(egress.SessionOptions{Token: req.RunCredential, Org: req.Org, Network: k.network, Credentials: k.creds, Bound: kinds})
	if err != nil {
		return nil, err
	}
	run := &Run{network: "archie-kit-" + req.Execution, token: session.Token(), execution: req.Execution}
	launch, err := kit.Assemble(k.plan, k.img, kit.LaunchParams{Execution: req.Execution, ProxyToken: session.Token(), CAPath: caPath, Bound: kinds, OAuth: facts})
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
		Image:     req.Kit,
		Network:   run.network,
		Worker:    []string{agentPath},
		WorkerEnv: req.WorkerEnv,
		Binds: append([]string{
			l.AgentBinary + ":" + agentPath + ":ro",
			l.CAFile + ":" + caPath + ":ro",
			req.WorkDir + ":" + kit.WorkspaceDir,
		}, skillsBinds(k.skills, l.skillsDir())...),
		Launch:      launch,
		InstallDone: session.EnterRuntime,
	})
	if err != nil {
		l.release(run)
		return nil, errors.Join(err, l.Networks.Remove(context.WithoutCancel(ctx), run.network))
	}
	return run, nil
}

// boundKinds is the kind of each declared credential the run is bound for:
// declared by the Kit, granted to the run's identity, bound in its org, and
// for an API key, resolvable now. The proxy resolves the value itself per
// request, through the same intersection.
func (l *Launcher) boundKinds(req Request, creds []spec.CredentialCapability) map[string]egress.CredentialKind {
	kinds := map[string]egress.CredentialKind{}
	if l.Config == nil {
		return kinds
	}
	current := l.Config.Get().Containers.Credentials
	if len(current) == 0 {
		return kinds
	}
	declared := make([]string, len(creds))
	oauth := map[string]bool{}
	for i, c := range creds {
		declared[i] = c.Service
		oauth[c.Service] = egress.IsOAuthManaged(c)
	}
	bindings := config.ContainerConfig{Credentials: current}.BoundCredentials(req.Org, req.GrantedServices, declared)
	for service, binding := range bindings {
		switch {
		case binding.Secret == (config.SecretRef{}):
			// No secret: only OAuth gives the service a value.
			if oauth[service] {
				kinds[service] = egress.CredentialOAuth
			}
		case l.Secrets != nil:
			if _, err := l.Secrets.Resolve(binding.Secret); err == nil {
				kinds[service] = egress.CredentialAPIKey
			}
		}
	}
	return kinds
}

// oauthFacts reads the stored scopes and expiry for each OAuth-bound
// credential with a credential file.
func (l *Launcher) oauthFacts(ctx context.Context, org string, creds []spec.CredentialCapability, kinds map[string]egress.CredentialKind) (map[string]kit.OAuthFacts, error) {
	facts := map[string]kit.OAuthFacts{}
	for _, c := range creds {
		if c.OAuth == nil || c.OAuth.CredentialFile == nil || kinds[c.Service] != egress.CredentialOAuth {
			continue
		}
		if l.OAuth == nil {
			return nil, fmt.Errorf("credential %q renders a credential file, but no harness secret store is configured", c.Service)
		}
		secret, err := l.OAuth.GetHarnessSecret(ctx, org, c.Service)
		if err != nil {
			return nil, fmt.Errorf("credential %q renders a credential file and has no captured OAuth token (run the setup terminal first): %w", c.Service, err)
		}
		facts[c.Service] = kit.OAuthFacts{Scopes: secret.Scopes, ExpiresAt: secret.ExpiresAt}
	}
	return facts, nil
}

// release undoes what Launch already did for run: its proxy session. Call sites past this point pass the network/container errors
// through their own cleanup; release only ever needs to run once per Launch
// failure, so it takes no error to join.
func (l *Launcher) release(run *Run) {
	l.Proxy.Revoke(run.token)
}

// kitNeeds is what a profile's Kit asks of its run, read from its admitted
// plan.
type kitNeeds struct {
	plan    *kit.Plan
	img     kit.ImageConfig
	network *spec.PhasedNetwork
	creds   []spec.CredentialCapability
	skills  []spec.AgentSkillsCapability
}

// read fetches and admits the profile's Kit, checks it can meet the
// workflow's gate retries, and decodes what it asks of the run.
func (l *Launcher) read(ctx context.Context, ref string, gateRetries int) (kitNeeds, error) {
	merged, err := l.Fetch.Assemble(ctx, []fetch.Request{{Reference: ref}}, kit.MergeOptions)
	if err != nil {
		return kitNeeds{}, fmt.Errorf("read kit: %w", err)
	}
	k := kitNeeds{}
	if k.plan, err = kit.FromMerge(merged.MergeResult); err != nil {
		return kitNeeds{}, err
	}
	if err := kit.ValidateNeeds(k.plan, gateRetries); err != nil {
		return kitNeeds{}, err
	}
	if k.img, err = l.imageConfig(ctx, ref); err != nil {
		return kitNeeds{}, err
	}
	for _, name := range slices.Sorted(maps.Keys(merged.Env)) {
		k.img.Env = append(k.img.Env, name+"="+merged.Env[name])
	}
	if k.network, err = spec.NetworkPolicyOf(k.plan.Capabilities); err != nil {
		return kitNeeds{}, err
	}
	if k.creds, err = spec.CredentialsOf(k.plan.Capabilities); err != nil {
		return kitNeeds{}, err
	}
	if k.skills, err = spec.AgentSkillsOf(k.plan.Capabilities); err != nil {
		return kitNeeds{}, err
	}
	return k, nil
}

// skillsDir is the operator's live skills_dir, or "" with no config.
func (l *Launcher) skillsDir() string {
	if l.Config == nil {
		return ""
	}
	return l.Config.Get().SkillsDir
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

// skillsBinds mounts the operator's shared skills store read-only at each
// path the Kit asks for it, whatever mode the Kit asks for. With no
// skills_dir configured the host withholds the mount.
func skillsBinds(asks []spec.AgentSkillsCapability, sharedDir string) []string {
	if sharedDir == "" {
		return nil
	}
	var binds []string
	for _, a := range asks {
		if a.Path != "" {
			binds = append(binds, skill.StoreDir(sharedDir)+":"+a.Path+":ro")
		}
	}
	return binds
}
