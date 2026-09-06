package config

// Services selects contract adapters at application composition time.
type Services struct {
	Gateway ServiceConnection `toml:"gateway" yaml:"gateway"`
	// State selects the State Store contract adapter. Unlike Gateway, an
	// empty Target means local (the *store.Store in-process adapter): the
	// State Store service is not yet extracted into its own process. The
	// standalone archie-state-store binary (.4.3) is the consumer of
	// [services.state].target_token; the daemon does not read it yet, so
	// it remains dead configuration until the daemon points at the
	// extracted store. Setting Target would dial the gRPC StateStoreService.
	State ServiceConnection `toml:"state" yaml:"state"`
}

// ServiceConnection is a contract adapter's gRPC address plus the bearer
// token a client presents when the target is non-loopback. It does not
// register services or start listeners. Changes require a daemon restart.
type ServiceConnection struct {
	Target string `toml:"target" yaml:"target"`
	// TargetToken is the bearer token a client (daemon or agent) presents
	// when dialing a non-loopback service target, and the standalone
	// archie-state-store server validates. Empty means no token, which is
	// the loopback-only, daemon-only listener topology permitted without
	// auth (docs/prds/state-store-contract.md §9).
	TargetToken string `toml:"target_token" yaml:"target_token"`
}
