// Package archieui composes the standalone UI Service: the operator-facing
// HTTP surface and the compiled SPA, served against a Gateway and a State
// Store running in other processes.
//
// docs/prds/ui-service-boundary.md is the authority. The two rules that shape
// this package are that the UI owns only process-local settings ("HTTP listen
// address and read/header/shutdown timeouts; UI asset source or build mode;
// UI authentication mode and token reference; Gateway endpoint and credential
// reference; State Store endpoint and credential reference; readiness
// endpoint and dependency timeout policy"), and that it "must never implement
// [configuration] policy by holding or mutating a shared config.Holder".
//
// It deliberately lives outside internal/app/archied: that package's
// dependency graph carries the daemon, forge, container and workflow runtimes
// that the PRD's deletion gate forbids the UI process from linking.
package archieui

import "time"

// Process defaults. Listen matches the dashboard's historical bind
// (internal/infrastructure/configuration/defaults.go) so an operator moving
// the dashboard into its own process keeps the same URL.
const (
	defaultListen            = "127.0.0.1:8484"
	defaultDependencyTimeout = 5 * time.Second
	defaultReadHeaderTimeout = 5 * time.Second
	defaultShutdownTimeout   = 5 * time.Second
)

// ServiceTarget is one remote contract's endpoint and the service-to-service
// bearer token this process presents to it. It is an endpoint reference, not
// a resolved daemon credential: the UI authenticates each client itself.
type ServiceTarget struct {
	Target string
	Token  string
}

// Options is the UI-owned input DTO. It carries process-local settings and
// endpoint references only -- there is deliberately no config.Config,
// config.Holder, forge credential, model catalog or workflow routing field
// for daemon configuration to arrive through.
type Options struct {
	// Config and Overlay name the configuration source the process reads a
	// narrow projection of. Both are optional: a deployment may pass every
	// setting as a flag.
	Config  string
	Overlay string

	// Listen is the dashboard HTTP bind address.
	Listen string
	// Token gates browser access. Empty is permitted only for a loopback
	// listener; see Options.validate.
	Token string
	// TokenFile is where the dashboard token is persisted, minted on first
	// use. It is the explicit opt-in to a non-loopback listener: without it
	// a reachable bind must be given a token.
	TokenFile string
	// TrustForwardedHeaders is tri-state so an explicit false on the command
	// line can override a configuration file that enables it. Nil means the
	// operator did not say, and the file (else false) decides.
	TrustForwardedHeaders *bool

	// Gateway and State are the two remote contracts the dashboard consumes.
	Gateway ServiceTarget
	State   ServiceTarget

	// DependencyTimeout bounds each readiness probe's call to a dependency.
	DependencyTimeout time.Duration
	// EventPollInterval bounds how stale the dashboard's activity feed can
	// be. This process owns no event bus, so live activity is a poll of the
	// State Store's event cursor; see webui.DefaultEventPollInterval and
	// docs/architecture/migration-decisions.md, "Dashboard live event
	// delivery".
	EventPollInterval time.Duration
	// ReadHeaderTimeout and ShutdownTimeout bound the HTTP lifecycle.
	ReadHeaderTimeout time.Duration
	ShutdownTimeout   time.Duration
}

// trustForwardedHeaders reports the resolved value, treating "not specified"
// as off: trusting forwarded headers unconditionally lets an untrusted client
// spoof the Origin scheme check.
func (o Options) trustForwardedHeaders() bool {
	return o.TrustForwardedHeaders != nil && *o.TrustForwardedHeaders
}
