package archied

import (
	"log/slog"
	"path/filepath"
	"testing"

	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/forge"
	"github.com/samcharles93/archie-core/internal/gateway"
	"github.com/samcharles93/archie-core/internal/secret"
)

// TestGatewayWorktreeManagerAdvertisesReviewPR composes the Gateway's own
// tree step with the reviewer and the review tool: whatever
// buildWorktreeManager produces must be enough for boot.prReviewer to yield a
// reviewer and for ReviewTools to advertise review_pr. The call-order
// assertion in composition_order_test.go proves the Gateway makes this step;
// this proves the step is sufficient, so a manager built without the forge
// credential or the configured work dir cannot pass as wired.
func TestGatewayWorktreeManagerAdvertisesReviewPR(t *testing.T) {
	const token = "gateway-forge-token"
	cfg := config.Config{
		WorkDir:  t.TempDir(),
		BotUser:  "archie-bot",
		BotEmail: "bot@example.test",
		Forge:    config.Forge{Host: "https://forge.example.test"},
	}
	b := &boot{
		cfg:         cfg,
		cfgHolder:   config.NewHolder(cfg),
		token:       token,
		log:         slog.New(slog.DiscardHandler),
		forgeClient: reviewCapableForge{Forge: forge.NewNoop(slog.New(slog.DiscardHandler))},
	}

	b.buildWorktreeManager()

	if b.trees.WorkDir != cfg.WorkDir {
		t.Errorf("worktree manager WorkDir = %q, want the configured %q", b.trees.WorkDir, cfg.WorkDir)
	}
	if b.trees.BaseURL != cfg.Forge.Host {
		t.Errorf("worktree manager BaseURL = %q, want the configured forge host %q", b.trees.BaseURL, cfg.Forge.Host)
	}
	if b.trees.Token != token {
		t.Errorf("worktree manager Token = %q, want the resolved forge token %q", b.trees.Token, token)
	}

	reviewer := b.prReviewer()
	if reviewer == nil {
		t.Fatal("prReviewer() = nil after the Gateway's worktree-manager step; review_pr would never be advertised")
	}
	if !hasReviewPRTool(gateway.ReviewTools(reviewer, "archie")) {
		t.Fatal("review_pr not advertised after the Gateway's worktree-manager step")
	}
}

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
