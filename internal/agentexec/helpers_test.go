package agentexec

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/samcharles93/archie-core/internal/domain/agentrun"
)

// resultSchema is the shape of a shipped planner result (triage's), with a
// nested object and an array so every constraint kind the agent can violate
// is exercised.
const resultSchema = `{
  "type": "object",
  "properties": {
    "needs_code_change": {"type": "boolean"},
    "workflow": {"type": "string", "enum": ["implement", "tdd", "feasibility"]},
    "reasons": {"type": "string"},
    "labels": {"type": "array", "items": {"type": "string"}, "maxItems": 2},
    "plan": {"type": "object", "properties": {"risk": {"type": "integer"}}, "required": ["risk"]}
  },
  "required": ["needs_code_change", "workflow", "reasons"]
}`

// TestValidateCaptureArgsEnforcesSchema pins that the advertised schema is
// enforced, not merely offered to the agent: an enum, a numeric type, an
// array's item type and length, and a nested object's shape all reject with a
// message naming what was wrong, so the agent can correct it.
func TestValidateCaptureArgsEnforcesSchema(t *testing.T) {
	base := `"needs_code_change": true, "workflow": "tdd", "reasons": "why"`
	tests := []struct {
		name     string
		args     string
		wantOK   bool
		wantText string
	}{
		{name: "valid", args: `{` + base + `}`, wantOK: true},
		{name: "enum violation", args: `{` + base + `, "workflow": "banana"}`, wantText: "workflow"},
		{name: "wrong boolean type", args: `{"needs_code_change": "yes", "workflow": "tdd", "reasons": "why"}`, wantText: "needs_code_change"},
		{name: "string where integer expected", args: `{` + base + `, "plan": {"risk": "high"}}`, wantText: "plan"},
		{name: "array item type", args: `{` + base + `, "labels": [1, 2]}`, wantText: "labels"},
		{name: "array too long", args: `{` + base + `, "labels": ["a", "b", "c"]}`, wantText: "labels"},
		{name: "nested required missing", args: `{` + base + `, "plan": {}}`, wantText: "plan"},
	}
	spec := agentrun.CaptureTool{Name: "result", Parameters: json.RawMessage(resultSchema)}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rejection, ok := ValidateCaptureArgs(spec, json.RawMessage(tt.args))
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, rejection = %q; want ok %v", ok, rejection, tt.wantOK)
			}
			if ok {
				return
			}
			if !strings.HasPrefix(rejection, "result rejected: ") {
				t.Fatalf("rejection %q does not name the tool", rejection)
			}
			if !strings.Contains(rejection, tt.wantText) {
				t.Fatalf("rejection %q does not mention %q", rejection, tt.wantText)
			}
		})
	}
}

// TestValidateCaptureArgsWithoutSchema pins that a tool with no schema, or one
// whose schema cannot resolve, still accepts a call: the derived constraint
// lists remain the whole check, so the agent is never blocked by a tool
// definition the schema check cannot read.
func TestValidateCaptureArgsWithoutSchema(t *testing.T) {
	tests := []struct {
		name   string
		spec   agentrun.CaptureTool
		args   string
		wantOK bool
	}{
		{name: "no schema", spec: agentrun.CaptureTool{Name: "note"}, args: `{"anything": 1}`, wantOK: true},
		{
			name:   "unresolvable schema is not the agent's problem",
			spec:   agentrun.CaptureTool{Name: "note", Parameters: json.RawMessage(`{"$ref": "#/definitions/missing"}`)},
			args:   `{"anything": 1}`,
			wantOK: true,
		},
		{
			name: "derived checks still apply without a schema",
			spec: agentrun.CaptureTool{Name: "note", NonEmptyStrings: []string{"body"}},
			args: `{"body": "  "}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, ok := ValidateCaptureArgs(tt.spec, json.RawMessage(tt.args))
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tt.wantOK)
			}
		})
	}
}
