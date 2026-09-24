package controlplane

import (
	"testing"
	"time"
)

func TestWorkflowSettingsTaskRuntime(t *testing.T) {
	for _, tc := range []struct {
		name  string
		value string
		want  time.Duration
	}{
		{"stored before the limit existed", `{"max_model_tool_steps":90,"max_runtime_seconds":3600,"max_consecutive_gate_failures":5}`, defaultTaskRuntime},
		{"explicitly disabled", `{"max_task_runtime_seconds":0}`, 0},
		{"set", `{"max_task_runtime_seconds":7200}`, 2 * time.Hour},
	} {
		t.Run(tc.name, func(t *testing.T) {
			settings, err := decodeSettings([]byte(tc.value))
			if err != nil {
				t.Fatal(err)
			}
			if settings.MaxTaskRuntime != tc.want {
				t.Fatalf("MaxTaskRuntime = %s, want %s", settings.MaxTaskRuntime, tc.want)
			}
			encoded, err := encodeSettings(settings)
			if err != nil {
				t.Fatal(err)
			}
			again, err := decodeSettings(encoded)
			if err != nil || again.MaxTaskRuntime != tc.want {
				t.Fatalf("round trip = %s, %v; want %s", again.MaxTaskRuntime, err, tc.want)
			}
		})
	}
}
