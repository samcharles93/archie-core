package task

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestParseWorkflowInterface(t *testing.T) {
	tests := []struct {
		name     string
		src      string
		wantErr  string
		wantMode RepositoryMode
	}{
		{name: "undeclared repository defaults to required", src: "id: a\nsteps: []\n", wantMode: RepositoryRequired},
		{name: "none", src: "id: a\nrepository: none\n", wantMode: RepositoryNone},
		{name: "unknown mode", src: "id: a\nrepository: sometimes\n", wantErr: "must be none, optional or required"},
		{name: "unknown input type", src: "id: a\ninputs:\n  ip: {type: ipv4}\n", wantErr: `type "ipv4"`},
		{name: "malformed input name", src: "id: a\ninputs:\n  src-ip: {type: string}\n", wantErr: `input "src-ip"`},
		{name: "negative gate_retries", src: "id: a\nneeds: {gate_retries: -1}\n", wantErr: "gate_retries must not be negative"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w, err := ParseWorkflowInterface(tt.src)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want containing %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got := w.RepositoryMode(); got != tt.wantMode {
				t.Errorf("RepositoryMode() = %q, want %q", got, tt.wantMode)
			}
		})
	}
}

// TestParseWorkflowInterfaceNeeds pins the declared-needs vocabulary
// (docs/prds/external-agent-harness.md, "Contract"): a stage's requirements
// of its harness are static, readable metadata, not derived at run time.
func TestParseWorkflowInterfaceNeeds(t *testing.T) {
	w, err := ParseWorkflowInterface("id: a\nprofile: contain\nneeds: {captures: true, gate_retries: 3}\n")
	if err != nil {
		t.Fatal(err)
	}
	if needs := w.Needs(); !needs.Captures || needs.GateRetries != 3 {
		t.Fatalf("Needs() = %+v, want {Captures:true GateRetries:3}", needs)
	}
}

// TestParseWorkflowInterfaceOutputsForceCaptures: an output is written
// through a capture tool, so declaring one forces the captures need whether
// or not the author also wrote needs.captures
// (docs/prds/workflow-call-outputs.md, "How a run writes one").
func TestParseWorkflowInterfaceOutputsForceCaptures(t *testing.T) {
	w, err := ParseWorkflowInterface("id: a\noutputs:\n  summary: {type: string}\n")
	if err != nil {
		t.Fatal(err)
	}
	if needs := w.Needs(); !needs.Captures {
		t.Fatalf("Needs() = %+v, want Captures for a workflow that declares an output", needs)
	}
}

func TestCheckInputs(t *testing.T) {
	w := WorkflowInterface{Inputs: map[string]InputSpec{
		"src_ip":   {Type: "string", Required: true},
		"severity": {Type: "number"},
		"extra":    {Type: "any"},
	}}
	tests := []struct {
		name    string
		values  map[string]any
		wantErr string
	}{
		{name: "required and typed", values: map[string]any{"src_ip": "10.0.0.1", "severity": json.Number("3")}},
		{name: "optional null is absent", values: map[string]any{"src_ip": "10.0.0.1", "severity": nil}},
		{name: "any takes an object", values: map[string]any{"src_ip": "x", "extra": map[string]any{"a": 1.0}}},
		{name: "missing required", values: map[string]any{"severity": 1.0}, wantErr: `"src_ip" is required`},
		{name: "null required", values: map[string]any{"src_ip": nil}, wantErr: `"src_ip" is required`},
		{name: "undeclared", values: map[string]any{"src_ip": "x", "port": 22.0}, wantErr: `"port" is not declared`},
		{name: "wrong type", values: map[string]any{"src_ip": "x", "severity": "high"}, wantErr: `"severity" is string, want number`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := w.CheckInputs(tt.values)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("CheckInputs() = %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("CheckInputs() = %v, want containing %q", err, tt.wantErr)
			}
		})
	}
}

