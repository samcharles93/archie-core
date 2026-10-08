package config

import (
	"testing"

	"github.com/samcharles93/archie-core/internal/domain/org"
)

func TestCredentialOrgBoundary(t *testing.T) {
	tests := []struct {
		name, binding, requested, identity, root string
		bound                                    bool
		want                                     string
	}{
		{name: "empty binding matches dashboard default", requested: string(org.DefaultOrgID), bound: true, want: string(org.DefaultOrgID)},
		{name: "explicit default matches root", binding: string(org.DefaultOrgID), bound: true, want: string(org.DefaultOrgID)},
		{name: "empty binding and root", bound: true, want: string(org.DefaultOrgID)},
		{name: "named tenant remains isolated", binding: "acme", requested: "other", identity: "acme", root: "other", want: "acme"},
		{name: "identity inherits root tenant", binding: "acme", requested: "acme", root: "acme", bound: true, want: "acme"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := Config{Org: tt.root, GrantedCredentials: []string{"codex"}, Identities: []IdentityConfig{{Name: "bot", Org: tt.identity}}, Containers: ContainerConfig{Credentials: []CredentialBinding{{Service: "codex", Org: tt.binding}}}}
			got, granted := cfg.CredentialAccess("bot")
			if got != tt.want {
				t.Fatalf("run org %q, want %q", got, tt.want)
			}
			bindings := cfg.Containers.BoundCredentials(tt.requested, granted, []string{"codex"})
			b, ok := bindings["codex"]
			if ok != tt.bound {
				t.Fatalf("bound=%v, want %v", ok, tt.bound)
			}
			if ok && b.Org == "" {
				t.Fatal("returned binding retains empty org")
			}
		})
	}
	cfg := ContainerConfig{Credentials: []CredentialBinding{{Service: "codex"}, {Service: "codex", Org: string(org.DefaultOrgID)}}}
	if err := cfg.ValidateCredentialBindings(); err == nil {
		t.Fatal("empty and explicit default bindings must be duplicates")
	}
}
