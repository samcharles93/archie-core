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

// Resolve returns the effective Options: flags first, then the config file's
// projection, then defaults, and validates the result.
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
	o = withEnvTokens(o)
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
	if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) && overlay == "" {
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
	capture               CaptureOptions
}

func project(cfg config.Config) projection {
	return projection{
		listen:                cfg.Web.Listen,
		trustForwardedHeaders: cfg.Web.TrustForwardedHeaders,
		gateway:               ServiceTarget{Target: cfg.Services.Get(config.ServiceNameGateway).Target, Token: cfg.Services.Get(config.ServiceNameGateway).TargetToken},
		state:                 ServiceTarget{Target: cfg.Services.Get(config.ServiceNameState).Target, Token: cfg.Services.Get(config.ServiceNameState).TargetToken},
		capture: CaptureOptions{
			Retention:     cfg.Capture.Retention.Std(),
			MaxEvents:     cfg.Capture.MaxEvents,
			MaxBodyBytes:  cfg.Capture.MaxBodyBytes,
			RatePerSecond: cfg.Capture.RatePerSecond,
			RateBurst:     cfg.Capture.RateBurst,
		},
	}
}

// withEnvTokens fills service tokens left unset from the environment.
func withEnvTokens(o Options) Options {
	if o.Gateway.Token == "" {
		o.Gateway.Token = os.Getenv("GATEWAY_TOKEN")
	}
	if o.State.Token == "" {
		o.State.Token = os.Getenv("STATE_STORE_TOKEN")
	}
	return o
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
	if o.Capture.Retention <= 0 {
		o.Capture.Retention = p.capture.Retention
	}
	if o.Capture.MaxEvents <= 0 {
		o.Capture.MaxEvents = p.capture.MaxEvents
	}
	if o.Capture.MaxBodyBytes <= 0 {
		o.Capture.MaxBodyBytes = p.capture.MaxBodyBytes
	}
	if o.Capture.RatePerSecond <= 0 {
		o.Capture.RatePerSecond = p.capture.RatePerSecond
	}
	if o.Capture.RateBurst <= 0 {
		o.Capture.RateBurst = p.capture.RateBurst
	}
	return o
}

func withDefaults(o Options) Options {
	// "off" disables the daemon's dashboard but means the default listener here.
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

// Unlike the daemon, which mints a token silently, this process refuses to
// start -- an operator who exposed the dashboard by accident learns about it
// here rather than from the log.
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
