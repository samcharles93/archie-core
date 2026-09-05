package archied

import (
	"testing"

	"github.com/samcharles93/archie-core/internal/config"
)

func TestChatRepoEnvSingleIdentity(t *testing.T) {
	cfg := config.Config{
		Forge: config.Forge{Host: "https://github.com", Type: "github"},
		Repos: []config.Repo{
			{Owner: "my-org", Name: "frontend-app", Base: "develop"},
			{Owner: "my-org", Name: "backend-api"},
		},
	}
	got := chatRepoEnv(cfg, "")
	if len(got) != 2 {
		t.Fatalf("expected 2 repos, got %d", len(got))
	}
	if got[0].FullName != "my-org/frontend-app" || got[0].Forge != "https://github.com" || got[0].DefaultBranch != "develop" {
		t.Errorf("repo 0 mismatch: %+v", got[0])
	}
	// Base empty -> the daemon's effective default branch (main), not a guess.
	if got[1].DefaultBranch != "main" {
		t.Errorf("expected default branch main, got %q", got[1])
	}
}

func TestChatRepoEnvMultiIdentityMatchesActive(t *testing.T) {
	cfg := config.Config{
		Identities: []config.IdentityConfig{
			{Name: "alpha", Forge: config.Forge{Host: "https://gitea.example.com"}, Repos: []config.Repo{{Owner: "alpha", Name: "app"}}},
			{Name: "beta", Forge: config.Forge{Host: "https://github.com"}, Repos: []config.Repo{{Owner: "beta", Name: "svc"}}},
		},
	}
	got := chatRepoEnv(cfg, "beta")
	if len(got) != 1 || got[0].FullName != "beta/svc" || got[0].Forge != "https://github.com" {
		t.Fatalf("expected the active identity's scope, got %+v", got)
	}
}

func TestChatRepoEnvMultiIdentityFallsBackToFirstWithRepos(t *testing.T) {
	cfg := config.Config{
		Identities: []config.IdentityConfig{
			{Name: "a", Forge: config.Forge{}, Repos: nil},
			{Name: "b", Forge: config.Forge{Host: "https://github.com"}, Repos: []config.Repo{{Owner: "b", Name: "svc"}}},
		},
	}
	got := chatRepoEnv(cfg, "no-such-identity")
	if len(got) != 1 || got[0].FullName != "b/svc" {
		t.Fatalf("expected fallback to the first identity with repos, got %+v", got)
	}
}

func TestChatRepoEnvPreservesEmptyForge(t *testing.T) {
	cfg := config.Config{
		Repos: []config.Repo{{Owner: "acme", Name: "widgets"}},
	}
	got := chatRepoEnv(cfg, "")
	if len(got) != 1 {
		t.Fatalf("expected 1 repo, got %d", len(got))
	}
	if got[0].Forge != "" {
		t.Errorf("expected empty forge to be preserved (so the prompt renders 'forge unknown'), got %q", got[0].Forge)
	}
	if got[0].DefaultBranch != "main" {
		t.Errorf("expected default branch main, got %q", got[0].DefaultBranch)
	}
}

func TestChatRepoEnvEmpty(t *testing.T) {
	if got := chatRepoEnv(config.Config{}, ""); len(got) != 0 {
		t.Fatalf("expected no repos, got %+v", got)
	}
}
