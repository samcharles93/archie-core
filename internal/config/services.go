package config

// Services selects contract adapters at application composition time.
type Services struct {
	Gateway ServiceConnection `toml:"gateway" yaml:"gateway"`
	// State selects the State Store contract adapter. Unlike Gateway, an
	// empty Target means local (the *store.Store in-process adapter): the
	// State Store service is not yet extracted into its own process. This
	// is the ratified presence-based [services.state].target surface
	// (docs/prds/state-store-contract.md §10), consumed by the upcoming
	// standalone archie-state-store binary (.4.3) -- the daemon does not
	// read it yet, so it remains dead configuration until that consumer
	// lands. Setting Target would dial the gRPC StateStoreService.
	State ServiceConnection `toml:"state" yaml:"state"`
}

// ServiceConnection is the archie-gateway process's gRPC address. It does not
// register services or start listeners. Changes require a daemon restart.
type ServiceConnection struct {
	Target string `toml:"target" yaml:"target"`
}
