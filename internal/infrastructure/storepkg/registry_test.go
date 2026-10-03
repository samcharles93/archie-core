package storepkg

import "testing"

// Plain HTTP is for this host only: a reference that merely looks local must
// still go over HTTPS.
func TestLoopbackReference(t *testing.T) {
	tests := []struct {
		reference string
		want      bool
	}{
		{"localhost:5000/bws:1.0.0", true},
		{"127.0.0.1:5001/bws", true},
		{"[::1]:5000/bws", true},
		{"ghcr.io/samcharles93/archipelago/bws:1.0.0", false},
		{"localhost.evil.example/bws", false},
		{"127.0.0.1.evil.example/bws", false},
		{"bws", false},
	}
	for _, tt := range tests {
		t.Run(tt.reference, func(t *testing.T) {
			if got := loopbackReference(tt.reference); got != tt.want {
				t.Fatalf("loopbackReference(%q) = %v, want %v", tt.reference, got, tt.want)
			}
		})
	}
}
