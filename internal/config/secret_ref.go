package config

// SecretRef references a secret in a named engine:
//
//	[forge]
//	token = {engine = "env", key = "GITEA_TOKEN"}
//
// The zero value resolves to "".
type SecretRef struct {
	Engine string `toml:"engine" yaml:"engine" json:"engine"`
	Key    string `toml:"key" yaml:"key" json:"key"`
}
