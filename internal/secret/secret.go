package secret

// SecretRef references a secret stored in a named engine. It replaces
// the previous TokenEnv/APIKeyEnv string fields throughout config.
//
// Format in TOML/YAML:
//
//	[forge]
//	token = {engine = "env", key = "GITEA_TOKEN"}
//
//	[providers.deepseek]
//	api_key = {engine = "bws", key = "DEEPSEEK_API_KEY"}
//
// The zero value (empty engine, empty key) resolves to "" with no error,
// for config fields where the secret is optional (e.g. an unconfigured
// channel token).
type SecretRef struct {
	Engine string `toml:"engine" yaml:"engine" json:"engine"`
	Key    string `toml:"key" yaml:"key" json:"key"`
}

// Resolve resolves this reference through reg. Returns "" with no error
// when the reference is empty (engine and key both empty — the zero
// value means "not configured").
func (s SecretRef) Resolve(reg *Registry) (string, error) {
	if s.Engine == "" && s.Key == "" {
		return "", nil
	}
	return reg.Resolve(s)
}
