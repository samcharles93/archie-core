package binding

import (
	"testing"

	"github.com/samcharles93/archie-core/internal/domain/mapping"
)

var filterFields = []mapping.Field{
	{Name: "severity", Path: "severity", Type: mapping.TypeString},
	{Name: "count", Path: "count", Type: mapping.TypeNumber},
	{Name: "urgent", Path: "urgent", Type: mapping.TypeBool},
	{Name: "extra", Path: "extra", Type: mapping.TypeAny},
}

func TestCompileFilterRefuses(t *testing.T) {
	tests := []struct{ name, src string }{
		{"unknown parameter", `sender == "x"`},
		{"not boolean", `severity`},
		{"type mismatch", `count == "3"`},
		{"syntax", `severity in [`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := CompileFilter(tt.src, filterFields); err == nil {
				t.Fatalf("CompileFilter(%q) = nil error, want refusal", tt.src)
			}
		})
	}
}

func TestFilterAdmits(t *testing.T) {
	values := map[string]any{"severity": "high", "count": float64(3), "urgent": true, "extra": "x"}
	tests := []struct {
		name   string
		src    string
		values map[string]any
		want   bool
	}{
		{"empty filter admits everything", "", values, true},
		{"list membership admits", `severity in ["high", "critical"]`, values, true},
		{"list membership excludes", `severity in ["critical"]`, values, false},
		{"number comparison", `count > 2.0 && urgent`, values, true},
		{"dyn parameter", `extra == "x"`, values, true},
		{"unresolved parameter excludes", `severity == "high"`, map[string]any{"count": float64(1)}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f, err := CompileFilter(tt.src, filterFields)
			if err != nil {
				t.Fatalf("CompileFilter: %v", err)
			}
			got, _ := f.Admits(tt.values)
			if got != tt.want {
				t.Fatalf("Admits = %v, want %v", got, tt.want)
			}
		})
	}
}
