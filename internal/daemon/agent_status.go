package daemon

import "sync"

// AgentStatus holds the version and install type the last archie-agent task
// response reported. Unknown until the first task completes.
type AgentStatus struct {
	mu          sync.RWMutex
	version     string
	installType string
	observed    bool
}

// Observe records an agent's reported version and install type.
func (s *AgentStatus) Observe(version, installType string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.version = version
	s.installType = installType
	s.observed = true
}

// Snapshot returns the most recently observed version/install-type, and
// whether any task has reported one yet.
func (s *AgentStatus) Snapshot() (version, installType string, ok bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.version, s.installType, s.observed
}
