package config

import (
	"fmt"
	"strings"
)

// Services maps a registered service name to its connection settings. Names
// come from RegisterService.
type Services map[string]ServiceConnection

// Get returns the connection configured for name, or the zero value when the
// configuration carries no section for it. A registered service always has an
// entry once defaults have been applied.
func (s Services) Get(name string) ServiceConnection {
	return s[name]
}

// ResolvedToken returns name's target_token, or the service's registered
// environment variable.
func (s Services) ResolvedToken(name string, getenv func(string) string) string {
	if token := s[name].TargetToken; token != "" {
		return token
	}
	spec, ok := LookupService(name)
	if !ok || spec.TokenEnv == "" {
		return ""
	}
	return getenv(spec.TokenEnv)
}

// RequireTarget returns name's dial address, or an error naming the config
// key.
func (s Services) RequireTarget(name string) (string, error) {
	target := strings.TrimSpace(s[name].Target)
	if target == "" {
		return "", fmt.Errorf("services.%s.target is required", name)
	}
	return target, nil
}

// ServiceConnection is a service's gRPC addresses and client token.
// Restart-required.
type ServiceConnection struct {
	Target string `toml:"target" yaml:"target"`
	// Listen is the address the owning process binds. Always defaulted.
	Listen string `toml:"listen" yaml:"listen"`
	// TargetToken is the bearer token clients present to a non-loopback target.
	// Empty means none.
	TargetToken string `toml:"target_token" yaml:"target_token"`
}
