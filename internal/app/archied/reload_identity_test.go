package archied

import (
	"slices"
	"testing"

	"github.com/samcharles93/archie-core/internal/config"
)

func TestReloadPreservesForgeIdentity(t *testing.T) {
	for _, tt := range []struct {
		name, field string
		change      func(*config.Config)
	}{
		{"forge host", "Forge", func(c *config.Config) { c.Forge.Host = "other.example" }},
		{"bot user", "BotUser", func(c *config.Config) { c.BotUser = "other-account" }},
		{"commit email", "BotEmail", func(c *config.Config) { c.BotEmail = "other@example.test" }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			old := config.Config{BotUser: "archie", BotEmail: "archie@example.test", Forge: config.Forge{Host: "forge.example"}}
			next := old.Clone()
			tt.change(&next)
			if restored := preserveForgeIdentity(old, next); restored.BotUser != old.BotUser || restored.BotEmail != old.BotEmail || restored.Forge != old.Forge {
				t.Fatal("reload changed the running forge identity")
			}
			if changed := changedNonReloadableFields(old, next); !slices.Contains(changed, tt.field) {
				t.Fatalf("identity change was reloadable: %v", changed)
			}
		})
	}
}
