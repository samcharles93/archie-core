package main

import "testing"

func TestNATSConnectionSettings(t *testing.T) {
	getenv := func(name string) string {
		switch name {
		case "NATS_URL":
			return "nats://nats.example:4222"
		case "NATS_TOKEN":
			return "test-nats-token"
		default:
			return ""
		}
	}

	t.Run("container environment", func(t *testing.T) {
		url, token := natsConnectionSettings("", getenv)
		if url != "nats://nats.example:4222" || token != "test-nats-token" {
			t.Fatalf("natsConnectionSettings() = (%q, %q)", url, token)
		}
	})

	t.Run("flag URL takes precedence", func(t *testing.T) {
		url, token := natsConnectionSettings("nats://flag.example:4222", getenv)
		if url != "nats://flag.example:4222" || token != "test-nats-token" {
			t.Fatalf("natsConnectionSettings() = (%q, %q)", url, token)
		}
	})
}
