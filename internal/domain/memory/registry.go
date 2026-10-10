package memory

import (
	"errors"

	"github.com/samcharles93/archie-core/internal/domain/lifecycle"
)

// Registry errors.
var (
	ErrDuplicate = lifecycle.ErrDuplicate
	ErrStarted   = lifecycle.ErrStarted
)

// Registry owns memory engine registration and lifecycle. An engine's
// failure affects only that engine.
type Registry struct {
	*lifecycle.Registry[MemoryEngine, Registrar]
	host Registrar
}

// NewRegistry builds the family registry. A nil Clock is replaced with the
// system clock.
func NewRegistry(host Registrar) *Registry {
	if host.Clock == nil {
		host.Clock = systemClock{}
	}
	return &Registry{Registry: lifecycle.New[MemoryEngine, Registrar]("memory engine", nil), host: host}
}

// Register validates the engine's declared shape and binds it to the
// registrar. A failure affects only that engine.
func (r *Registry) Register(e MemoryEngine) error {
	if lifecycle.IsNil(e) {
		return errors.New("memory: nil engine")
	}
	if err := e.Manifest().Validate(); err != nil {
		return err
	}
	return r.Add(e, r.host)
}
