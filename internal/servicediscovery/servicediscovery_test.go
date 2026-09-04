package servicediscovery

import (
	"errors"
	"fmt"
	"testing"
)

// TestEventKindString pins the human-readable form of each kind, including the
// zero value (unset/unknown), so logging and diagnostics never render a magic
// number.
func TestEventKindString(t *testing.T) {
	tests := []struct {
		name string
		kind EventKind
		want string
	}{
		{name: "join", kind: Join, want: "join"},
		{name: "leave", kind: Leave, want: "leave"},
		{name: "unknown out-of-range", kind: EventKind(99), want: "unknown"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.kind.String(); got != tt.want {
				t.Errorf("EventKind(%d).String() = %q, want %q", tt.kind, got, tt.want)
			}
		})
	}
}

// TestErrNotInstalledWraps pins that implementations can wrap the sentinel and
// callers can still match it with errors.Is, which is the contract's intended
// use (a broker or registry specific error may be joined around it).
func TestErrNotInstalledWraps(t *testing.T) {
	wrapped := fmt.Errorf("resolve %s: %w", "curator", ErrNotInstalled)
	if !errors.Is(wrapped, ErrNotInstalled) {
		t.Fatalf("wrapped ErrNotInstalled does not match errors.Is: %v", wrapped)
	}
}
