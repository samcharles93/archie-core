package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestRunRelay(t *testing.T) {
	t.Run("-to is required", func(t *testing.T) {
		var stderr bytes.Buffer
		called := false
		serve := func(context.Context, string, string) error { called = true; return nil }
		if code := runRelay([]string{"-listen", "127.0.0.1:0"}, &stderr, serve); code != 1 {
			t.Fatalf("exit %d, want 1", code)
		}
		if called || !strings.Contains(stderr.String(), "-to is required") {
			t.Fatalf("served=%v stderr=%q", called, stderr.String())
		}
	})
	t.Run("serves the named target on the listen address", func(t *testing.T) {
		var gotTarget, gotAddr string
		serve := func(_ context.Context, listen, target string) error {
			gotTarget, gotAddr = target, listen
			return nil
		}
		if code := runRelay([]string{"-listen", "127.0.0.1:0", "-to", "172.17.0.1:3129"}, &bytes.Buffer{}, serve); code != 0 {
			t.Fatalf("exit %d, want 0", code)
		}
		if gotTarget != "172.17.0.1:3129" || gotAddr != "127.0.0.1:0" {
			t.Fatalf("served %q on %q", gotTarget, gotAddr)
		}
	})
}

func TestRelaySubcommandRoutes(t *testing.T) {
	var stderr bytes.Buffer
	code := runCommand([]string{"relay"}, func(string) string { return "" }, &stderr, nil)
	if code != 1 || !strings.Contains(stderr.String(), "-to is required") {
		t.Fatalf("relay subcommand exit %d stderr %q; want the relay's own usage error", code, stderr.String())
	}
}
