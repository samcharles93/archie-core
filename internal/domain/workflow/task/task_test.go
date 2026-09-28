package task

import (
	"encoding/json"
	"testing"
)

func TestEffectivePRNumber(t *testing.T) {
	for _, test := range []struct {
		name string
		task Task
		want int
	}{
		{"PRNumber set directly wins", Task{PRNumber: 7, Inputs: map[string]any{"pr_number": json.Number("99")}}, 7},
		{"json.Number from a store round trip", Task{Inputs: map[string]any{"pr_number": json.Number("42")}}, 42},
		{"float64 from a hand-built map", Task{Inputs: map[string]any{"pr_number": float64(13)}}, 13},
		{"int from a hand-built map", Task{Inputs: map[string]any{"pr_number": 5}}, 5},
		{"missing input", Task{Inputs: map[string]any{}}, 0},
		{"nil Inputs", Task{}, 0},
		{"wrong type", Task{Inputs: map[string]any{"pr_number": "not a number"}}, 0},
		{"malformed json.Number", Task{Inputs: map[string]any{"pr_number": json.Number("not-a-number")}}, 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := test.task.EffectivePRNumber(); got != test.want {
				t.Errorf("EffectivePRNumber() = %d, want %d", got, test.want)
			}
		})
	}
}
