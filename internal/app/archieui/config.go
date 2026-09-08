package archieui

import (
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"

	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/infrastructure/configuration"
	"github.com/samcharles93/archie-core/internal/webui"
)

// Resolve produces the effective Options: flag values win, and a field the
// operator left empty falls back to the configuration file's projection, then
// to a process default. It ends with validate, so a caller that gets no error
// holds a startable configuration.
//
// The projection is an allowlist of exactly six values --
// [services.gateway].{target,target_token}, [services.state].{target,
// target_token} and [web].{listen,trust_forwarded_headers}. The UI reads the
// same file the daemon does because operators already keep the service
// endpoints there, but Options has no field for anything else in it: the
// daemon's forge credentials, model catalog, workflow routing, NATS settings,
// filesystem jail and agent/container settings have nowhere to land
// (docs/prds/ui-service-boundary.md:87-89).
func Resolve(o Options, log *slog.Logger) (Options, error) {
	if o.Config != "" {
		cfg, found, err := readProjection(o.Config, o.Overlay, log)
		if err != nil {
			return Options{}, err
		}
		if found {
			o = merge(o, cfg)
		}
	}
	o = withDefaults(o)
	token, err := resolveToken(o)
	if err != nil {
		return Options{}, err
	}
	o.Token = token
	if err := o.validate(); err != nil {
		return Options{}, err
	}
	return o, nil
}

// readProjection loads the configuration source and returns the six values
// the UI process is allowed to see. An absent source is not an error (the
// process is fully drivable by flags); an unreadable or invalid one is.
func readProjection(path, overlay string, log *slog.Logger) (projection, bool, error) {
	if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
		return projection{}, false, nil
	}
	doc, err := configuration.New(log).Resolve(path, overlay)
	if err != nil {
		return projection{}, false, fmt.Errorf("read ui configuration: %w", err)
	}
	return project(doc.Config), true, nil
}

// projection is the file's contribution, narrowed at the point of decode so
// no other configuration value is carried past this function.
type projection struct {
	listen                string
	trustForwardedHeaders bool
	gateway               ServiceTarget
	state                 ServiceTarget
}

func project(cfg config.Config) projection {
	// Token resolution mirrors the daemon's (stateStoreResolvedToken /
	// gatewayResolvedToken): the explicit [services.*].target_token key,
	// then the secret/env var. The config loader does not expand env, so a
	// deployment that presents the gateway/state tokens by environment —
	// which config.example.toml documents for remote consumers — would
	// otherwise leave the UI with an empty token and staterpc.Dial would
	// refuse a non-loopback start.
	gatewayToken := cfg.Services.Gateway.TargetToken
	if gatewayToken == "" {
		gatewayToken = os.Getenv("GATEWAY_TOKEN")
	}
	stateToken := cfg.Services.State.TargetToken
	if stateToken == "" {
		stateToken = os.Getenv("STATE_STORE_TOKEN")
	}
	return projection{
		listen:                cfg.Web.Listen,
		trustForwardedHeaders: cfg.Web.TrustForwardedHeaders,
		gateway:               ServiceTarget{Target: cfg.Services.Gateway.Target, Token: gatewayToken},
		state:                 ServiceTarget{Target: cfg.Services.State.Target, Token: stateToken},
	}
}

// merge fills only the fields the flags left empty.
func merge(o Options, p projection) Options {
	if o.Listen == "" {
		o.Listen = p.listen
	}
	if o.TrustForwardedHeaders == nil {
		trust := p.trustForwardedHeaders
		o.TrustForwardedHeaders = &trust
	}
	if o.Gateway.Target == "" {
		o.Gateway.Target = p.gateway.Target
	}
	if o.Gateway.Token == "" {
		o.Gateway.Token = p.gateway.Token
	}
	if o.State.Target == "" {
		o.State.Target = p.state.Target
	}
	if o.State.Token == "" {
		o.State.Token = p.state.Token
	}
	return o
}

func withDefaults(o Options) Options {
	// This is the one config key the UI and the daemon read with opposite
	// meanings: in the daemon, Web.Listen = "off" disables the dashboard
	// (config.Config "off" is a sentinel, not an address). In a dedicated UI
	// process there is no sense disabling the only thing it does, so "off"
	// falls through to the default listener. None of the other projected
	// values is overloaded this way; if "off" ever means something else here
	// it must be pinned with a test, because it is exactly where an operator
	// preparing the split would set it.
	if o.Listen == "" || o.Listen == "off" {
		o.Listen = defaultListen
	}
	if o.DependencyTimeout <= 0 {
		o.DependencyTimeout = defaultDependencyTimeout
	}
	if o.EventPollInterval <= 0 {
		o.EventPollInterval = webui.DefaultEventPollInterval
	}
	if o.ReadHeaderTimeout <= 0 {
		o.ReadHeaderTimeout = defaultReadHeaderTimeout
	}
	if o.ShutdownTimeout <= 0 {
		o.ShutdownTimeout = defaultShutdownTimeout
	}
	return o
}

// resolveToken returns the dashboard token, minting and persisting one when
// the operator named a token file and gave no token directly. Naming the file
// is how a non-loopback listener opts in to a generated token instead of
// being refused.
func resolveToken(o Options) (string, error) {
	if o.Token != "" || o.TokenFile == "" {
		return o.Token, nil
	}
	return webui.LoadOrCreateToken(o.TokenFile)
}

// validate holds the PRD's security posture (lines 116-118): a loopback
// listener may omit the dashboard token, a non-loopback listener requires one
// and fails closed without it. Unlike the daemon, which mints a token
// silently, this process refuses to start -- an operator who exposed the
// dashboard by accident learns about it here rather than from the log.
func (o Options) validate() error {
	if o.Gateway.Target == "" {
		return fmt.Errorf("gateway target is required (-gateway-target or [services.gateway].target)")
	}
	if o.State.Target == "" {
		return fmt.Errorf("state store target is required (-state-target or [services.state].target)")
	}
	if !webui.IsLoopback(o.Listen) && o.Token == "" {
		return fmt.Errorf(
			"ui listen address %q is non-loopback; a reachable dashboard requires a token (-token) or a token file to mint one (-token-file) (docs/prds/ui-service-boundary.md, listen and authentication)",
			o.Listen,
		)
	}
	return nil
}
