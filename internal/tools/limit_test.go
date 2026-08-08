package tools

import "testing"

func TestListLimit(t *testing.T) {
	tests := []struct {
		name  string
		input map[string]any
		def   int
		max   int
		want  int
	}{
		{name: "absent", input: map[string]any{}, def: 20, max: 100, want: 20},
		{name: "zero", input: map[string]any{"limit": float64(0)}, def: 20, max: 100, want: 20},
		{name: "negative", input: map[string]any{"limit": float64(-5)}, def: 20, max: 100, want: 20},
		{name: "float64 within range", input: map[string]any{"limit": float64(42)}, def: 20, max: 100, want: 42},
		{name: "int within range", input: map[string]any{"limit": 7}, def: 20, max: 100, want: 7},
		{name: "clamped to max", input: map[string]any{"limit": float64(200)}, def: 20, max: 100, want: 100},
		{name: "exactly max", input: map[string]any{"limit": float64(100)}, def: 20, max: 100, want: 100},
		{name: "exactly def", input: map[string]any{"limit": float64(20)}, def: 20, max: 100, want: 20},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ListLimit(tt.input, tt.def, tt.max); got != tt.want {
				t.Errorf("ListLimit() = %d, want %d", got, tt.want)
			}
		})
	}
}
