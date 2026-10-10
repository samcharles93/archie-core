package webui

import "testing"

// TestIsLoopback pins the loopback exemption that lets a bind skip the
// dashboard token: the wildcard host must not qualify, or `-listen :8484`
// would serve every interface unauthenticated.
func TestIsLoopback(t *testing.T) {
	tests := []struct {
		listen string
		want   bool
	}{
		{":8484", false},
		{"0.0.0.0:8484", false},
		{"[::]:8484", false},
		{"192.168.1.5:8484", false},
		{"127.0.0.1:8484", true},
		{"127.0.0.2:8484", true},
		{"[::1]:8484", true},
		{"localhost:8484", true},
	}
	for _, tt := range tests {
		t.Run(tt.listen, func(t *testing.T) {
			if got := IsLoopback(tt.listen); got != tt.want {
				t.Fatalf("IsLoopback(%q) = %v, want %v", tt.listen, got, tt.want)
			}
		})
	}
}
