package curator

import (
	"errors"
	"time"

	"github.com/samcharles93/archie-core/internal/domain/lifecycle"
)

// Registry errors.
var (
	ErrDuplicate = lifecycle.ErrDuplicate
	ErrStarted   = lifecycle.ErrStarted
)

// Registry owns curator registration and lifecycle. A curator's failure
// affects only that curator.
type Registry struct {
	*lifecycle.Registry[CuratorEngine, Registrar]
	host Registrar

	// activity tracks recent per-curator run history for observability.
	// It has its own mutex.
	activity *activityTracker
}

// NewRegistry builds the family registry. A nil Clock is replaced with the
// system clock; every other nil service is valid until a curator declares
// the capability that requires it.
func NewRegistry(host Registrar) *Registry {
	if host.Clock == nil {
		host.Clock = systemClock{}
	}
	return &Registry{
		Registry: lifecycle.New[CuratorEngine, Registrar]("curator", host.Log),
		host:     host,
		activity: newActivityTracker(),
	}
}

// RecordActivity records one pass's outcome for the named curator.
func (r *Registry) RecordActivity(name string, at time.Time, actions []Action) {
	r.activity.record(name, at, actions)
}

// Activity returns the named curator's recorded activity, if any has been
// recorded yet.
func (r *Registry) Activity(name string) (Activity, bool) {
	return r.activity.snapshot(name)
}

// Register validates the curator's manifest and binds it to a filtered host
// view. Failures affect only that curator.
func (r *Registry) Register(c CuratorEngine) error {
	if lifecycle.IsNil(c) {
		return errors.New("curator: nil engine")
	}
	m := c.Manifest()
	if err := m.Validate(); err != nil {
		return err
	}
	if err := r.validateDeclared(m); err != nil {
		return err
	}
	return r.Add(c, r.filter(m))
}

// Host returns the raw registrar bundle the registry was built with. The
// daemon itself is never part of it.
func (r *Registry) Host() Registrar {
	return r.host
}

// validateDeclared enforces "only the host services a curator's declared
// shape requires": a declared capability must have a host service behind it.
// A curator can never discover the daemon or an untyped hook map here — the
// registrar is the only channel.
func (r *Registry) validateDeclared(m Manifest) error {
	if len(m.Tools) > 0 && r.host.Tools == nil {
		return errors.New("curator manifest: declares tools but the registrar has no ToolBuilder")
	}
	if m.Skills && r.host.Skills == nil {
		return errors.New("curator manifest: declares the skills capability but the registrar has no SkillStore")
	}
	if m.MemoryEngine != "" && r.host.MemoryEngines == nil {
		return errors.New("curator manifest: declares a memory engine but the registrar has no MemoryEngineSource")
	}
	if m.Conversations && r.host.Conversations == nil {
		return errors.New("curator manifest: declares conversation history but the registrar has no ConversationSource")
	}
	return nil
}

// filter returns the registrar view narrowed to the curator's declared
// capabilities. Events and Clock are always present (activity must stay
// attributable and time is universal); model access is present only for
// agentic curators; every capability service is present only when declared.
func (r *Registry) filter(m Manifest) Registrar {
	v := r.host
	if !agentic(m) {
		v.LLM = nil
	}
	if len(m.Tools) == 0 {
		v.Tools = nil
	}
	if !m.Skills {
		v.Skills = nil
	}
	if m.MemoryEngine == "" {
		v.MemoryEngines = nil
	}
	if !m.Conversations {
		v.Conversations = nil
	}
	return v
}

func agentic(m Manifest) bool {
	return len(m.Tools) > 0 || m.Skills || m.MemoryEngine != "" || m.Conversations
}

func (r *Registry) replaceDefinitions(candidate *Registry) {
	r.Replace(func(c CuratorEngine) bool {
		_, custom := c.(*DefinitionEngine)
		return custom
	}, candidate.Registry)
}
