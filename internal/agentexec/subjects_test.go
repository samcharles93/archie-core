package agentexec

import "testing"

// TestTaskIDFromSystemSubject pins the round trip with SubjectForSystem, and
// that a subject which merely resembles one (wrong kind suffix, malformed
// ID) is rejected rather than parsed into a wrong task ID -- a daemon
// demuxing a wildcard subscription onto SubjectSystemWildcard has no other
// check on what actually arrives.
func TestTaskIDFromSystemSubject(t *testing.T) {
	tests := []struct {
		name    string
		subject string
		wantID  int64
		wantOK  bool
	}{
		{"round trip with SubjectForSystem", SubjectForSystem(42), 42, true},
		{"round trip with a large ID", SubjectForSystem(9_223_372_036), 9_223_372_036, true},
		{"wrong kind suffix is rejected", "archie.agent.42.response", 0, false},
		{"missing prefix is rejected", "some.other.42.system", 0, false},
		{"non-numeric ID is rejected", "archie.agent.abc.system", 0, false},
		{"zero ID is rejected", "archie.agent.0.system", 0, false},
		{"negative ID is rejected", "archie.agent.-1.system", 0, false},
		{"empty subject is rejected", "", 0, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotID, gotOK := TaskIDFromSystemSubject(tt.subject)
			if gotID != tt.wantID || gotOK != tt.wantOK {
				t.Errorf("TaskIDFromSystemSubject(%q) = (%d, %v), want (%d, %v)",
					tt.subject, gotID, gotOK, tt.wantID, tt.wantOK)
			}
		})
	}
}

func TestSubjectSystemWildcardMatchesEverySystemSubject(t *testing.T) {
	tests := []int64{1, 42, 9_223_372_036}
	for _, taskID := range tests {
		if id, ok := TaskIDFromSystemSubject(SubjectForSystem(taskID)); !ok || id != taskID {
			t.Errorf("SubjectForSystem(%d) did not round-trip through TaskIDFromSystemSubject: got (%d, %v)", taskID, id, ok)
		}
	}
	if SubjectSystemWildcard != "archie.agent.*.system" {
		t.Errorf("SubjectSystemWildcard = %q, want the single-token wildcard NATS matches against subjectForTask's shape",
			SubjectSystemWildcard)
	}
}

// TestTaskIDFromEventsSubject mirrors TestTaskIDFromSystemSubject: a daemon
// demuxing SubjectEventsWildcard has no other check on what arrives, so a
// subject that merely resembles one must be rejected, not parsed into a
// wrong task ID.
func TestTaskIDFromEventsSubject(t *testing.T) {
	tests := []struct {
		name    string
		subject string
		wantID  int64
		wantOK  bool
	}{
		{"round trip with SubjectForEvents", SubjectForEvents(42), 42, true},
		{"round trip with a large ID", SubjectForEvents(9_223_372_036), 9_223_372_036, true},
		{"wrong kind suffix is rejected", "archie.agent.42.system", 0, false},
		{"missing prefix is rejected", "some.other.42.events", 0, false},
		{"non-numeric ID is rejected", "archie.agent.abc.events", 0, false},
		{"zero ID is rejected", "archie.agent.0.events", 0, false},
		{"negative ID is rejected", "archie.agent.-1.events", 0, false},
		{"empty subject is rejected", "", 0, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotID, gotOK := TaskIDFromEventsSubject(tt.subject)
			if gotID != tt.wantID || gotOK != tt.wantOK {
				t.Errorf("TaskIDFromEventsSubject(%q) = (%d, %v), want (%d, %v)",
					tt.subject, gotID, gotOK, tt.wantID, tt.wantOK)
			}
		})
	}
}

func TestSubjectEventsWildcardMatchesEveryEventsSubject(t *testing.T) {
	tests := []int64{1, 42, 9_223_372_036}
	for _, taskID := range tests {
		if id, ok := TaskIDFromEventsSubject(SubjectForEvents(taskID)); !ok || id != taskID {
			t.Errorf("SubjectForEvents(%d) did not round-trip through TaskIDFromEventsSubject: got (%d, %v)", taskID, id, ok)
		}
	}
	if SubjectEventsWildcard != "archie.agent.*.events" {
		t.Errorf("SubjectEventsWildcard = %q, want the single-token wildcard NATS matches against subjectForTask's shape",
			SubjectEventsWildcard)
	}
}

// TestEventsAndSystemSubjectsDoNotCrossMatch guards the reason a dedicated
// events subject exists instead of reusing SubjectForSystem: the daemon
// already owns one payload format per subject (logging.Entry on .system),
// and a task event decoded as a log entry (or vice versa) would silently
// corrupt whichever table receives it.
func TestEventsAndSystemSubjectsDoNotCrossMatch(t *testing.T) {
	if _, ok := TaskIDFromSystemSubject(SubjectForEvents(7)); ok {
		t.Error("TaskIDFromSystemSubject accepted an events subject")
	}
	if _, ok := TaskIDFromEventsSubject(SubjectForSystem(7)); ok {
		t.Error("TaskIDFromEventsSubject accepted a system subject")
	}
}
