package daemon

import "sync"

// AgentStatus tracks the most recent version and install type an
// archie-agent worker reported about itself. It is observed passively, off
// ordinary taskrun.Response traffic in runViaAgent -- there is no dedicated
// poll, since every archie-agent process is task-scoped and ephemeral (see
// docs/architecture and internal/app/agentworker), not a long-lived process
// the daemon could query on demand the way it can Telegram's gateway.
//
// Before the first task completes, nothing is known: Snapshot's ok return
// reports that honestly rather than a caller guessing a value.
type AgentStatus struct {
	mu          sync.RWMutex
	version     string
	installType string
	observed    bool
}

// Observe records the version/install-type an archie-agent worker reported
// in a task response. Called for every completed task, so the value always
// reflects whichever agent build most recently ran -- the same one that is
// actually in service, not the version archied's own release pipeline
// stamped for it (those two can diverge, which is exactly what this exists
// to catch).
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
