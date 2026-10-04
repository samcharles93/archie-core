package workflow

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// notify posts a one-way notice on a forge-backed task's issue and, when
// configured, to the notify webhook.
func notify(ctx context.Context, tc *TaskContext, kind string) {
	if tc.Task.IsForgeBacked() {
		if _, err := tc.Forge.Comment(ctx, tc.Task.Owner, tc.Task.Repo, tc.Task.IssueNumber, deliveryNotice(tc, kind)); err != nil {
			tc.Log.Warn("delivery notice not posted",
				"kind", kind, "issue", tc.Task.IssueNumber, "err", err)
		}
	}
	url := tc.Cfg.Notify.Webhook
	if url == "" {
		return
	}
	fields := map[string]any{
		"type": kind, "repo": tc.Repo.FullName(), "task_id": tc.Task.ID,
		"source": tc.Task.Source, "identity": tc.Task.Identity,
		"title": tc.Task.Title, "prd": tc.Task.Plan,
	}
	if tc.Task.IsForgeBacked() {
		fields["issue"] = tc.Task.IssueNumber
		fields["issue_url"] = fmt.Sprintf("%s/%s/issues/%d",
			tc.Cfg.Forge.Host, tc.Repo.FullName(), tc.Task.IssueNumber)
	}
	payload, _ := json.Marshal(fields)
	reqCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		tc.Log.Warn("notify webhook request build failed", "err", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		tc.Log.Warn("notify webhook failed", "err", err)
		return
	}
	if err := resp.Body.Close(); err != nil {
		tc.Log.Warn("close notify webhook response", "err", err)
	}
}

// deliveryNotice is the body of the one-way notice notify posts on a
// forge-backed task's issue.
func deliveryNotice(tc *TaskContext, kind string) string {
	subject := "archie finished a run that needs you"
	if kind == "approval" {
		subject = "archie's PRD is ready  --  awaiting your go/no-go"
	}
	return fmt.Sprintf("**%s.**\n\n%s\n\n_Decide in chat or on the dashboard._", subject, tc.Task.Plan)
}
