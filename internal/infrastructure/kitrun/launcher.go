// Package kitrun starts a Kit task: it composes the profile's Kits, opens
// the task's egress session and sandbox network, and starts the container
// with archie's worker as root and the harness spec the worker runs stages on.
package kitrun

import (
	"context"
	"crypto/rand"
	"encoding/hex"
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

// GrantRegistry records a live session's granted credential services, so the
// egress proxy can resolve a session token that names no task. It is optional:
// a Launcher without one serves task runs only.
type GrantRegistry interface {
	Grant(token, org string, services []string)
	Revoke(token string)
}

// Launcher starts Kit task containers.
type Launcher struct {
	Pool     *container.Pool
	Fetch    *fetch.Client
	Proxy    *egress.Proxy
	Networks *egress.Networks
	// Grants records a setup session's granted services while it is open.
	// Nil refuses a setup session rather than opening one whose token the
	// egress proxy cannot resolve.
	Grants GrantRegistry
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
	grants    GrantRegistry
	// ephemeral marks a setup session, whose volumes are never reused by a
	// later run and so are removed with it.
	ephemeral bool
}

// SetupRequest is one ephemeral setup session to start: a Kit-profile
// container whose only job is to run a CLI's own login so the proxy captures
// the tokens at the token endpoint.
type SetupRequest struct {
	// Session keys the container, network and volumes, and roots its
	// container name.
	Session string
	// Kit is the profile's digest-pinned Kit or published Kit set.
	Kit string
	// Org is the org whose credential bindings the session may use. A setup
	// session carries no task and no identity grant, so its bound services
	// are the org's bindings for the Kit's declared services.
	Org string
}

// LaunchSetup starts an ephemeral setup session's container. Unlike Launch it
// mounts no worktree and runs no worker: the container holds a keepalive and
// the operator's PTY runs the CLI's login inside it. The launch runs the
// Kit's install hooks at the install phase and then enters runtime, exactly
// as a task run does, so a runtime-phase credential's token endpoint is the
// one the proxy intercepts.
func (l *Launcher) LaunchSetup(ctx context.Context, req SetupRequest) (*Run, error) {
	if req.Kit == "" {
		return nil, errors.New("kit profile names no kit")
	}
	if l.Grants == nil || l.Config == nil {
		return nil, errors.New("setup sessions need a session grant registry and a configuration holder")
	}
	if err := l.startEgress(ctx); err != nil {
		return nil, err
	}
	k, err := l.read(ctx, req.Kit, 0)
	if err != nil {
		return nil, err
	}
	bound, services := orgCredentialKinds(l.Config.Get().Containers.Credentials, req.Org, k.creds)
	token, err := newProxyToken()
	if err != nil {
		return nil, err
	}
	session, err := l.Proxy.Register(egress.SessionOptions{Token: token, Org: req.Org, Network: k.network, Credentials: k.creds, Bound: bound})
	if err != nil {
		return nil, err
	}
	run := &Run{network: "archie-kit-" + req.Session, token: session.Token(), execution: req.Session, grants: l.Grants, ephemeral: true}
	// Bound is deliberately empty here: a credential file renders the stored
	// scopes and expiry, and there is no stored token set yet -- that is the
	// state this session exists to change. The Kit's sentinels still reach the
	// harness environment.
	launch, err := kit.Assemble(k.plan, k.img, kit.LaunchParams{Execution: req.Session, ProxyToken: session.Token(), CAPath: caPath})
	if err != nil {
		l.release(run)
		return nil, err
	}
	run.Harness, run.Volumes = launch.Harness, launch.Volumes
	l.Grants.Grant(session.Token(), req.Org, services)
	if err := l.Networks.Create(ctx, run.network); err != nil {
		l.release(run)
		return nil, err
	}
	// The container runs archie's own agent as a keepalive rather than the
	// image's entrypoint (the harness CLI, which would exit) and rather than an
	// image binary such as sleep, which a Kit image need not provide. The
	// environment stays empty (WorkerEnv nil): the harness env is passed
	// explicitly on the exec, and no worker credential reaches it.
	run.Container, err = l.Pool.AcquireKit(ctx, container.KitSpec{
		Name:    "archie-kit-" + req.Session,
		Image:   req.Kit,
		Network: run.network,
		Worker:  []string{agentPath, "hold"},
		Binds: append([]string{
			l.AgentBinary + ":" + agentPath + ":ro",
			l.CAFile + ":" + caPath + ":ro",
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

// orgCredentialKinds is the kind of each of the Kit's declared services the
// org binds, with no identity grant in the intersection: a setup session acts
// on the org's secret, not on a run's grant.
func orgCredentialKinds(bindings []config.CredentialBinding, org string, creds []spec.CredentialCapability) (map[string]egress.CredentialKind, []string) {
	declared := make([]string, len(creds))
	for i, c := range creds {
		declared[i] = c.Service
	}
	bound := config.ContainerConfig{Credentials: bindings}.BoundCredentials(org, declared, declared)
	kinds := make(map[string]egress.CredentialKind, len(bound))
	services := make([]string, 0, len(bound))
	for service, b := range bound {
		if b.Secret == (config.SecretRef{}) {
			kinds[service] = egress.CredentialOAuth
		} else {
			kinds[service] = egress.CredentialAPIKey
		}
		services = append(services, service)
	}
	return kinds, services
}

// newProxyToken mints a setup session's run credential. It is unguessable
// because it is the container's proxy password.
func newProxyToken() (string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("mint session credential: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
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
	if run.grants != nil {
		run.grants.Revoke(run.token)
	}
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
// credential grant and ends its egress session. A setup session's volumes are
// removed too: they are keyed to one ephemeral session and no later run reuses
// them. A task run keeps its volumes for the life of its execution.
func (l *Launcher) Release(ctx context.Context, run *Run) error {
	l.Pool.Release(ctx, run.Container)
	l.release(run)
	err := l.Networks.Remove(context.WithoutCancel(ctx), run.network)
	if run.ephemeral {
		err = errors.Join(err, l.RemoveVolumes(context.WithoutCancel(ctx), run.Volumes))
	}
	return err
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
