package config

// Services maps a registered service name to its connection settings, keyed by
// the [services.<name>] section it was decoded from.
//
// It was a struct with one field per service. That stated each service's name
// three times over -- here, in the loader's defaulting branches, and at every
// consumer's field access -- so a new service meant editing all three and
// nothing prevented them disagreeing. Which names are legitimate now comes
// from RegisterService; see ServiceSpec.
type Services map[string]ServiceConnection

// Get returns the connection configured for name, or the zero value when the
// configuration carries no section for it. A registered service always has an
// entry once defaults have been applied.
func (s Services) Get(name string) ServiceConnection {
	return s[name]
}

// ResolvedToken is the bearer token a client presents when dialling name: the
// explicit target_token if set, otherwise the environment variable the service
// registered. getenv is passed in rather than read directly so this stays free
// of internal/secret, which imports this package.
//
// This replaces two resolvers that were identical apart from the variable name
// each hard-coded.
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

// ServiceConnection is a service's gRPC addresses plus the bearer token a
// client presents when the target is non-loopback. A client reads only Target
// and TargetToken; Listen belongs to the process that owns the service. The
// struct does not itself register services or start listeners. Changes require
// a daemon restart.
type ServiceConnection struct {
	Target string `toml:"target" yaml:"target"`
	// Listen is the address the process that OWNS this service binds
	// (archie-state-store, archie-gateway). It is read only by that process;
	// every client ignores it. Defaulted from the service's registration, so
	// a loaded config is never empty here -- an empty address reaches
	// net.Listen as "any free port", which would silently move the service
	// off the address its clients dial.
	Listen string `toml:"listen" yaml:"listen"`
	// TargetToken is the bearer token a client (daemon or agent) presents
	// when dialing a non-loopback service target, and the standalone
	// archie-state-store server validates. Empty means no token, which is
	// the loopback-only, daemon-only listener topology permitted without
	// auth (docs/prds/state-store-contract.md §9).
	TargetToken string `toml:"target_token" yaml:"target_token"`
}
