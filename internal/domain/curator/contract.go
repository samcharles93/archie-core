// Package curator is the curator engine family: long-running agent loops
// that reason over memory and perform small maintenance tasks, rather than
// one hardcoded background job. See epic archie-core-yp9.
//
// A curator is defined by a declared shape (check-in interval, tool set,
// attached memory engine), runs persistently while there is input, and backs
// off to a cooldown when idle. The family follows the strict plugin engine
// rule: a typed contract (CuratorEngine), an owning registry (Registry) with
// start/health/stop and shutdown ordering, and narrow typed host access
// (Registrar) — a curator never receives the daemon or an untyped hook map.
package curator

import (
	"context"
	"errors"
	"strings"
	"time"
)

// HealthStatus is a curator's point-in-time health.
type HealthStatus string

const (
	HealthHealthy   HealthStatus = "healthy"
	HealthDegraded  HealthStatus = "degraded"
	HealthUnhealthy HealthStatus = "unhealthy"
)

// Health is a point-in-time curator health report.
type Health struct {
	Status  HealthStatus
	Message string
}

// Manifest is a curator's declared shape: what input wakes it, how often it
// runs, which tools it may reach, and which memory engine it uses. The
// registry validates the manifest at registration, so a curator can never
// discover capabilities it did not declare.
type Manifest struct {
	// Interval is the check-in cadence: how often a pass is eligible when
	// there is no pending input. Must be positive.
	Interval time.Duration
	// Cooldown is the backoff after a pass that found nothing to do. Zero
	// lets the runtime pick a multiple of Interval.
	Cooldown time.Duration
	// OnInput marks the curator as input-driven: primary input (e.g. a
	// completed chat turn) wakes it before the next interval. Wake events
	// are best-effort nudges; the trigger decision is a deterministic
	// state read at pass time (archie-core-035).
	OnInput bool
	// Tools is the declared tool set: the only tools a pass may reach.
	// Enforcement is mechanical — the pass's tool set is built from this
	// list, never from a broader registry.
	Tools []string
	// Skills declares the skill-maintenance capability (skill curator,
	// archie-core-i7i). The registrar must carry a SkillStore for a
	// curator that declares it.
	Skills bool
	// MemoryEngine names the attached memory engine once the memory engine
	// family exists (archie-core-pek). Empty means the curator does not
	// use memory; a memory operation without a declared engine is
	// rejected.
	MemoryEngine string
	// Model is the model reference ("provider/model") passes use. Empty
	// uses the registrar's default.
	Model string
}

// Validate checks the declared shape. It is called by the registry at
// registration; a curator with an invalid manifest is refused.
func (m Manifest) Validate() error {
	if m.Interval <= 0 {
		return errors.New("curator manifest: interval must be positive")
	}
	for _, tool := range m.Tools {
		if strings.TrimSpace(tool) == "" {
			return errors.New("curator manifest: tool names must be non-empty")
		}
	}
	return nil
}

// Lifecycle is the start/health/stop contract every curator implements. The
// registry drives it with shutdown ordering and failure isolation; a
// curator's Health must never panic the daemon.
type Lifecycle interface {
	Start(ctx context.Context) error
	Health(ctx context.Context) Health
	Stop(ctx context.Context) error
}

// CuratorEngine is the typed contract every curator implements. Name/Version
// are identity, Manifest declares the shape, Check/Pass are the domain
// operations, Bind receives the narrow host access, and Lifecycle carries
// start/health/stop.
type CuratorEngine interface {
	Lifecycle
	Name() string
	Version() string
	Manifest() Manifest
	// Check reports whether a pass should run now. It must be cheap: no
	// model calls, no work a chat turn would wait on. The runtime calls it
	// when deciding whether to run a pass early.
	Check(ctx context.Context) (bool, error)
	// Pass runs one maintenance pass. The runtime guarantees at most one
	// in-flight pass per curator, bounds it with context cancellation and
	// per-pass budgets, and recovers panics so a failing pass never takes
	// down another curator, a chat turn, or the daemon.
	Pass(ctx context.Context, in PassInput) (PassResult, error)
	// Bind attaches the curator's narrow host access. The registry calls
	// Bind exactly once, at registration, with a view filtered to the
	// curator's declared capabilities.
	Bind(host Registrar)
}

// PassInput carries why a pass runs and the input horizon it may consume.
type PassInput struct {
	// Reason describes why this pass runs (e.g. "20 primary turns since
	// last pass" or "daily check-in"). It is recorded on actions and
	// events so curator activity stays attributable.
	Reason string
	// Since is the input horizon: primary input at or after this time is
	// this pass's material. Zero means the curator decides.
	Since time.Time
	// LastPass is when the previous pass started; zero for the first pass.
	LastPass time.Time
}

// PassResult reports what a pass did. Every action is attributable: the
// registry emits them as events and they are inspectable at runtime
// (archie-core-114).
type PassResult struct {
	// Actions records what this pass changed and why.
	Actions []Action
	// NextCheckIn lets a pass suggest the next eligible time (e.g. a
	// longer backoff when there was nothing to do). Zero means the
	// runtime uses the manifest interval and cooldown.
	NextCheckIn time.Time
}

// Action records one change a curator made and why.
type Action struct {
	At     time.Time
	Type   string // e.g. "skill.updated", "memory.written"
	Detail string // what changed, human-readable
	Reason string // why it happened
}
