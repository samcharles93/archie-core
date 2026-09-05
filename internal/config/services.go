package config

// Services selects contract adapters at application composition time.
type Services struct {
	Gateway ServiceConnection `toml:"gateway" yaml:"gateway"`
}

// ServiceConnection selects local calls or a TLS gRPC target. It does not
// register services or start listeners. Changes require a daemon restart.
type ServiceConnection struct {
	Mode   string `toml:"mode" yaml:"mode"`
	Target string `toml:"target" yaml:"target"`
}