// TestParseWorkflowInterfaceOutputs pins the declared-output grammar beside
// the declared-input grammar (docs/prds/workflow-call-outputs.md, "Where
// outputs are declared"): the same identifier shape, the same type
// vocabulary, the same Validate both ParseDefinition and
// ParseWorkflowInterface call.
func TestParseWorkflowInterfaceOutputs(t *testing.T) {
	tests := []struct {
		name    string
		src     string
		wantErr string
	}{
		{
			name: "outputs parse beside inputs",
			src:  "id: a\ninputs:\n  src_ip: {type: string}\noutputs:\n  contained: {type: bool, required: true}\n  summary: {type: string}\n",
		},
		{name: "unknown output type", src: "id: a\noutputs:\n  contained: {type: boolean}\n", wantErr: `output "contained" type "boolean" must be one of`},
		{name: "malformed output name", src: "id: a\noutputs:\n  con-tained: {type: bool}\n", wantErr: `output "con-tained" must be a letter or underscore`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w, err := ParseWorkflowInterface(tt.src)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want containing %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			contained := w.Outputs["contained"]
			if contained.Type != "bool" || !contained.Required {
				t.Errorf("outputs.contained = %+v, want {bool required}", contained)
			}
			// required defaults to false.
			if summary := w.Outputs["summary"]; summary.Type != "string" || summary.Required {
				t.Errorf("outputs.summary = %+v, want {string not required}", summary)
			}
		})
	}
}

// TestCheckOutputs pins the run-time and finish-time refusal the engine
// applies to a written output set: required, undeclared, wrong type -- with
// a written null counting as not written, exactly as CheckInputs judges a
// null input (docs/prds/workflow-call-outputs.md, "Failure rules").
func TestCheckOutputs(t *testing.T) {
	w := WorkflowInterface{Outputs: map[string]OutputSpec{
		"contained": {Type: "bool", Required: true},
		"summary":   {Type: "string"},
		"extra":     {Type: "any"},
	}}
	tests := []struct {
		name    string
		values  map[string]any
		wantErr string
	}{
		{name: "required and typed", values: map[string]any{"contained": true, "summary": "done"}},
		{name: "optional null is absent", values: map[string]any{"contained": true, "summary": nil}},
		{name: "any takes an object", values: map[string]any{"contained": true, "extra": map[string]any{"a": 1.0}}},
		{name: "missing required", values: map[string]any{"summary": "done"}, wantErr: `"contained" is required`},
		{name: "null required", values: map[string]any{"contained": nil}, wantErr: `"contained" is required`},
		{name: "undeclared", values: map[string]any{"contained": true, "port": 22}, wantErr: `"port" is not declared`},
		{name: "wrong type", values: map[string]any{"contained": true, "summary": json.Number("3")}, wantErr: `"summary" is number, want string`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := w.CheckOutputs(tt.values)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("CheckOutputs() = %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("CheckOutputs() = %v, want containing %q", err, tt.wantErr)
			}
		})
	}
}

// TestEncodeDecodeOutputs pins outputs' stored form: the shape EncodeInputs
// already has, with numbers exact across the round trip and empty for none.
func TestEncodeDecodeOutputs(t *testing.T) {
	if s, err := EncodeOutputs(nil); err != nil || s != "" {
		t.Fatalf("EncodeOutputs(nil) = (%q, %v), want (\"\", nil)", s, err)
	}
	encoded, err := EncodeOutputs(map[string]any{"contained": true, "n": json.Number("3")})
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeOutputs(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if decoded["contained"] != true {
		t.Errorf("contained = %v, want true", decoded["contained"])
	}
	if _, ok := decoded["n"].(json.Number); !ok {
		t.Errorf("n = %T, want json.Number", decoded["n"])
	}
	if outputs, err := DecodeOutputs(""); err != nil || outputs != nil {
		t.Fatalf("DecodeOutputs(\"\") = (%+v, %v), want (nil, nil)", outputs, err)
	}
	if _, err := DecodeOutputs("{"); err == nil {
		t.Fatal("DecodeOutputs({) = nil, want an error")
	}
}
