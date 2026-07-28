package gateway

import (
	"strings"
	"testing"
)

func TestSessionStateGoalLifecycle(t *testing.T) {
	s := NewSessionState()

	if s.HasGoal() {
		t.Error("new state should have no goal")
	}
	if g := s.Goals(); len(g) != 0 {
		t.Errorf("Goals() = %d, want 0", len(g))
	}

	s.SetGoal("ship the thing", true)
	if !s.HasGoal() {
		t.Error("HasGoal() = false after SetGoal")
	}
	goals := s.Goals()
	if len(goals) != 1 || goals[0].Text != "ship the thing" || goals[0].Status != GoalActive {
		t.Errorf("Goals() = %+v, want active goal", goals)
	}

	s.PauseGoal()
	goals = s.Goals()
	if goals[0].Status != GoalPaused {
		t.Errorf("status = %s, want paused", goals[0].Status)
	}

	s.ResumeGoal()
	goals = s.Goals()
	if goals[0].Status != GoalActive {
		t.Errorf("status = %s, want active after resume", goals[0].Status)
	}

	s.ClearGoal()
	if s.HasGoal() {
		t.Error("HasGoal() = true after ClearGoal")
	}
}

func TestPauseResumeWithoutGoalIsNoop(t *testing.T) {
	s := NewSessionState()
	if s.PauseGoal() {
		t.Error("PauseGoal() without goal should return false")
	}
	if s.ResumeGoal() {
		t.Error("ResumeGoal() without goal should return false")
	}
}

func TestSessionStateSubgoals(t *testing.T) {
	s := NewSessionState()

	// Cannot add subgoal without a goal
	if s.AddSubgoal("do thing 1") {
		t.Error("AddSubgoal() without goal should return false")
	}

	s.SetGoal("ship", true)
	if !s.AddSubgoal("sub-a") {
		t.Error("AddSubgoal() should succeed with goal present")
	}
	s.AddSubgoal("sub-b")
	s.AddSubgoal("sub-c")

	sgs := s.Subgoals()
	if len(sgs) != 3 {
		t.Fatalf("Subgoals() = %d, want 3", len(sgs))
	}

	// Remove subgoal at position 2 (sub-b)
	if !s.RemoveSubgoal(2) {
		t.Error("RemoveSubgoal(2) should succeed")
	}
	sgs = s.Subgoals()
	if len(sgs) != 2 || sgs[0].Text != "sub-a" || sgs[1].Text != "sub-c" {
		t.Errorf("Subgoals() = %+v, want [sub-a, sub-c]", sgs)
	}

	// Out of range
	if s.RemoveSubgoal(10) {
		t.Error("RemoveSubgoal(10) should return false")
	}
	if s.RemoveSubgoal(0) {
		t.Error("RemoveSubgoal(0) should return false")
	}

	s.ClearSubgoals()
	if len(s.Subgoals()) != 0 {
		t.Error("Subgoals() not empty after ClearSubgoals")
	}
}

func TestSessionStateSetGoalPreservesSubgoalsByDefault(t *testing.T) {
	s := NewSessionState()
	s.SetGoal("goal-a", false)
	s.AddSubgoal("sg-1")
	s.AddSubgoal("sg-2")

	s.SetGoal("goal-b", false) // no clear
	sgs := s.Subgoals()
	if len(sgs) != 2 {
		t.Errorf("subgoals = %d after SetGoal without clear, want 2", len(sgs))
	}

	s.SetGoal("goal-c", true) // clear
	sgs = s.Subgoals()
	if len(sgs) != 0 {
		t.Errorf("subgoals = %d after SetGoal with clear, want 0", len(sgs))
	}
}

func TestSessionStateWait(t *testing.T) {
	s := NewSessionState()
	if s.IsWaiting() {
		t.Error("IsWaiting() = true on new state")
	}
	s.SetWait()
	if !s.IsWaiting() {
		t.Error("IsWaiting() = false after SetWait")
	}
	s.ClearWait()
	if s.IsWaiting() {
		t.Error("IsWaiting() = true after ClearWait")
	}
}

func TestSessionStateSteer(t *testing.T) {
	s := NewSessionState()

	if s.HasPendingSteer() {
		t.Error("HasPendingSteer() = true on new state")
	}
	if _, ok := s.PollSteer(); ok {
		t.Error("PollSteer() should return false on empty")
	}

	s.SetSteer("do the thing")
	if !s.HasPendingSteer() {
		t.Error("HasPendingSteer() = false after SetSteer")
	}
	if text, ok := s.PeekSteer(); !ok || text != "do the thing" {
		t.Errorf("PeekSteer() = (%q, %v), want (do the thing, true)", text, ok)
	}
	// Peek doesn't consume
	if !s.HasPendingSteer() {
		t.Error("PeekSteer should not consume")
	}

	text, ok := s.PollSteer()
	if !ok || text != "do the thing" {
		t.Errorf("PollSteer() = (%q, %v), want (do the thing, true)", text, ok)
	}
	if s.HasPendingSteer() {
		t.Error("HasPendingSteer() = true after PollSteer")
	}

	// Second set replaces
	s.SetSteer("first")
	s.SetSteer("second")
	text, _ = s.PollSteer()
	if text != "second" {
		t.Errorf("second SetSteer should replace: got %q", text)
	}
}

