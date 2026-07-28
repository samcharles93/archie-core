package gateway

import (
	"fmt"
	"strings"
	"sync"
)

// GoalStatus is the operational state of a goal.
type GoalStatus string

const (
	GoalActive GoalStatus = "active"
	GoalPaused GoalStatus = "paused"
)

// Goal is a standing objective for a session.
type Goal struct {
	Text   string     `json:"text"`
	Status GoalStatus `json:"status"`
}

// Subgoal is a child objective under a goal.
type Subgoal struct {
	Text string `json:"text"`
}

// QueueEntry is a single item in the session's background queue.
type QueueEntry struct {
	Text string `json:"text"`
}

// SessionState holds per-session control state for goal, steer, and
// queue commands. All access is guarded by the embedded mutex.
type SessionState struct {
	mu sync.Mutex

	// Goal management
	goals    []Goal
	subgoals []Subgoal
	waiting  bool // /goal wait flag

	// Steer injection
	steerText string

	// Background queue
	queue []QueueEntry
}

// NewSessionState returns an empty, usable SessionState.
func NewSessionState() *SessionState {
	return &SessionState{}
}

// ── Goal operations ──────────────────────────────────────────────────

// SetGoal replaces the current goal. If there is an existing goal with
// subgoals, subgoals are preserved unless clearSubgoals is true.
func (s *SessionState) SetGoal(text string, clearSubgoals bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.goals) == 0 {
		s.goals = []Goal{{Text: text, Status: GoalActive}}
		return
	}
	s.goals[0] = Goal{Text: text, Status: GoalActive}
	if clearSubgoals {
		s.subgoals = nil
	}
}

// Goals returns a snapshot of current goals.
func (s *SessionState) Goals() []Goal {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Goal, len(s.goals))
	copy(out, s.goals)
	return out
}

// PauseGoal marks the first goal as paused.
func (s *SessionState) PauseGoal() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.goals) == 0 {
		return false
	}
	s.goals[0].Status = GoalPaused
	return true
}

// ResumeGoal marks the first goal as active.
func (s *SessionState) ResumeGoal() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.goals) == 0 {
		return false
	}
	s.goals[0].Status = GoalActive
	return true
}

// ClearGoal removes all goals and subgoals.
func (s *SessionState) ClearGoal() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.goals = nil
	s.subgoals = nil
	s.waiting = false
}

// HasGoal reports whether a goal is set.
func (s *SessionState) HasGoal() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.goals) > 0
}

// ── Subgoal operations ───────────────────────────────────────────────

// AddSubgoal appends a subgoal.
func (s *SessionState) AddSubgoal(text string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.goals) == 0 {
		return false
	}
	s.subgoals = append(s.subgoals, Subgoal{Text: text})
	return true
}

// Subgoals returns a snapshot of current subgoals.
func (s *SessionState) Subgoals() []Subgoal {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Subgoal, len(s.subgoals))
	copy(out, s.subgoals)
	return out
}

// RemoveSubgoal removes the subgoal at the given 1-indexed position.
// Returns false if the index is out of range.
func (s *SessionState) RemoveSubgoal(n int) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	idx := n - 1
	if idx < 0 || idx >= len(s.subgoals) {
		return false
	}
	s.subgoals = append(s.subgoals[:idx], s.subgoals[idx+1:]...)
	return true
}

// ClearSubgoals removes all subgoals.
func (s *SessionState) ClearSubgoals() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.subgoals = nil
}

// ── Wait operations ──────────────────────────────────────────────────

// SetWait sets the wait flag.
func (s *SessionState) SetWait() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.waiting = true
}

// ClearWait clears the wait flag.
func (s *SessionState) ClearWait() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.waiting = false
}

// IsWaiting reports the wait flag.
func (s *SessionState) IsWaiting() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.waiting
}

// ── Steer operations ─────────────────────────────────────────────────

// SetSteer stores a pending steer message. The previous steer (if not yet
// consumed) is replaced.
func (s *SessionState) SetSteer(text string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.steerText = text
}

// PollSteer returns and clears any pending steer message. The second
// return value is false when no steer was pending.
func (s *SessionState) PollSteer() (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.steerText == "" {
		return "", false
	}
	text := s.steerText
	s.steerText = ""
	return text, true
}

// PeekSteer returns the pending steer message without consuming it.
func (s *SessionState) PeekSteer() (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.steerText == "" {
		return "", false
	}
	return s.steerText, true
}

// HasPendingSteer reports whether a steer message is waiting.
func (s *SessionState) HasPendingSteer() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.steerText != ""
}

// ── Queue operations ─────────────────────────────────────────────────

// AddToQueue appends an entry to the queue.
func (s *SessionState) AddToQueue(text string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.queue = append(s.queue, QueueEntry{Text: text})
}

// QueueEntries returns a snapshot of the queue.
func (s *SessionState) QueueEntries() []QueueEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]QueueEntry, len(s.queue))
	copy(out, s.queue)
	return out
}

// RemoveFromQueue removes the entry at the given 1-indexed position.
// Returns false if the index is out of range.
func (s *SessionState) RemoveFromQueue(n int) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	idx := n - 1
	if idx < 0 || idx >= len(s.queue) {
		return false
	}
	s.queue = append(s.queue[:idx], s.queue[idx+1:]...)
	return true
}

// ClearQueue removes all queue entries.
func (s *SessionState) ClearQueue() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.queue = nil
}

// QueueLen returns the number of queue entries.
func (s *SessionState) QueueLen() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.queue)
}

// ── Prompt rendering ─────────────────────────────────────────────────

// BuildGoalPrompt returns a string suitable for injecting into the system
// prompt. Returns empty string when no goal is set.
func (s *SessionState) BuildGoalPrompt() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.goals) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("<standing_goal")
	if s.goals[0].Status == GoalPaused {
		b.WriteString(` status="paused"`)
	}
	if s.waiting {
		b.WriteString(` wait="true"`)
	}
	b.WriteString(">\n")
	b.WriteString(s.goals[0].Text)
	b.WriteString("\n")

	if len(s.subgoals) > 0 {
		b.WriteString("\nSubgoals:\n")
		for i, sg := range s.subgoals {
			fmt.Fprintf(&b, "%d. %s\n", i+1, sg.Text)
		}
	}
	b.WriteString("</standing_goal>")

	if s.waiting && s.goals[0].Status == GoalActive {
		b.WriteString("\n\nThe goal has a wait flag set. Do not begin new work toward the goal unless the user explicitly asks you to proceed. Continue responding to other requests normally.")
	}
	if s.goals[0].Status == GoalPaused {
		b.WriteString("\n\nThe goal is paused. Do not work toward it unless the user explicitly resumes it.")
	}

	return b.String()
}
