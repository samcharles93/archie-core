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
