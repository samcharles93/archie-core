package workflow

import (
	"context"
	"log/slog"
	"strings"
	"testing"
)

// TestFeasibilityDeliverNotifiesTheIssueWithoutAWebhook pins archie-core-y1kh.
// notify returned early when [notify].webhook was unset, so a parked PRD left
// the process nowhere and the issue comment both doc comments promised was
// never posted. The deliver stage must post the PRD to the forge issue -- the
// one notification channel a default deployment has -- with no webhook set.
func TestFeasibilityDeliverNotifiesTheIssueWithoutAWebhook(t *testing.T) {
	forgeClient := &fakeForge{}
	tc := &TaskContext{
		Task: &Task{
			ID: 7, Owner: "acme", Repo: "widget", IssueNumber: 42,
			Title: "Add CSV export", Plan: "PRD: export the task list as CSV.", Source: SourceForge,
		},
		Forge: forgeClient,
		Log:   slog.New(slog.DiscardHandler),
		// Cfg.Notify.Webhook is deliberately empty: the default deployment.
	}

	if err := Feasibility().Stages[3].Run(context.Background(), tc); err != nil {
		t.Fatal(err)
	}
	if len(forgeClient.commented) != 1 {
		t.Fatalf("issue comments = %d, want exactly 1 delivery notice with no webhook configured", len(forgeClient.commented))
	}
	if !strings.Contains(forgeClient.commented[0], tc.Task.Plan) {
		t.Errorf("delivery notice does not carry the PRD:\n%s", forgeClient.commented[0])
	}
	if tc.Task.WatchCommentID != 0 {
		t.Errorf("delivery notice recorded a reply cursor (WatchCommentID = %d): the issue is a delivery channel, not a reply channel", tc.Task.WatchCommentID)
	}
	if tc.Outcome.Status != StatusWaitingHuman {
		t.Errorf("outcome = %q, want %q", tc.Outcome.Status, StatusWaitingHuman)
	}
}
