package config

import (
	"testing"
	"time"
)

// TestRateLimitConfigEnabled pins Enabled as opt-in: the zero value (an
// absent [chat.rate_limit] table) must leave rate limiting off, and both
// fields must be positive before it counts as configured.
func TestRateLimitConfigEnabled(t *testing.T) {
	tests := []struct {
		name string
		cfg  RateLimitConfig
		want bool
	}{
		{"zero value", RateLimitConfig{}, false},
		{"window only", RateLimitConfig{Window: time.Minute}, false},
		{"max requests only", RateLimitConfig{MaxRequests: 20}, false},
		{"both set", RateLimitConfig{Window: time.Minute, MaxRequests: 20}, true},
		{"negative max requests", RateLimitConfig{Window: time.Minute, MaxRequests: -1}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.cfg.Enabled(); got != tc.want {
				t.Errorf("Enabled() = %v, want %v", got, tc.want)
			}
		})
	}
}
