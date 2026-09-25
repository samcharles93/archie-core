package main

import (
	"bytes"
	"context"
	"maps"
	"strings"
	"testing"
)

func TestRunRelay(t *testing.T) {
	noServe := func(t *testing.T) relayServer {
		return func(context.Context, map[string]string) error {
			t.Error("the relay served despite a usage error")
			return nil
		}
	}
	for name, args := range map[string][]string{
		"no forward":              {},
		"a forward with no =":     {"-forward", "127.0.0.1:0"},
		"a forward with no port":  {"-forward", "=172.17.0.1:3129"},
		"one listen address used": {"-forward", ":3128=a:1", "-forward", ":3128=b:2"},
	} {
		t.Run(name+" is a usage error", func(t *testing.T) {
			var stderr bytes.Buffer
			if code := runRelay(args, &stderr, noServe(t)); code == 0 {
				t.Fatalf("exit 0, want a usage error; stderr %q", stderr.String())
			}
		})
	}
	t.Run("every forward is served", func(t *testing.T) {
		var got map[string]string
		serve := func(_ context.Context, forwards map[string]string) error {
			got = forwards
			return nil
		}
		args := []string{"-forward", ":3128=172.17.0.1:3129", "-forward", ":4222=172.17.0.1:4222"}
		if code := runRelay(args, &bytes.Buffer{}, serve); code != 0 {
			t.Fatalf("exit %d, want 0", code)
		}
		want := map[string]string{":3128": "172.17.0.1:3129", ":4222": "172.17.0.1:4222"}
		if !maps.Equal(got, want) {
			t.Fatalf("served %v, want %v", got, want)
		}
	})
}

func TestRelaySubcommandRoutes(t *testing.T) {
	var stderr bytes.Buffer
	code := runCommand([]string{"relay"}, func(string) string { return "" }, &stderr, nil)
	if code != 1 || !strings.Contains(stderr.String(), "-forward") {
		t.Fatalf("relay subcommand exit %d stderr %q; want the relay's own usage error", code, stderr.String())
	}
}
