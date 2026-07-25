package tools

import (
	"errors"
	"testing"
)

func TestDefaultGuardrailConfig(t *testing.T) {
	c := DefaultGuardrailConfig()

	if c.ExactFailureWarnAfter != 3 {
		t.Errorf("ExactFailureWarnAfter = %d, want 3", c.ExactFailureWarnAfter)
	}
	if c.SameToolFailureWarnAfter != 5 {
		t.Errorf("SameToolFailureWarnAfter = %d, want 5", c.SameToolFailureWarnAfter)
	}
	if c.NoProgressWarnAfter != 10 {
		t.Errorf("NoProgressWarnAfter = %d, want 10", c.NoProgressWarnAfter)
	}
	if c.HardStopAfterWarnRepeat != 2 {
		t.Errorf("HardStopAfterWarnRepeat = %d, want 2", c.HardStopAfterWarnRepeat)
	}
}

func TestGuardrailConfigValidate(t *testing.T) {
	t.Run("default config is valid", func(t *testing.T) {
		if err := DefaultGuardrailConfig().Validate(); err != nil {
			t.Errorf("default config should be valid: %v", err)
		}
	})

	t.Run("zero values are valid", func(t *testing.T) {
		// Zero means "never trigger"  --  valid.
		if err := (ToolCallGuardrailConfig{}).Validate(); err != nil {
			t.Errorf("zero config should be valid: %v", err)
		}
	})

	t.Run("negative values are invalid", func(t *testing.T) {
		tests := []struct {
			name   string
			config ToolCallGuardrailConfig
		}{
			{"negative ExactFailureWarnAfter", ToolCallGuardrailConfig{ExactFailureWarnAfter: -1}},
			{"negative SameToolFailureWarnAfter", ToolCallGuardrailConfig{SameToolFailureWarnAfter: -1}},
			{"negative NoProgressWarnAfter", ToolCallGuardrailConfig{NoProgressWarnAfter: -1}},
			{"negative HardStopAfterWarnRepeat", ToolCallGuardrailConfig{HardStopAfterWarnRepeat: -1}},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				if err := tt.config.Validate(); err == nil {
					t.Error("expected error for negative value")
				}
			})
		}
	})
}

// --- Guardrail decision engine tests ---

func TestGuardrailEngineAllow(t *testing.T) {
	// Below all thresholds  --  should allow.
	g := NewGuardrailEngine(DefaultGuardrailConfig())

	// A few successes  --  no issues.
	g.RecordSuccess("tool-a")
	g.RecordSuccess("tool-b")
	g.RecordSuccess("tool-a")

	if g.TotalCalls() != 3 {
		t.Errorf("TotalCalls = %d, want 3", g.TotalCalls())
	}
}

func TestGuardrailEngineExactFailureWarn(t *testing.T) {
	config := ToolCallGuardrailConfig{
		ExactFailureWarnAfter:   2,
		HardStopAfterWarnRepeat: 99, // effectively never hard-stop
	}
	g := NewGuardrailEngine(config)

	err := errors.New("connection refused")

	// First failure  --  allow.
	d := g.RecordFailure("tool-a", err)
	if d != DecisionAllow {
		t.Errorf("first failure should be allowed, got %v", d)
	}

	// Second same failure  --  warn.
	d = g.RecordFailure("tool-a", err)
	if d != DecisionWarn {
		t.Errorf("second same failure should warn, got %v", d)
	}

	// Different error on same tool  --  doesn't trigger exact-failure warn yet.
	d = g.RecordFailure("tool-a", errors.New("timeout"))
	if d != DecisionAllow {
		t.Errorf("different error should be allowed, got %v", d)
	}
}

func TestGuardrailEngineSameToolFailureWarn(t *testing.T) {
	config := ToolCallGuardrailConfig{
		SameToolFailureWarnAfter: 3,
		HardStopAfterWarnRepeat:  99,
	}
	g := NewGuardrailEngine(config)

	// Three different errors on the same tool  --  same-tool-failure triggers.
	g.RecordFailure("tool-a", errors.New("err1"))
	g.RecordFailure("tool-a", errors.New("err2"))
	d := g.RecordFailure("tool-a", errors.New("err3"))

	if d != DecisionWarn {
		t.Errorf("third same-tool failure should warn, got %v", d)
	}

	// A success resets the consecutive counter.
	g.RecordSuccess("tool-a")
	d = g.RecordFailure("tool-a", errors.New("err4"))
	if d != DecisionAllow {
		t.Errorf("after success, failure should be allow, got %v", d)
	}
}

func TestGuardrailEngineNoProgressWarn(t *testing.T) {
	config := ToolCallGuardrailConfig{
		NoProgressWarnAfter:     5,
		HardStopAfterWarnRepeat: 99,
	}
	g := NewGuardrailEngine(config)

	for range 4 {
		g.RecordSuccess("tool-a")
	}

	// 5th call  --  warn (no progress).
	d := g.RecordSuccess("tool-a")
	if d != DecisionWarn {
		t.Errorf("5th call with no progress should warn, got %v", d)
	}
}

