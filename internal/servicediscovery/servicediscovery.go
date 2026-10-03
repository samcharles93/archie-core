package servicediscovery

import (
	"context"
	"errors"
)

// ErrNotInstalled reports that a service was never installed. Callers treat
// the capability as disabled.
var ErrNotInstalled = errors.New("servicediscovery: service not installed")

// Endpoint is one resolved instance of a service.
type Endpoint struct {
	// Service is the name the endpoint registered under.
	Service string `json:"service,omitempty"`

	// ID distinguishes multiple instances of the same service. It is the
	// per-instance identifier; two endpoints with the same Service and ID are
	// the same instance. An ID must not contain the registry's key separator
	// (see the NATS implementation's key scheme).
	ID string `json:"id"`

	// Address is the dialable host:port for the instance.
	Address string `json:"address"`
}

// EventKind is the kind of membership change an [Event] describes.
type EventKind int

const (
	// Join reports that an endpoint became live.
	Join EventKind = iota

	// Leave reports that an endpoint left: it was unregistered, its heartbeat
	// expired, or its process stopped.
	Leave
)

// String returns a human-readable kind, for logging and tests.
func (k EventKind) String() string {
	switch k {
	case Join:
		return "join"
	case Leave:
		return "leave"
	default:
		return "unknown"
	}
}

// Event is a single membership change for one endpoint.
type Event struct {
	// Endpoint is the instance that joined or left. On a [Leave] the Address
	// may be empty: the registry observing an entry disappear can recover the
	// instance's ID from the key but not the address it carried. The ID
	// uniquely identifies the instance.
	Endpoint Endpoint

	// Kind is [Join] or [Leave].
	Kind EventKind
}

// ServiceRegistry resolves and watches the live endpoints of a named service.
// It models network endpoints only. In-process services do not register here;
// application composition selects local adapters independently of discovery.
type ServiceRegistry interface {
	// Resolve returns service's live endpoints, possibly none, or
	// ErrNotInstalled.
	Resolve(ctx context.Context, service string) ([]Endpoint, error)

	// Watch emits Join and Leave events for service until ctx ends, or returns
	// ErrNotInstalled.
	Watch(ctx context.Context, service string) (<-chan Event, error)
}
