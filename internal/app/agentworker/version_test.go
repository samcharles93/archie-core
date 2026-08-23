package agentworker

import "testing"

// TestVersionReportsDevWhenUnstamped guards the unstamped-build fallback:
// an empty ldflags value (e.g. a local `go build`, or a release pipeline
// that forgot -X for this package) must read as "dev", the same
// placeholder cmd/archied's own version vars use, not as an empty string
// that would silently sail through daemon.AgentStatus as a real version.
func TestVersionReportsDevWhenUnstamped(t *testing.T) {
	orig := version
	t.Cleanup(func() { version = orig })
	version = ""

	if got := Version(); got != "dev" {
		t.Errorf("Version() = %q, want %q", got, "dev")
	}
}

func TestVersionReportsStampedValue(t *testing.T) {
	orig := version
	t.Cleanup(func() { version = orig })
	version = "1.9.11"

	if got := Version(); got != "1.9.11" {
		t.Errorf("Version() = %q, want %q", got, "1.9.11")
	}
}
