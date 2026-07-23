package main

import (
	"strings"
	"testing"

	"github.com/samcharles93/archie-core/internal/config"
)

func TestConfiguredNATSToken(t *testing.T) {
	t.Run("anonymous when token env is not configured", func(t *testing.T) {
		token, err := configuredNATSToken(config.NATSConfig{}, func(string) string {
			return ""
		})
		if err != nil || token != "" {
			t.Fatalf("configuredNATSToken() = (%q, %v), want empty token and nil error", token, err)
		}
	})

	t.Run("reads configured environment variable", func(t *testing.T) {
		token, err := configuredNATSToken(
			config.NATSConfig{TokenEnv: "ARCHIE_NATS_SECRET"},
			func(name string) string {
				if name == "ARCHIE_NATS_SECRET" {
					return "test-nats-token"
				}
				return ""
			},
		)
		if err != nil || token != "test-nats-token" {
			t.Fatalf("configuredNATSToken() = (%q, %v)", token, err)
		}
	})

	t.Run("rejects missing configured credential", func(t *testing.T) {
		_, err := configuredNATSToken(
			config.NATSConfig{TokenEnv: "ARCHIE_NATS_SECRET"},
			func(string) string { return "" },
		)
		if err == nil || !strings.Contains(err.Error(), "ARCHIE_NATS_SECRET") {
			t.Fatalf("configuredNATSToken() error = %v, want missing variable name", err)
		}
	})
}
