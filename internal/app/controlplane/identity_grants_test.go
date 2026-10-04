package controlplane

import (
	"slices"
	"testing"

	"github.com/samcharles93/archie-core/internal/config"
)

// TestIdentityGrantsReplaceTheFiles pins that the stored grants replace the
// file's: a listed identity gets exactly its list, an unlisted one is left to
// inherit the root's (nil), and an empty list grants nothing.
func TestIdentityGrantsReplaceTheFiles(t *testing.T) {
	fileList := []string{"from-file"}
	cfg := config.Config{
		GrantedCredentials: []string{"file-root"},
		Identities: []config.IdentityConfig{
			{Name: "listed", GrantedCredentials: &fileList},
			{Name: "unlisted", GrantedCredentials: &fileList},
			{Name: "none"},
		},
	}
	applyIdentityGrants(&cfg, identityGrants{
		Root:       []string{"github"},
		Identities: map[string][]string{"listed": {"anthropic"}, "none": {}},
	})
	if !slices.Equal(cfg.GrantedCredentials, []string{"github"}) {
		t.Fatalf("root granted %v", cfg.GrantedCredentials)
	}
	tests := []struct {
		identity string
		want     []string // nil means inherit the root
	}{
		{"listed", []string{"anthropic"}},
		{"unlisted", nil},
		{"none", []string{}},
	}
	for i, tt := range tests {
		t.Run(tt.identity, func(t *testing.T) {
			got := cfg.Identities[i].GrantedCredentials
			if (got == nil) != (tt.want == nil) || (got != nil && !slices.Equal(*got, tt.want)) {
				t.Fatalf("granted %v, want %v", got, tt.want)
			}
		})
	}
}
