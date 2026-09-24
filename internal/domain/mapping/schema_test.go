package mapping_test

import (
	"errors"
	"testing"

	"github.com/samcharles93/archie-core/internal/domain/eventtype"
	"github.com/samcharles93/archie-core/internal/domain/mapping"
)

func TestCheckSchema(t *testing.T) {
	schema := map[string]eventtype.ValueType{
		"action":         eventtype.TypeString,
		"issue":          eventtype.TypeObject,
		"issue.number":   eventtype.TypeNumber,
		"labels":         eventtype.TypeArray,
		"labels[]":       eventtype.TypeObject,
		"labels[].name":  eventtype.TypeString,
		"closed_at":      eventtype.TypeNull,
		"repository.url": eventtype.TypeString,
	}
	tests := []struct {
		name   string
		fields []mapping.Field
		ok     bool
	}{
		{"top level path with matching type", []mapping.Field{{Name: "a", Path: "action", Type: mapping.TypeString}}, true},
		{"nested number", []mapping.Field{{Name: "n", Path: "issue.number", Type: mapping.TypeNumber}}, true},
		{"indexed array element maps to the [] path", []mapping.Field{{Name: "l", Path: "labels[0].name", Type: mapping.TypeString}}, true},
		{"any accepts every schema type", []mapping.Field{{Name: "i", Path: "issue", Type: mapping.TypeAny}}, true},
		{"a null example value accepts any declared type", []mapping.Field{{Name: "c", Path: "closed_at", Type: mapping.TypeString}}, true},
		{"path the type does not have", []mapping.Field{{Name: "x", Path: "sender.login", Type: mapping.TypeString}}, false},
		{"type differs from the schema", []mapping.Field{{Name: "n", Path: "issue.number", Type: mapping.TypeString}}, false},
		{"malformed index", []mapping.Field{{Name: "l", Path: "labels[x].name", Type: mapping.TypeString}}, false},
		{"one bad field refuses the mapping", []mapping.Field{
			{Name: "a", Path: "action", Type: mapping.TypeString},
			{Name: "b", Path: "nope", Type: mapping.TypeString},
		}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := mapping.CheckSchema(tt.fields, schema)
			if tt.ok && err != nil {
				t.Fatalf("CheckSchema() = %v, want nil", err)
			}
			if !tt.ok && !errors.Is(err, mapping.ErrSchema) {
				t.Fatalf("CheckSchema() = %v, want ErrSchema", err)
			}
		})
	}
}