func TestGuardrailEngineMarkProgress(t *testing.T) {
	config := ToolCallGuardrailConfig{
		NoProgressWarnAfter: 5,
	}
	g := NewGuardrailEngine(config)

	for range 4 {
		g.RecordSuccess("tool-a")
	}

	// Mark progress resets the counter.
	g.MarkProgress()

	d := g.RecordSuccess("tool-b")
	if d != DecisionAllow {
		t.Errorf("after MarkProgress, should be allow, got %v", d)
	}
	if g.TotalCalls() != 1 {
		t.Errorf("after MarkProgress, TotalCalls = %d, want 1", g.TotalCalls())
	}
}

func TestGuardrailEngineHardStop(t *testing.T) {
	config := ToolCallGuardrailConfig{
		ExactFailureWarnAfter:   1,
		HardStopAfterWarnRepeat: 2,
	}
	g := NewGuardrailEngine(config)

	err := errors.New("fatal")

	// First failure  --  warn.
	d := g.RecordFailure("tool-a", err)
	if d != DecisionWarn {
		t.Errorf("expected warn, got %v", d)
	}

	// Second same failure  --  hard-stop (warn repeat threshold reached).
	d = g.RecordFailure("tool-a", err)
	if d != DecisionHardStop {
		t.Errorf("expected hard-stop, got %v", d)
	}

	if !g.HardStopped() {
		t.Error("HardStopped should be true")
	}

	// All subsequent calls are hard-stop.
	d = g.RecordSuccess("tool-b")
	if d != DecisionHardStop {
		t.Errorf("after hard-stop, all calls should be hard-stop, got %v", d)
	}
}

func TestGuardrailEngineReset(t *testing.T) {
	config := ToolCallGuardrailConfig{
		ExactFailureWarnAfter:   1,
		HardStopAfterWarnRepeat: 1,
	}
	g := NewGuardrailEngine(config)

	g.RecordFailure("tool-a", errors.New("err"))
	g.RecordFailure("tool-a", errors.New("err"))

	if !g.HardStopped() {
		t.Fatal("expected hard-stopped")
	}

	g.Reset()

	if g.HardStopped() {
		t.Error("after reset, should not be hard-stopped")
	}
	if g.TotalCalls() != 0 {
		t.Errorf("after reset, TotalCalls = %d, want 0", g.TotalCalls())
	}

	// Can record normally again.
	d := g.RecordSuccess("tool-b")
	if d != DecisionAllow {
		t.Errorf("after reset, should allow, got %v", d)
	}
}

func TestGuardrailDecisionString(t *testing.T) {
	tests := []struct {
		d    GuardrailDecision
		want string
	}{
		{DecisionAllow, "allow"},
		{DecisionWarn, "warn"},
		{DecisionHardStop, "hard-stop"},
		{GuardrailDecision(99), "unknown"},
	}
	for _, tt := range tests {
		if got := tt.d.String(); got != tt.want {
			t.Errorf("String() = %q, want %q", got, tt.want)
		}
	}
}

func TestGuardrailEngineConcurrent(t *testing.T) {
	// Use config with all thresholds effectively disabled to avoid
	// guardrail-induced short-circuits during concurrent stress.
	config := ToolCallGuardrailConfig{
		ExactFailureWarnAfter:    99999,
		SameToolFailureWarnAfter: 99999,
		NoProgressWarnAfter:      99999,
		HardStopAfterWarnRepeat:  0, // never hard-stop
	}
	g := NewGuardrailEngine(config)

	done := make(chan struct{})
	for range 10 {
		go func() {
			for range 100 {
				g.RecordSuccess("tool")
				g.RecordFailure("tool", errors.New("err"))
				g.TotalCalls()
				g.HardStopped()
			}
			done <- struct{}{}
		}()
	}

	for range 10 {
		<-done
	}

	// Should not panic  --  race detector will catch issues.
	if g.TotalCalls() != 1000*2 {
		t.Errorf("TotalCalls = %d, want 2000", g.TotalCalls())
	}
}

func TestGuardrailEngineRecordFailureNilError(t *testing.T) {
	// Regression: RecordFailure used err.Error() without a nil check,
	// causing a nil-pointer dereference panic. After fix, nil error is
	// handled gracefully.
	g := NewGuardrailEngine(DefaultGuardrailConfig())

	// Must not panic.
	d := g.RecordFailure("tool-a", nil)
	if d != DecisionAllow {
		t.Errorf("nil error should be DecisionAllow, got %v", d)
	}
	if g.TotalCalls() != 1 {
		t.Errorf("TotalCalls = %d, want 1 (call should still be counted)", g.TotalCalls())
	}
}

func TestGuardrailEngineRecordFailureNilErrorDoesNotCountAsFailure(t *testing.T) {
	// Nil error is treated as "unknown failure" and does not increment
	// failure counters. Successive real failures should behave normally.
	config := ToolCallGuardrailConfig{
		ExactFailureWarnAfter: 2,
	}
	g := NewGuardrailEngine(config)

	// Nil error — allowed, not counted toward exact-failure.
	g.RecordFailure("tool-a", nil)

	// Real error — this is the first real failure.
	realErr := errors.New("real error")
	d := g.RecordFailure("tool-a", realErr)
	if d != DecisionAllow {
		t.Errorf("first real failure should be allowed, got %v", d)
	}

	// Second real failure — should warn.
	d = g.RecordFailure("tool-a", realErr)
	if d != DecisionWarn {
		t.Errorf("second real failure should warn, got %v", d)
	}
}
