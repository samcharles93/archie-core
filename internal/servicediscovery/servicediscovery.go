package servicediscovery

import (
	"context"
	"errors"
)

// ErrNotInstalled reports that a service has never been installed. It is
// distinct from a service that is installed but currently has no healthy
// endpoint: that case is an empty, non-error result, not ErrNotInstalled.
// Implementations of [ServiceRegistry.Resolve] and [ServiceRegistry.Watch]
// return it (wrapped, matched with errors.Is) when a service's "installed"
// marker is absent. Callers match it and treat the capability as disabled.
var ErrNotInstalled = errors.New("servicediscovery: service not installed")

// Endpoint is one resolved instance of a service.
type Endpoint struct {
	// Service is the logical name the endpoint was registered under. It is
	// redundant within the scope of a single Resolve/Watch call, but it makes
	// the value self-describing and directly usable by a consumer that reads
	// raw registry records (e.g. a custom gRPC resolver) rather than going
	// through ServiceRegistry.
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
type ServiceRegistry interface {
	// Resolve returns the current healthy endpoints for service. It returns
	// [ErrNotInstalled] if service has never been installed. If service is
	// installed but currently has no healthy endpoint, it returns a
	// possibly-empty slice with a nil error -- callers distinguish that from
	// NotInstalled to avoid treating an optional, disabled service as broken.
	Resolve(ctx context.Context, service string) ([]Endpoint, error)

	// Watch returns a channel that emits [Join] and [Leave] events as service's
	// membership changes, until ctx is cancelled. It returns [ErrNotInstalled]
	// if service has never been installed at the time of the call. A service
	// that is installed but currently has no healthy endpoint returns a live
	// channel that simply does not emit until an endpoint joins.
	Watch(ctx context.Context, service string) (<-chan Event, error)
}
