package archied

import (
	"log/slog"
	"path/filepath"
	"testing"

	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/secret"
)

// TestBuildTreesAndIdentitiesBuildsRootManagerAndIdentityRunners pins the
// daemon's tree composition, which the shared buildWorktreeManager must not
// change: the daemon still owns the process-wide manager *and* one runner per
// configured identity, each with its own WorkDir so concurrent identities
// never clone into the same directory. The Gateway takes only the manager, so
// this is the assertion that keeps the two roots apart.
func TestBuildTreesAndIdentitiesBuildsRootManagerAndIdentityRunners(t *testing.T) {
	workDir := t.TempDir()
	cfg := config.Config{
		WorkDir:  workDir,
		BotUser:  "archie-bot",
		BotEmail: "bot@example.test",
		Forge:    config.Forge{Host: "https://forge.example.test"},
		Identities: []config.IdentityConfig{{
			Name:    "archie",
			BotUser: "archie-identity",
		}},
	}
	b := &boot{
		cfg:     cfg,
		token:   "daemon-forge-token",
		log:     slog.New(slog.DiscardHandler),
		secrets: secret.NewRegistry(),
	}

	if err := b.buildTreesAndIdentities(t.Context()); err != nil {
		t.Fatalf("buildTreesAndIdentities = %v", err)
	}

	if b.trees == nil {
		t.Fatal("b.trees = nil; the daemon would work no task")
	}
	if b.trees.WorkDir != workDir || b.trees.Token != b.token {
		t.Errorf("root worktree manager = {WorkDir:%q Token:%q}, want {%q %q}", b.trees.WorkDir, b.trees.Token, workDir, b.token)
	}
	if got := len(b.identityRunners); got != 1 {
		t.Fatalf("identityRunners = %d, want 1", got)
	}
	wantIdentityDir := filepath.Join(workDir, "identity-archie")
	if got := b.identityRunners[0].Trees.WorkDir; got != wantIdentityDir {
		t.Errorf("identity worktree manager WorkDir = %q, want %q", got, wantIdentityDir)
	}
}
