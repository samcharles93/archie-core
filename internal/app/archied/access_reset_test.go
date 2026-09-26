package archied

import (
	"strings"
	"testing"
)

func TestIsAccessResetArgs(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want bool
	}{
		{name: "org reset", args: []string{"access", "reset", "--org", "acme"}, want: true},
		{name: "instance reset", args: []string{"access", "reset", "--instance"}, want: true},
		{name: "config first", args: []string{"access", "reset", "-config", "/tmp/config.toml", "--org", "acme"}, want: true},
		{name: "setup is not a reset", args: []string{"setup"}, want: false},
		{name: "bare access", args: []string{"access"}, want: false},
		{name: "other subcommand", args: []string{"access", "grant"}, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsAccessResetArgs(tt.args); got != tt.want {
				t.Fatalf("IsAccessResetArgs(%v) = %v, want %v", tt.args, got, tt.want)
			}
		})
	}
}

func TestRunAccessResetUsage(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		wantCode int
		wantText string
	}{
		{name: "no target", args: nil, wantCode: 2, wantText: "exactly one of --org"},
		{name: "both targets", args: []string{"--org", "acme", "--instance"}, wantCode: 2, wantText: "exactly one of --org"},
		{name: "unexpected argument", args: []string{"--instance", "extra"}, wantCode: 2, wantText: "unexpected argument"},
		{name: "unknown flag", args: []string{"--workspace", "net"}, wantCode: 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stderr strings.Builder
			code := RunAccessReset(t.Context(), tt.args, &stderr)
			if code != tt.wantCode {
				t.Fatalf("RunAccessReset(%v) = %d, want %d (stderr: %s)", tt.args, code, tt.wantCode, stderr.String())
			}
			if tt.wantText != "" && !strings.Contains(stderr.String(), tt.wantText) {
				t.Fatalf("stderr = %q, want it to name %q", stderr.String(), tt.wantText)
			}
		})
	}
}
