package configuration

import (
	"testing"
	"time"

	"github.com/samcharles93/archie-core/internal/config"
)

func TestApplyCaptureDefaultsFillsZeroValues(t *testing.T) {
	cfg := &config.Config{}
	applyCaptureDefaults(cfg)

	if got, want := cfg.Capture.Retention.Std(), defaultCaptureRetention; got != want {
		t.Errorf("Capture.Retention = %v, want %v", got, want)
	}
	if cfg.Capture.MaxEvents != defaultCaptureMaxEvents {
		t.Errorf("Capture.MaxEvents = %d, want %d", cfg.Capture.MaxEvents, defaultCaptureMaxEvents)
	}
	if cfg.Capture.MaxBodyBytes != defaultCaptureMaxBodyBytes {
		t.Errorf("Capture.MaxBodyBytes = %d, want %d", cfg.Capture.MaxBodyBytes, defaultCaptureMaxBodyBytes)
	}
	if cfg.Capture.RatePerSecond != defaultCaptureRatePerSecond {
		t.Errorf("Capture.RatePerSecond = %v, want %v", cfg.Capture.RatePerSecond, defaultCaptureRatePerSecond)
	}
	if cfg.Capture.RateBurst != defaultCaptureRateBurst {
		t.Errorf("Capture.RateBurst = %d, want %d", cfg.Capture.RateBurst, defaultCaptureRateBurst)
	}
}

func TestApplyCaptureDefaultsLeavesExplicitValuesAlone(t *testing.T) {
	cfg := &config.Config{
		Capture: config.CaptureConfig{
			Retention:     config.Duration(24 * time.Hour),
			MaxEvents:     42,
			MaxBodyBytes:  1024,
			RatePerSecond: 2.5,
			RateBurst:     9,
		},
	}
	applyCaptureDefaults(cfg)

	if cfg.Capture.Retention.Std() != 24*time.Hour {
		t.Errorf("Capture.Retention = %v, want unchanged 24h", cfg.Capture.Retention.Std())
	}
	if cfg.Capture.MaxEvents != 42 {
		t.Errorf("Capture.MaxEvents = %d, want unchanged 42", cfg.Capture.MaxEvents)
	}
	if cfg.Capture.MaxBodyBytes != 1024 {
		t.Errorf("Capture.MaxBodyBytes = %d, want unchanged 1024", cfg.Capture.MaxBodyBytes)
	}
	if cfg.Capture.RatePerSecond != 2.5 {
		t.Errorf("Capture.RatePerSecond = %v, want unchanged 2.5", cfg.Capture.RatePerSecond)
	}
	if cfg.Capture.RateBurst != 9 {
		t.Errorf("Capture.RateBurst = %d, want unchanged 9", cfg.Capture.RateBurst)
	}
}