func TestSessionStateQueue(t *testing.T) {
	s := NewSessionState()

	if n := s.QueueLen(); n != 0 {
		t.Errorf("QueueLen() = %d, want 0", n)
	}

	s.AddToQueue("item-a")
	s.AddToQueue("item-b")
	s.AddToQueue("item-c")

	if n := s.QueueLen(); n != 3 {
		t.Errorf("QueueLen() = %d, want 3", n)
	}

	entries := s.QueueEntries()
	if len(entries) != 3 {
		t.Fatalf("QueueEntries() = %d, want 3", len(entries))
	}

	if !s.RemoveFromQueue(2) {
		t.Error("RemoveFromQueue(2) should succeed")
	}
	entries = s.QueueEntries()
	if len(entries) != 2 || entries[0].Text != "item-a" || entries[1].Text != "item-c" {
		t.Errorf("QueueEntries() = %+v, want [item-a, item-c]", entries)
	}

	s.ClearQueue()
	if n := s.QueueLen(); n != 0 {
		t.Errorf("QueueLen() = %d after clear, want 0", n)
	}
}

func TestSessionStateClearGoalResetsWait(t *testing.T) {
	s := NewSessionState()
	s.SetGoal("g", false)
	s.SetWait()

	s.ClearGoal()
	if s.IsWaiting() {
		t.Error("ClearGoal should reset wait flag")
	}
}

func TestBuildGoalPromptEmpty(t *testing.T) {
	s := NewSessionState()
	if p := s.BuildGoalPrompt(); p != "" {
		t.Errorf("BuildGoalPrompt() = %q, want empty", p)
	}
}

func TestBuildGoalPromptActive(t *testing.T) {
	s := NewSessionState()
	s.SetGoal("build the API", false)
	s.AddSubgoal("add auth")
	s.AddSubgoal("add rate limiting")

	p := s.BuildGoalPrompt()
	if p == "" {
		t.Fatal("BuildGoalPrompt() returned empty")
	}
	if !strings.Contains(p, "build the API") {
		t.Error("prompt missing goal text")
	}
	if !strings.Contains(p, "add auth") {
		t.Error("prompt missing subgoal")
	}
	if !strings.Contains(p, "add rate limiting") {
		t.Error("prompt missing subgoal")
	}
	if !strings.Contains(p, "standing_goal") {
		t.Error("prompt missing standing_goal tag")
	}
	// Active goal should not mention pause/wait
	if strings.Contains(p, "paused") {
		t.Error("active goal should not mention paused")
	}
}

func TestBuildGoalPromptPaused(t *testing.T) {
	s := NewSessionState()
	s.SetGoal("refactor", false)
	s.PauseGoal()

	p := s.BuildGoalPrompt()
	if !strings.Contains(p, `status="paused"`) {
		t.Error("prompt missing paused status attribute")
	}
	if !strings.Contains(p, "paused") {
		t.Error("prompt should mention goal is paused")
	}
}

func TestBuildGoalPromptWaiting(t *testing.T) {
	s := NewSessionState()
	s.SetGoal("write docs", false)
	s.SetWait()

	p := s.BuildGoalPrompt()
	if !strings.Contains(p, `wait="true"`) {
		t.Error("prompt missing wait attribute")
	}
	if !strings.Contains(p, "wait flag") {
		t.Error("prompt should mention wait flag")
	}
}

func TestBuildGoalPromptConcurrency(t *testing.T) {
	// Verify that concurrent access to BuildGoalPrompt and mutations
	// does not race. Run with -race.
	s := NewSessionState()
	s.SetGoal("concurrent goal", false)
	s.AddSubgoal("sg-1")
	s.AddSubgoal("sg-2")

	done := make(chan struct{})
	go func() {
		for i := range 100 {
			s.SetSteer("steer " + string(rune('a'+i%26)))
			s.SetWait()
			s.ClearWait()
			s.AddToQueue("q")
		}
		close(done)
	}()

	for range 1000 {
		p := s.BuildGoalPrompt()
		if p == "" {
			t.Error("BuildGoalPrompt returned empty during concurrent access")
			break
		}
		s.PollSteer()
		s.QueueEntries()
	}
	<-done
}
