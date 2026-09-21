// Package memory is the memory engine family: the authoritative store for
// durable observations, addressed by typed scope. See
// docs/prds/memory-engine-unification.md for the decision this contract
// implements.
//
// One engine, four scopes. A caller names the scopes it may read or write
// (Scope, Subject) and the engine applies no policy of its own: an engine
// that enforced access would need to know about channels, sessions and
// bindings, and every backend would reimplement it. Isolation then falls out
// mechanically, because ScopeAgent{a} and ScopeAgent{b} are different
// storage keys.
//
// The family follows the strict plugin engine rule: a typed contract
// (MemoryEngine) with real domain operations (create, read, update, delete,
// revisions — not just Name/Version), an owning registry (Registry) with
// start/health/stop and shutdown ordering, and narrow typed host access
// (Registrar) — an engine never receives the daemon or an untyped hook map.
//
// This package owns the contract and the family's policy that is independent
// of any backend (the content scanner). internal/memory's MemoryProvider and
// Manager predate the domain migration and are the legacy runtime path,
// deleted in slice 5 of the same PRD; nothing here depends on them.
package memory

import (
	"context"
	"errors"
	"fmt"
	"strconv"
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

// ScopeKind names one of the four durable memory scopes
// (docs/architecture/agent-system.md, "Memory scopes").
type ScopeKind string

const (
	// ScopeGlobal is shared across the application. Never model-writable:
	// writing it is an operator action. It is part of the contract because
	// the read set includes it.
	ScopeGlobal ScopeKind = "global"
	// ScopeAgent is private to one Agent. Agent-wide is not a synonym for
	// shared; ScopeAgent{a} and ScopeAgent{b} are different keys.
	ScopeAgent ScopeKind = "agent"
	// ScopeUser is one user, across every Agent serving them.
	ScopeUser ScopeKind = "user"
	// ScopeAgentUser is one Agent's relationship with one user: the only
	// home for relationship facts, and the scope the session curator
	// writes.
	ScopeAgentUser ScopeKind = "agent-user"
)

// AgentID and IdentityID are opaque. This package never resolves them,
// canonicalises them, or invents one from a channel: resolution happens
// where the channel's native identity is in hand (the gateway turn path),
// because a channel's sender id is not necessarily a person -- treating one
// as a person would give a URL an identity.
type (
	AgentID    string
	IdentityID string
)

// Scope addresses one region of memory. Which components are required is a
// function of Kind, and Validate is the only place that rule lives.
type Scope struct {
	Kind  ScopeKind
	Agent AgentID
	User  IdentityID
}

// Validate rejects an unknown kind, a missing component, and — just as
// importantly — a component present where it is not required, so no two
// spellings address the same region.
func (s Scope) Validate() error {
	switch s.Kind {
	case ScopeGlobal:
		if s.Agent != "" || s.User != "" {
			return fmt.Errorf("memory scope global: takes no agent or user, got agent %q user %q", s.Agent, s.User)
		}
	case ScopeAgent:
		if s.Agent == "" {
			return errors.New("memory scope agent: agent must not be empty")
		}
		if s.User != "" {
			return fmt.Errorf("memory scope agent: takes no user, got %q", s.User)
		}
	case ScopeUser:
		if s.User == "" {
			return errors.New("memory scope user: user must not be empty")
		}
		if s.Agent != "" {
			return fmt.Errorf("memory scope user: takes no agent, got %q", s.Agent)
		}
	case ScopeAgentUser:
		if s.Agent == "" || s.User == "" {
			return fmt.Errorf("memory scope agent-user: agent and user both required, got agent %q user %q", s.Agent, s.User)
		}
	default:
		return fmt.Errorf("memory scope: unknown kind %q", s.Kind)
	}
	return nil
}

// Key returns the scope's canonical storage key. Each component is
// length-prefixed, so two different scopes can never share a key even when
// an id embeds the separator byte — which matters because AgentID and
// IdentityID are opaque and come from channels this package does not
// control.
//
// An invalid scope returns "", which a storage key can never legitimately
// be: callers must Validate first. Every engine path does.
func (s Scope) Key() string {
	switch s.Kind {
	case ScopeGlobal:
		return "global"
	case ScopeAgent:
		return "agent:" + lengthPrefixed(string(s.Agent))
	case ScopeUser:
		return "user:" + lengthPrefixed(string(s.User))
	case ScopeAgentUser:
		return "agent-user:" + lengthPrefixed(string(s.Agent)) + ":" + lengthPrefixed(string(s.User))
	default:
		return ""
	}
}

// String renders the scope for logs and error messages in Key's canonical
// form.
func (s Scope) String() string { return s.Key() }

// lengthPrefixed renders s as "<byteLen>:<s>", so a component containing ':'
// (or any other byte) cannot be confused with the boundary between
// components.
func lengthPrefixed(s string) string {
	return strconv.Itoa(len(s)) + ":" + s
}

// Subject is a turn's resolved identity: one value, supplied by the
// composition per channel, never inferred by the engine.
type Subject struct {
	// AgentID is the Agent serving the turn. Required in practice; an empty
	// AgentID simply yields no agent-addressed scopes.
	AgentID AgentID
	// UserID is the initiating user's identity. Empty when the channel
	// carries no per-person identity (the dashboard, a webhook) — which
	// means "show none of it", never "fall back to a wider scope".
	UserID IdentityID
}

// Scopes returns the scopes a read for this subject names, narrowest first:
// agent-user, agent, user, then global last, because global is the only
// scope every subject shares.
//
// The read path hands exactly this set to the engine and the engine unions
// nothing beyond it.
func (s Subject) Scopes() []Scope {
	return append(s.WritableScopes(), Scope{Kind: ScopeGlobal})
}

// WritableScopes returns the scopes a write for this subject may name —
// Scopes() minus global, which is never model-writable. A component the
// subject does not carry removes every scope that requires it.
func (s Subject) WritableScopes() []Scope {
	var scopes []Scope
	if s.AgentID != "" && s.UserID != "" {
		scopes = append(scopes, Scope{Kind: ScopeAgentUser, Agent: s.AgentID, User: s.UserID})
	}
	if s.AgentID != "" {
		scopes = append(scopes, Scope{Kind: ScopeAgent, Agent: s.AgentID})
	}
	if s.UserID != "" {
		scopes = append(scopes, Scope{Kind: ScopeUser, User: s.UserID})
	}
	return scopes
}

// Store is the real domain operations every memory backend implements.
// Split out from MemoryEngine so the two concerns (storage vs.
// identity/lifecycle) stay separately named and independently testable.
//
// Every method that touches one record takes the record's Scope: a record id
// alone does not locate a scope, and an engine that had to search for one
// would be reimplementing the caller's addressing.
type Store interface {
	// Create records one new memory and returns the stored Record,
	// including the identifier Get/Update/Forget/Revisions later need.
	Create(ctx context.Context, in NewRecord) (Record, error)
	// Get returns one record by id within scope. A missing record is
	// ErrNotFound.
	Get(ctx context.Context, scope Scope, id RecordID) (Record, error)
	// Query returns the live records of q.Scopes, grouped in the order
	// those scopes were named and ordered within a scope by the engine
	// (most recent first is the expected choice). Heads only: superseded
	// states are Revisions.
	Query(ctx context.Context, q Query) ([]Record, error)
	// List returns every live record of one scope, for inspection and
	// deletion flows rather than relevance-ranked recall. Heads only.
	List(ctx context.Context, scope Scope) ([]Record, error)
	// Update supersedes a record's content and returns the new state. The
	// superseded state is retained and readable through Revisions.
	// Unaddressed, or already superseded when RecordUpdate.Expected is
	// set, returns ErrNotFound or ErrStaleRevision respectively.
	Update(ctx context.Context, in RecordUpdate) (Record, error)
	// Forget removes one record's live state, retaining a deleted revision
	// so its provenance outlives its content. Forgetting a record that
	// does not exist is not an error: the end state (id absent) already
	// holds.
	Forget(ctx context.Context, scope Scope, id RecordID) error
	// Revisions returns one record's retained states, oldest first,
	// including the deleted revision Forget records. A record that never
	// existed is not an error: the result is empty.
	Revisions(ctx context.Context, scope Scope, id RecordID) ([]Revision, error)
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

// RecordID identifies one record within its scope, stable for the record's
// whole life: assigned once at Create, independent of content, so an edit
// somewhere else can never orphan an id.
type RecordID string

// NewRecord is one memory submitted for creation. Revision, timestamps and
// id are stamped by the engine; Scope is what the record will be addressed
// by for the rest of its life.
type NewRecord struct {
	Scope   Scope
	Kind    string
	Content string
	// Author is the acting identity that wrote the record — an agent id, or
	// a producer's own name ("operator", "session-memory curator"). Free
	// text: the engine retains it, it does not interpret it.
	Author string
	// OriginUser is the user whose interaction produced the record, when
	// one is known. Distinct from Scope.User: a curator writing
	// agent-user scope has both, but a record about a user may be written
	// into agent scope by an operator.
	OriginUser IdentityID
	// Source is the producer's own label for where the record came from
	// (a tool name, a curator pass, a session id). Free text.
	Source string
}

// Validate checks what every engine needs before writing: an addressable
// scope and content. Kind's format belongs to the backend (the builtin
// engine maps it to a section name), so it is validated there.
//
// Implementations must also scan Content before persisting it — see
// ScanContent and docs/prds/memory-engine-unification.md §7.
func (n NewRecord) Validate() error {
	if err := n.Scope.Validate(); err != nil {
		return err
	}
	if n.Content == "" {
		return errors.New("memory record: content must not be empty")
	}
	return nil
}

// Query asks an engine to recall records within the caller-named scopes.
type Query struct {
	// Scopes is the read set, narrowest first (Subject.Scopes()). The
	// engine unions exactly these and applies no policy of its own.
	Scopes []Scope
	// Text filters by content substring when non-empty. It stays unused by
	// the prompt path; ranked retrieval is a deliberately open question.
	Text string
	// Limit bounds the number of records returned. Zero lets the engine
	// choose a default; there is no way to request "unlimited" — List
	// exists for that.
	Limit int
}

// Validate rejects an empty or invalid read set, and a negative limit.
func (q Query) Validate() error {
	if len(q.Scopes) == 0 {
		return errors.New("memory query: at least one scope is required")
	}
	for _, scope := range q.Scopes {
		if err := scope.Validate(); err != nil {
			return fmt.Errorf("memory query: %w", err)
		}
	}
	if q.Limit < 0 {
		return fmt.Errorf("memory query: limit must not be negative, got %d", q.Limit)
	}
	return nil
}

// RecordUpdate supersedes one record's content. It deliberately cannot name
// a Scope: a record may never move between scopes, so the API makes that
// unrepresentable rather than validating it.
type RecordUpdate struct {
	// Scope and ID address the record being superseded.
	Scope Scope
	ID    RecordID
	// Content replaces the live content. Required.
	Content string
	// Expected optionally names the revision this update supersedes. Zero
	// means "whatever is live". A mismatch returns ErrStaleRevision, which
	// matters because two processes (archied and archie-gateway) can hold
	// the same scope's file.
	Expected int
	// Author and Source are refreshed on the new revision, with the same
	// meaning as in NewRecord.
	Author string
	Source string
}

// Validate checks the address and the replacement content.
func (u RecordUpdate) Validate() error {
	if err := u.Scope.Validate(); err != nil {
		return err
	}
	if u.ID == "" {
		return errors.New("memory record update: id must not be empty")
	}
	if u.Content == "" {
		return errors.New("memory record update: content must not be empty")
	}
	if u.Expected < 0 {
		return fmt.Errorf("memory record update: expected revision must not be negative, got %d", u.Expected)
	}
	return nil
}

// Record is one stored memory as returned by Create, Get, Query, List or
// Update. It carries the provenance agent-system.md requires be retained:
// scope, kind, content, revision, author, originating user, source, and
// timestamps.
type Record struct {
	ID         RecordID
	Scope      Scope
	Kind       string
	Content    string
	Revision   int
	Author     string
	OriginUser IdentityID
	Source     string
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// Revision is one retained state of a record: a state that was live and has
// been superseded, or the deletion marker Forget records. The record's own
// Revision field is the revision this state carried while live.
type Revision struct {
	Record Record
	// SupersededAt is when this state stopped being live (replaced by an
	// Update, or removed by a Forget).
	SupersededAt time.Time
	// Deleted marks the revision Forget recorded: Record.Content is the
	// last live content, retained so provenance outlives it.
	Deleted bool
}

// Store errors. Both cross a producer/consumer boundary, and both are
// expected conditions rather than failures: a missing record and a losing
// race between two processes holding one scope.
var (
	// ErrNotFound is returned when a record does not exist in the named
	// scope. Get and Update return it; Forget and Revisions do not, because
	// their end state already holds.
	ErrNotFound = errors.New("memory record not found")
	// ErrStaleRevision is returned when RecordUpdate.Expected names a
	// revision that is no longer live.
	ErrStaleRevision = errors.New("memory record revision is stale")
)
