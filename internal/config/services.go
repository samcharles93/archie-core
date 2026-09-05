package config

// Services selects contract adapters at application composition time.
type Services struct {
	Gateway ServiceConnection `toml:"gateway" yaml:"gateway"`
}

// ServiceConnection is the archie-gateway process's gRPC address. It does not
// register services or start listeners. Changes require a daemon restart.
type ServiceConnection struct {
	Target string `toml:"target" yaml:"target"`
}
