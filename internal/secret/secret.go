package secret

import (
	"fmt"
	"os"
)

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
// The zero value (empty engine, empty key) resolves to "" with no error
// for backward compatibility with optional secrets.
type SecretRef struct {
	Engine string `toml:"engine" yaml:"engine" json:"engine"`
	Key    string `toml:"key" yaml:"key" json:"key"`
}

// Resolve resolves this reference through the default registry. Returns
// "" when the reference is empty (engine and key both empty — the
// zero-value means "not configured").
func (s SecretRef) Resolve(reg *Registry) (string, error) {
	if s.Engine == "" && s.Key == "" {
		return "", nil
	}
	return reg.Resolve(s)
}

// ResolveOrEnv resolves through the registry, falling back to the
// environment when no engine is configured but a key is set. This
// provides backward compatibility during migration.
func (s SecretRef) ResolveOrEnv(reg *Registry) (string, error) {
	if s.Engine == "" && s.Key == "" {
		return "", nil
	}
	if s.Engine == "" {
		// Fallback: treat Key as an env var name.
		if v, ok := os.LookupEnv(s.Key); ok {
			return v, nil
		}
		return "", fmt.Errorf("secret env var %q is not set", s.Key)
	}
	return reg.Resolve(s)
}
