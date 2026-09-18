package agentexec

import (
	"encoding/json"
	"strings"
	"testing"
)

// A capture tool sometimes needs a field only under a condition the same call
// carries: triage must name a workflow when it says a code change is needed,
// and must not be made to invent one when it says the opposite. A flat
// RequiredFields entry cannot say that, so the choice used to be between a
// meaningless forced answer and a silent default.
func TestValidateCaptureArgsConditionalRequirement(t *testing.T) {
	spec := CaptureTool{
		Name:           "decide",
		RequiredFields: []string{"needs_code_change"},
		RequiredWhenTrue: map[string][]string{
			"needs_code_change": {"workflow"},
		},
	}

	tests := []struct {
		name         string
		args         string
		wantOK       bool
		wantRejected string
	}{
		{
			name:   "trigger true and field present",
			args:   `{"needs_code_change": true, "workflow": "feasibility"}`,
			wantOK: true,
		},
		{
			name:         "trigger true and field missing",
			args:         `{"needs_code_change": true}`,
			wantRejected: "workflow",
		},
		{
			name:         "trigger true and field empty",
			args:         `{"needs_code_change": true, "workflow": "  "}`,
			wantRejected: "workflow",
		},
		{
			name:   "trigger false, field not required",
			args:   `{"needs_code_change": false}`,
			wantOK: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rejection, ok := validateCaptureArgs(spec, json.RawMessage(tt.args))
			if ok != tt.wantOK {
				t.Fatalf("validateCaptureArgs ok = %v, want %v (rejection %q)", ok, tt.wantOK, rejection)
			}
			if tt.wantRejected != "" && !strings.Contains(rejection, tt.wantRejected) {
				t.Fatalf("rejection %q does not name %q", rejection, tt.wantRejected)
			}
		})
	}
}
