package channels

import "sync"

// State reports the runtime lifecycle independently from whether a channel is
// present in configuration. A configured adapter can fail to start; an
// unconfigured adapter is simply stopped, not unhealthy.
type State string

const (
	StateConfigured State = "configured"
	StateStarting   State = "starting"
	StateRunning    State = "running"
	StateDegraded   State = "degraded"
	StateFailed     State = "failed"
	StateStopped    State = "stopped"
)

// Descriptor is the safe, operator-facing declaration of one channel.
// Configuration values are intentionally not retained here: a manager must
// never become a second source for tokens or credentials.
type Descriptor struct {
	ID              string
	Name            string
	Configured      bool
	ReloadSupported bool
	Detail          string
}

// Status is an immutable manager snapshot for a single channel.
type Status struct {
	Descriptor
	State State
}

// Manager owns channel lifecycle facts for the dashboard and other operator
// surfaces. It is deliberately typed to channels rather than extending the
// metadata-only plugin interface.
type Manager struct {
	mu       sync.RWMutex
	channels []Status
	index    map[string]int
}

func NewManager(descriptors []Descriptor) *Manager {
	m := &Manager{index: make(map[string]int, len(descriptors))}
	for _, d := range descriptors {
		state := StateStopped
		if d.Configured {
			state = StateConfigured
		}
		m.index[d.ID] = len(m.channels)
		m.channels = append(m.channels, Status{Descriptor: d, State: state})
	}
	return m
}

func (m *Manager) Snapshot() []Status {
	if m == nil {
		return nil
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return append([]Status(nil), m.channels...)
}

func (m *Manager) MarkStarting(id string) { m.set(id, StateStarting, "") }
func (m *Manager) MarkRunning(id string)  { m.set(id, StateRunning, "") }
func (m *Manager) MarkDegraded(id, detail string) {
	m.set(id, StateDegraded, detail)
}
func (m *Manager) MarkFailed(id, detail string)  { m.set(id, StateFailed, detail) }
func (m *Manager) MarkStopped(id, detail string) { m.set(id, StateStopped, detail) }

func (m *Manager) set(id string, state State, detail string) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	i, ok := m.index[id]
	if !ok {
		return
	}
	m.channels[i].State = state
	if detail != "" {
		m.channels[i].Detail = detail
	}
}
