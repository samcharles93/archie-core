package forge

import (
	"context"
	"log/slog"
)

// NoopForge is a zero-op Forge implementation used when forge polling is disabled ("none", "off", or "disabled").
type NoopForge struct {
	log *slog.Logger
}

// NewNoop returns a NoopForge instance.
func NewNoop(log *slog.Logger) Forge {
	if log == nil {
		log = slog.Default()
	}
	return &NoopForge{log: log}
}

func (n *NoopForge) AssignedIssues(ctx context.Context, owner, repo, assignee string) ([]Issue, error) {
	return nil, nil
}

func (n *NoopForge) IssuesWithLabel(ctx context.Context, owner, repo, label string) ([]Issue, error) {
	return nil, nil
}

func (n *NoopForge) Comment(ctx context.Context, owner, repo string, number int, body string) (int64, error) {
	return 0, nil
}

func (n *NoopForge) RepliesAfter(ctx context.Context, owner, repo string, number int, afterID int64, exclude string) ([]Reply, error) {
	return nil, nil
}

func (n *NoopForge) CloseIssue(ctx context.Context, owner, repo string, number int, comment string) error {
	return nil
}

func (n *NoopForge) CreateIssue(ctx context.Context, owner, repo, title, body string, labels []string) (int, error) {
	return 0, nil
}

func (n *NoopForge) React(ctx context.Context, owner, repo string, number int, reaction string) error {
	return nil
}

func (n *NoopForge) SetStateLabel(ctx context.Context, owner, repo string, number int, label string, knownLabels []string) {
}

func (n *NoopForge) CreatePR(ctx context.Context, owner, repo, title, head, base, body string) (int, error) {
	return 0, nil
}

func (n *NoopForge) PRState(ctx context.Context, owner, repo string, number int) (string, error) {
	return "closed", nil
}

func (n *NoopForge) AcceptInvitations(ctx context.Context) error {
	return nil
}

func (n *NoopForge) VerifyPush(ctx context.Context, owner, repo string) error {
	return nil
}

func (n *NoopForge) LinkBranch(ctx context.Context, owner, repo string, issueNumber int, branch string) error {
	return nil
}
