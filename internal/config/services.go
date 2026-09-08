package config

// Services selects contract adapters at application composition time.
type Services struct {
	Gateway ServiceConnection `toml:"gateway" yaml:"gateway"`
	// State selects the State Store contract adapter. The State Store is its
	// own process (cmd/archie-state-store) and owns archie.db; an empty Target
	// is a startup error in archied/archie-gateway, which no longer serve a
	// local store (docs/prds/state-store-contract.md §12 step 7). The daemon
	// and agent dial the remote StateStoreService and present target_token
	// when the target is non-loopback. Token resolution mirrors the daemon's:
	// the explicit key, then the STATE_STORE_TOKEN secret/env var.
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
