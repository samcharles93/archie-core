package mapping_test

import (
	"testing"

	"github.com/samcharles93/archie-core/internal/domain/mapping"
)

func TestResolveExtractsValuesByPath(t *testing.T) {
	tests := []struct {
		name    string
		payload string
		fields  []mapping.Field
		want    map[string]any
	}{
		{
			name:    "top level string",
			payload: `{"action":"opened"}`,
			fields:  []mapping.Field{{Name: "action", Path: "action", Type: mapping.TypeString}},
			want:    map[string]any{"action": "opened"},
		},
		{
			name:    "nested object",
			payload: `{"issue":{"title":"bug found"}}`,
			fields:  []mapping.Field{{Name: "title", Path: "issue.title", Type: mapping.TypeString}},
			want:    map[string]any{"title": "bug found"},
		},
		{
			name:    "array index",
			payload: `{"items":[{"id":1},{"id":2}]}`,
			fields:  []mapping.Field{{Name: "second_id", Path: "items[1].id", Type: mapping.TypeNumber}},
			want:    map[string]any{"second_id": float64(2)},
		},
		{
			name:    "any type matches anything",
			payload: `{"flag":true}`,
			fields:  []mapping.Field{{Name: "flag", Path: "flag", Type: mapping.TypeAny}},
			want:    map[string]any{"flag": true},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			values, failures := mapping.Resolve(tt.fields, []byte(tt.payload))
			if len(failures) != 0 {
				t.Fatalf("unexpected failures: %+v", failures)
			}
			for k, want := range tt.want {
				got, ok := values[k]
				if !ok {
					t.Fatalf("missing resolved value for %q", k)
				}
				if got != want {
					t.Errorf("values[%q] = %#v, want %#v", k, got, want)
				}
			}
		})
	}
}

func TestResolveReportsFailuresLoudly(t *testing.T) {
	tests := []struct {
		name       string
		payload    string
		fields     []mapping.Field
		wantReason string
	}{
		{
			name:       "missing path",
			payload:    `{"action":"opened"}`,
			fields:     []mapping.Field{{Name: "title", Path: "issue.title", Type: mapping.TypeString, Required: true}},
			wantReason: mapping.ReasonMissing,
		},
		{
			name:       "null value",
			payload:    `{"issue":{"title":null}}`,
			fields:     []mapping.Field{{Name: "title", Path: "issue.title", Type: mapping.TypeString}},
			wantReason: mapping.ReasonNull,
		},
		{
			name:       "type mismatch",
			payload:    `{"issue":{"title":42}}`,
			fields:     []mapping.Field{{Name: "title", Path: "issue.title", Type: mapping.TypeString}},
			wantReason: mapping.ReasonTypeMismatch,
		},
		{
			name:       "index past array length",
			payload:    `{"items":[{"id":1}]}`,
			fields:     []mapping.Field{{Name: "second_id", Path: "items[1].id", Type: mapping.TypeNumber, Required: true}},
			wantReason: mapping.ReasonMissing,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			values, failures := mapping.Resolve(tt.fields, []byte(tt.payload))
			if len(failures) != 1 {
				t.Fatalf("got %d failures, want 1: %+v", len(failures), failures)
			}
			f := failures[0]
			if f.Reason != tt.wantReason {
				t.Errorf("Reason = %q, want %q", f.Reason, tt.wantReason)
			}
			if f.FieldName != tt.fields[0].Name {
				t.Errorf("FieldName = %q, want %q", f.FieldName, tt.fields[0].Name)
			}
			if f.Path != tt.fields[0].Path {
				t.Errorf("Path = %q, want %q", f.Path, tt.fields[0].Path)
			}
			if _, ok := values[tt.fields[0].Name]; ok {
				t.Errorf("a failed field must not appear in values, got %#v", values[tt.fields[0].Name])
			}
		})
	}
}

func TestResolveOptionalMissingIsNotAFailure(t *testing.T) {
	fields := []mapping.Field{{Name: "title", Path: "issue.title", Type: mapping.TypeString, Required: false}}
	values, failures := mapping.Resolve(fields, []byte(`{"action":"opened"}`))
	if len(failures) != 0 {
		t.Fatalf("optional missing field must not fail, got %+v", failures)
	}
	if _, ok := values["title"]; ok {
		t.Errorf("missing optional field must not appear in values")
	}
}

