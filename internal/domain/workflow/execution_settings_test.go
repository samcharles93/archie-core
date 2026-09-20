package workflow

import (
	"testing"
	"time"
)

func TestExecutionSettingsValidateRejectsNegativeLimits(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		settings ExecutionSettings
	}{
		{"steps", ExecutionSettings{MaxModelToolSteps: -1, MaxRuntime: time.Second, MaxConsecutiveGateFailures: 1}},
		{"runtime", ExecutionSettings{MaxModelToolSteps: 1, MaxRuntime: -time.Second, MaxConsecutiveGateFailures: 1}},
		{"gate failures", ExecutionSettings{MaxModelToolSteps: 1, MaxRuntime: time.Second, MaxConsecutiveGateFailures: -1}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if err := tt.settings.Validate(); err == nil {
				t.Fatal("Validate() = nil, want error")
			}
		})
	}
}
