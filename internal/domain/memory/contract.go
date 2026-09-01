// Package memory is the memory engine family: a pluggable backend for
// durable observations about a session or identity, so an external memory
// service (Honcho, or a future alternative) can be bolted on without
// touching the agent loop. See epic archie-core-1786637499161-356-e424e40d.
//
// The family follows the strict plugin engine rule: a typed contract
// (MemoryEngine) with real domain operations (write, query, list, forget —
// not just Name/Version), an owning registry (Registry) with
// start/health/stop and shutdown ordering, and narrow typed host access
// (Registrar) — an engine never receives the daemon or an untyped hook map.
//
// This package defines the contract only. internal/memory's existing
// MemoryProvider and Manager predate the domain migration and are the
// current runtime path; they are not extended in place (see
// archie-core-1786637490069-11-e245c170's notes) and are superseded by this
// family as engines land against it.
package memory

import (
	"context"
	"errors"
	"time"
)

// HealthStatus is an engine's point-in-time health.
type HealthStatus string

const (
	HealthHealthy   HealthStatus = "healthy"
	HealthDegraded  HealthStatus = "degraded"
	HealthUnhealthy HealthStatus = "unhealthy"
)

// Health is a point-in-time engine health report.
type Health struct {
	Status  HealthStatus
	Message string
}

// Manifest is an engine's declared shape. RequiresNetwork marks an engine
// that calls an external service, so a caller can size timeouts and
// failure-isolation expectations accordingly: a non-default engine's
// failure must degrade to a reported error, never take down a turn.
type Manifest struct {
	RequiresNetwork bool
}

// Validate checks the declared shape. It is called by the registry at
// registration; present for symmetry with the curator family and as the
// seam for future required fields, even though no field is required yet.
func (m Manifest) Validate() error { return nil }

// Lifecycle is the start/health/stop contract every engine implements. The
// registry drives it with shutdown ordering and failure isolation; an
// engine's Health must never panic the daemon.
type Lifecycle interface {
	Start(ctx context.Context) error
	Health(ctx context.Context) Health
	Stop(ctx context.Context) error
}

// Store is the real domain operations every memory backend implements —
// what the epic requires beyond identity metadata. Split out from
// MemoryEngine so the two concerns (storage vs. identity/lifecycle) stay
// separately named and independently testable.
type Store interface {
	// Write records one observation and returns the stored Record,
	// including the identifier Forget later needs.
	Write(ctx context.Context, obs Observation) (Record, error)
	// Query returns records relevant to q, most relevant or most recent
	// first — ordering is engine-defined and not part of this contract.
	Query(ctx context.Context, q Query) ([]Record, error)
	// List returns every record for identity, for inspection and
	// deletion flows rather than relevance-ranked recall.
	List(ctx context.Context, identity string) ([]Record, error)
	// Forget permanently removes one record. Forgetting an id that does
	// not exist is not an error: the end state (id absent) already holds.
	Forget(ctx context.Context, id string) error
}

// MemoryEngine is the typed contract every memory backend implements.
// Name/Version are identity, Manifest declares the shape, Store carries
// the real domain operations, Bind receives narrow host access, and
// Lifecycle carries start/health/stop.
type MemoryEngine interface {
	Lifecycle
	Store
	Name() string
	Version() string
	Manifest() Manifest
	// Bind attaches the engine's narrow host access. The registry calls
	// Bind exactly once, at registration.
	Bind(host Registrar)
}

// Observation is one fact to remember, submitted for writing.
type Observation struct {
	// Identity is whose memory this is about (a session id, a user id —
	// engine-defined, but always required so Query/List/Forget have a
	// scope to operate within).
	Identity string
	// Kind is a freeform category ("preference", "fact", "correction").
	Kind string
	// Content is the observation itself.
	Content string
	// At is when the observation was made. Zero means the engine should
	// stamp it with the current time.
	At time.Time
	// Metadata carries engine-agnostic key/value context alongside the
	// observation (e.g. source, confidence).
	Metadata map[string]string
}

// Validate checks the fields every engine needs regardless of backend:
// Identity scopes every other operation, and Content is the whole point
// of writing.
func (o Observation) Validate() error {
	if o.Identity == "" {
		return errors.New("memory observation: identity must not be empty")
	}
	if o.Content == "" {
		return errors.New("memory observation: content must not be empty")
	}
	return nil
}

// Query asks an engine to recall observations relevant to Text within
// Identity's scope.
type Query struct {
	Identity string
	Text     string
	// Limit bounds the number of records returned. Zero lets the engine
	// choose a default; there is no way to request "unlimited" — List
	// exists for that.
	Limit int
}

// Validate checks the field every engine needs to scope a query: Identity.
// Text may be empty (some engines support a scope-only recall), so it is
// not required here.
func (q Query) Validate() error {
	if q.Identity == "" {
		return errors.New("memory query: identity must not be empty")
	}
	return nil
}

// Record is one stored observation as returned by Write, Query, or List.
type Record struct {
	ID       string
	Identity string
	Kind     string
	Content  string
	At       time.Time
	Metadata map[string]string
}
