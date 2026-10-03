package config

// CuratorDefinition is one [[curators]] entry. Interval is required.
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
