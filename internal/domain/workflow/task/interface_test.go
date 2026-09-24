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
