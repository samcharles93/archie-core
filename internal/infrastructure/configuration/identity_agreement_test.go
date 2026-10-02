package configuration

import (
	"strings"
	"testing"

	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/domain/identity"
)

// TestIdentityNameRuleAgreesAcrossFileAndStore pins the one field both paths
// judge: a configured identity name becomes the store identity's display name
// (internal/app/archied.configuredIdentityNames ->
// postgres.Store.BootstrapIdentities -> identity.New), so a name the file
// loader accepts must be a name the store accepts, and a name it refuses must
// be refused on both sides. Two rule sets on the same string is how a name of
// spaces passed the file's `name == ""` check and then failed the store's
// trimmed check -- the file accepted a config the State Store could not boot
// with.
func TestIdentityNameRuleAgreesAcrossFileAndStore(t *testing.T) {
	tests := []struct {
		name       string
		configured string
		wantReject bool
	}{
		{name: "ordinary name", configured: "personal"},
		{name: "name padded with whitespace", configured: "  personal  "},
		{name: "whitespace-only name", configured: "   ", wantReject: true},
		{name: "empty name", configured: "", wantReject: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := minimalValidConfig()
			cfg.BotUser = ""
			cfg.Identities = []config.IdentityConfig{{
				Name:    tt.configured,
				BotUser: "archie-personal",
				Repos:   []config.Repo{{Owner: "acme", Name: "app"}},
				Forge:   config.Forge{Type: "none"},
			}}
			fileErr := Validate(&cfg)
			_, storeErr := identity.New(identity.StableID(tt.configured), identity.KindBot, tt.configured)
			if (fileErr != nil) != tt.wantReject {
				t.Fatalf("Validate(identities[0].name=%q) = %v, wantReject=%v", tt.configured, fileErr, tt.wantReject)
			}
			if (storeErr != nil) != tt.wantReject {
				t.Fatalf("identity.New(%q) = %v, wantReject=%v", tt.configured, storeErr, tt.wantReject)
			}
		})
	}
}

// TestBotUserNameRuleAgreesAcrossFileAndStore is the single-identity half of
// the same rule: configuredIdentityNames turns bot_user into the same display
// name, and BootstrapIdentities trims it the way identity.New does, so the
// file loader's `bot_user == ""` test must reject a name of spaces exactly as
// the store does.
func TestBotUserNameRuleAgreesAcrossFileAndStore(t *testing.T) {
	tests := []struct {
		name       string
		botUser    string
		wantReject bool
	}{
		{name: "ordinary bot user", botUser: "archie-bot"},
		{name: "bot user padded with whitespace", botUser: " archie-bot "},
		{name: "whitespace-only bot user", botUser: "   ", wantReject: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := minimalValidConfig()
			cfg.BotUser = tt.botUser
			fileErr := Validate(&cfg)
			trimmed := strings.TrimSpace(tt.botUser)
			_, storeErr := identity.New(identity.StableID(trimmed), identity.KindBot, trimmed)
			if (fileErr != nil) != tt.wantReject {
				t.Fatalf("Validate(bot_user=%q) = %v, wantReject=%v", tt.botUser, fileErr, tt.wantReject)
			}
			if (storeErr != nil) != tt.wantReject {
				t.Fatalf("identity.New(bot_user=%q) = %v, wantReject=%v", tt.botUser, storeErr, tt.wantReject)
			}
		})
	}
}
