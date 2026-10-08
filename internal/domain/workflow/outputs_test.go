package workflow

import (
	"encoding/json"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"

	"github.com/samcharles93/archie-core/internal/domain/workflow/task"
)

// TestOutputCaptureToolSchemaAllowsNull pins the advertised schema of a
// declared output against the contract the run applies: a typed value, or a
// null that means "not written" (decodeCapturedOutput). Enforcing the schema
// must not reject the null the contract gives a meaning to.
func TestOutputCaptureToolSchemaAllowsNull(t *testing.T) {
	tool := outputCaptureTool("summary", task.OutputSpec{Type: "string"})
	var schema jsonschema.Schema
	if err := json.Unmarshal(tool.Parameters, &schema); err != nil {
		t.Fatalf("tool schema: %v", err)
	}
	resolved, err := schema.Resolve(nil)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	tests := []struct {
		name    string
		value   any
		wantErr bool
	}{
		{name: "a string value", value: "text"},
		{name: "null means not written", value: nil},
		{name: "a number is not a string", value: 1, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := resolved.Validate(map[string]any{"value": tt.value})
			if (err != nil) != tt.wantErr {
				t.Fatalf("Validate(%#v) err = %v, want error %v", tt.value, err, tt.wantErr)
			}
		})
	}
}