func TestResolveOptionalButWrongTypeStillFails(t *testing.T) {
	fields := []mapping.Field{{Name: "title", Path: "issue.title", Type: mapping.TypeString, Required: false}}
	_, failures := mapping.Resolve(fields, []byte(`{"issue":{"title":42}}`))
	if len(failures) != 1 || failures[0].Reason != mapping.ReasonTypeMismatch {
		t.Fatalf("optional field with wrong type must still fail loudly, got %+v", failures)
	}
}

func TestResolveKeepsGoingAfterOneFieldFails(t *testing.T) {
	fields := []mapping.Field{
		{Name: "missing", Path: "nope", Type: mapping.TypeString, Required: true},
		{Name: "action", Path: "action", Type: mapping.TypeString},
	}
	values, failures := mapping.Resolve(fields, []byte(`{"action":"opened"}`))
	if len(failures) != 1 {
		t.Fatalf("want exactly 1 failure, got %+v", failures)
	}
	if values["action"] != "opened" {
		t.Errorf("second field should still resolve, got values=%+v", values)
	}
}

func TestValidateRejectsMalformedMappings(t *testing.T) {
	tests := []struct {
		name string
		m    mapping.Mapping
	}{
		{"empty name", mapping.Mapping{Name: "", Fields: []mapping.Field{{Name: "a", Path: "a", Type: mapping.TypeString}}}},
		{"no fields", mapping.Mapping{Name: "m", Fields: nil}},
		{"field with no name", mapping.Mapping{Name: "m", Fields: []mapping.Field{{Path: "a", Type: mapping.TypeString}}}},
		{"field with no path", mapping.Mapping{Name: "m", Fields: []mapping.Field{{Name: "a", Type: mapping.TypeString}}}},
		{"field with unknown type", mapping.Mapping{Name: "m", Fields: []mapping.Field{{Name: "a", Path: "a", Type: "wat"}}}},
		{"duplicate field names", mapping.Mapping{Name: "m", Fields: []mapping.Field{
			{Name: "a", Path: "x", Type: mapping.TypeString},
			{Name: "a", Path: "y", Type: mapping.TypeString},
		}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.m.Validate(); err == nil {
				t.Fatalf("Validate() = nil, want an error for %+v", tt.m)
			}
		})
	}
}

func TestValidateAcceptsWellFormedMapping(t *testing.T) {
	m := mapping.Mapping{
		Name:   "sentry issue opened",
		Fields: []mapping.Field{{Name: "title", Path: "issue.title", Type: mapping.TypeString, Required: true}},
	}
	if err := m.Validate(); err != nil {
		t.Fatalf("Validate() = %v, want nil", err)
	}
}

func TestResolveTreatsMalformedArrayIndexAsMissingNotReinterpreted(t *testing.T) {
	// "items[abc].id" must never silently resolve as if it were "items.id" --
	// a malformed path is a mistake the operator should see, not a different
	// path succeeding by accident.
	fields := []mapping.Field{{Name: "id", Path: "items[abc].id", Type: mapping.TypeNumber, Required: true}}
	values, failures := mapping.Resolve(fields, []byte(`{"items":{"id":5}}`))
	if len(failures) != 1 || failures[0].Reason != mapping.ReasonMissing {
		t.Fatalf("failures = %+v, want exactly one ReasonMissing", failures)
	}
	if _, ok := values["id"]; ok {
		t.Fatalf("a malformed path must never resolve a value, got %#v", values["id"])
	}
}

func TestResolveNumberPreservesIntVsFloatShapeButTypeCheckIsNumeric(t *testing.T) {
	fields := []mapping.Field{
		{Name: "count", Path: "count", Type: mapping.TypeNumber},
	}
	values, failures := mapping.Resolve(fields, []byte(`{"count":42}`))
	if len(failures) != 0 {
		t.Fatalf("unexpected failures: %+v", failures)
	}
	if values["count"] != float64(42) {
		t.Errorf("count = %#v, want float64(42)", values["count"])
	}
}
