package config

// Services selects contract adapters at application composition time.
type Services struct {
	Gateway ServiceConnection `toml:"gateway" yaml:"gateway"`
	// State selects the State Store contract adapter. Unlike Gateway, an
	// empty Target means local (the *store.Store in-process adapter, the
	// Phase 2 default): the State Store service is not yet extracted into
	// its own process (see docs/prds/state-store-contract.md §10). Setting
	// Target dials the gRPC StateStoreService instead.
	State ServiceConnection `toml:"state" yaml:"state"`
}

// ServiceConnection is the archie-gateway process's gRPC address. It does not
// register services or start listeners. Changes require a daemon restart.
type ServiceConnection struct {
	Target string `toml:"target" yaml:"target"`
}
