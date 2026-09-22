package config

// CuratorDefinition is one [[curators]] entry: a curator definition as
// seed data. It carries the curator.Manifest shape verbatim plus the
// identity, lifecycle flag, and free-form instructions the manifest cannot
// express. A definition missing interval is refused by configuration
// validation, never defaulted.
type CuratorDefinition struct {
	Name         string `toml:"name" yaml:"name" json:"name"`
	Enabled      bool   `toml:"enabled" yaml:"enabled" json:"enabled"`
	Instructions string `toml:"instructions" yaml:"instructions" json:"instructions"`

	Interval      Duration `toml:"interval" yaml:"interval" json:"interval"`
	Cooldown      Duration `toml:"cooldown" yaml:"cooldown" json:"cooldown"`
	OnInput       bool     `toml:"on_input" yaml:"on_input" json:"on_input"`
	Tools         []string `toml:"tools" yaml:"tools" json:"tools"`
	Skills        bool     `toml:"skills" yaml:"skills" json:"skills"`
	MemoryEngine  string   `toml:"memory_engine" yaml:"memory_engine" json:"memory_engine"`
	Conversations bool     `toml:"conversations" yaml:"conversations" json:"conversations"`
	Model         string   `toml:"model" yaml:"model" json:"model"`
}
