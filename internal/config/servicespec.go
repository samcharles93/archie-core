package config

import (
	"fmt"
	"sort"
	"strings"
	"sync"
)

// Service contexts. The context decides which addresses a service must end up
// with: a process that owns a service needs a listen address, and a process
// that dials one needs a target.
const (
	// ServiceClient is dialled but not hosted by this repository.
	ServiceClient = "client"
	// ServiceServer is hosted but not dialled.
	ServiceServer = "server"
	// ServiceBoth is hosted by one of our processes and dialled by others.
	ServiceBoth = "both"
)

// ServiceSpec is everything the codebase knows about one service that is not
// operator input. It exists so a service is declared once: the name, its
// defaults, and the environment variable its token falls back to were
// previously a struct field, a defaulting branch, and a hand-copied resolver
// in three separate packages, with nothing keeping them in step.
type ServiceSpec struct {
	// Context is ServiceClient, ServiceServer, or ServiceBoth.
	Context string
	// Name is the [services.<name>] section and the key callers look up.
	Name string
	// Target is the default dial address. Empty means there is no default
	// and the operator must supply one, which is how "services.state.target
	// is required" is stated without a branch that names the service.
	Target string
	// Listen is the default bind address for the process that owns the
	// service. It is never empty for a hosted service: an empty address
	// reaches net.Listen as "any free port", silently moving the service
	// off the address its clients dial.
	Listen string
	// TokenEnv names the environment variable a client falls back to when
	// [services.<name>].target_token is empty.
	TokenEnv string
}

// Registered service names. They live beside the registrations below so the
// spelling a consumer looks up and the spelling that was registered are the
// same token, not two string literals that a typo can separate.
const (
	ServiceNameGateway = "gateway"
	ServiceNameState   = "state"
)

var (
	serviceRegistryMu sync.RWMutex
	serviceRegistry   = map[string]ServiceSpec{}
)

// RegisterService declares one service. It is the single entry point the
// design calls for: adding a service is this call plus its consumer, with no
// new struct field, defaulting branch, or accessor anywhere else.
//
// Re-registering a name replaces the previous spec, so a test can install a
// throwaway service without disturbing the built-ins.
//
// It panics on a registration that cannot be honoured. Registrations come from
// this repository's own init(), never from operator input, so an invalid one is
// a programming error: there is no caller positioned to handle an error return,
// and failing at process start is better than a service that binds somewhere
// nobody dials. Context is enforced here rather than merely documented, so
// "hosted" genuinely implies a bind address.
func RegisterService(context, name, target, listen, tokenEnv string) {
	switch context {
	case ServiceClient, ServiceServer, ServiceBoth:
	default:
		panic(fmt.Sprintf(
			"config.RegisterService(%q): unknown context %q, want %q, %q or %q",
			name, context, ServiceClient, ServiceServer, ServiceBoth,
		))
	}
	if context != ServiceClient && strings.TrimSpace(listen) == "" {
		panic(fmt.Sprintf(
			"config.RegisterService(%q): a %q service needs a default listen address; "+
				"an empty one reaches net.Listen as \"any free port\"",
			name, context,
		))
	}
	serviceRegistryMu.Lock()
	defer serviceRegistryMu.Unlock()
	serviceRegistry[name] = ServiceSpec{
		Context:  context,
		Name:     name,
		Target:   target,
		Listen:   listen,
		TokenEnv: tokenEnv,
	}
}

// LookupService returns the spec registered for name. The registry is the
// authority on which service names exist, which is what lets the loader still
// report a typo'd [services.gatway] once Services is a map that would
// otherwise decode any section at all.
func LookupService(name string) (ServiceSpec, bool) {
	serviceRegistryMu.RLock()
	defer serviceRegistryMu.RUnlock()
	spec, ok := serviceRegistry[name]
	return spec, ok
}

// RegisteredServices returns every spec, ordered by name so defaulting and
// validation run in a stable order regardless of map iteration.
func RegisteredServices() []ServiceSpec {
	serviceRegistryMu.RLock()
	defer serviceRegistryMu.RUnlock()
	out := make([]ServiceSpec, 0, len(serviceRegistry))
	for _, spec := range serviceRegistry {
		out = append(out, spec)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func init() {
	// The two services this repository ships. Everything that differs
	// between them is an argument here, not a branch somewhere else: the
	// gateway's target is defaulted while the State Store's is supplied by
	// the operator, and each falls back to its own token variable.
	RegisterService(ServiceBoth, ServiceNameGateway, "127.0.0.1:8585", "127.0.0.1:8585", "GATEWAY_TOKEN")
	RegisterService(ServiceClient, ServiceNameState, "", "127.0.0.1:9090", "STATE_STORE_TOKEN")
}
