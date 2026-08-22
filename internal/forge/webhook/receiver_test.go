package webhook

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/google/go-github/v78/github"

	"github.com/samcharles93/archie-core/internal/domain/workintake"
)

const (
	testSecret = "test-secret"
	testBot    = "archie-bot"
	testLabel  = "archie"
)

func issueEvent(t *testing.T, action string, mutate func(*github.IssuesEvent)) *github.IssuesEvent {
	t.Helper()
	ev := &github.IssuesEvent{
		Action: new(action),
		Issue: &github.Issue{
			Number: new(42),
			Title:  new("Do the thing"),
			Body:   new("please"),
			State:  new("open"),
		},
		Repo: &github.Repository{
			Name:  new("repo"),
			Owner: &github.User{Login: new("owner")},
		},
	}
	if mutate != nil {
		mutate(ev)
	}
	return ev
}

func signedRequest(t *testing.T, secret, eventType string, payload []byte) *http.Request {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/webhook", strings.NewReader(string(payload)))
	req.Header.Set("X-GitHub-Event", eventType)
	if secret != "" {
		mac := hmac.New(sha256.New, []byte(secret))
		mac.Write(payload)
		req.Header.Set("X-Hub-Signature-256", "sha256="+hex.EncodeToString(mac.Sum(nil)))
	}
	return req
}

func serve(t *testing.T, r *Receiver, req *http.Request) (*httptest.ResponseRecorder, []workintake.TaskEnvelope) {
	t.Helper()
	var published []workintake.TaskEnvelope
	r.publish = func(_ context.Context, task workintake.TaskEnvelope) error {
		published = append(published, task)
		return nil
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec, published
}

func TestReceiverPublishesMatchingIssue(t *testing.T) {
	tests := []struct {
		name       string
		trigger    string
		cfgLabel   string
		eventType  string
		action     string
		secret     string
		mutate     func(*github.IssuesEvent)
		wantStatus int
		wantPubl   int
	}{
		{
			name:       "labelled issue matches label trigger",
			trigger:    "label",
			cfgLabel:   testLabel,
			eventType:  "issues",
			action:     "labeled",
			secret:     testSecret,
			mutate:     func(ev *github.IssuesEvent) { ev.Issue.Labels = []*github.Label{{Name: new(testLabel)}} },
			wantStatus: http.StatusAccepted,
			wantPubl:   1,
		},
		{
			name:       "assigned issue matches assignee trigger",
			trigger:    "assignee",
			eventType:  "issues",
			action:     "assigned",
			secret:     testSecret,
			mutate:     func(ev *github.IssuesEvent) { ev.Issue.Assignees = []*github.User{{Login: new(testBot)}} },
			wantStatus: http.StatusAccepted,
			wantPubl:   1,
		},
		{
			name:       "opened issue with label matches label trigger",
			trigger:    "label",
			cfgLabel:   testLabel,
			eventType:  "issues",
			action:     "opened",
			secret:     testSecret,
			mutate:     func(ev *github.IssuesEvent) { ev.Issue.Labels = []*github.Label{{Name: new(testLabel)}} },
			wantStatus: http.StatusAccepted,
			wantPubl:   1,
		},
		{
			name:       "wrong label does not match",
			trigger:    "label",
			cfgLabel:   testLabel,
			eventType:  "issues",
			action:     "labeled",
			secret:     testSecret,
			mutate:     func(ev *github.IssuesEvent) { ev.Issue.Labels = []*github.Label{{Name: new("other")}} },
			wantStatus: http.StatusAccepted,
			wantPubl:   0,
		},
		{
			name:       "invalid signature is rejected",
			trigger:    "label",
			cfgLabel:   testLabel,
			eventType:  "issues",
			action:     "labeled",
			secret:     "wrong-secret",
			mutate:     func(ev *github.IssuesEvent) { ev.Issue.Labels = []*github.Label{{Name: new(testLabel)}} },
			wantStatus: http.StatusUnauthorized,
			wantPubl:   0,
		},
		{
			name:       "missing signature is rejected",
			trigger:    "label",
			cfgLabel:   testLabel,
			eventType:  "issues",
			action:     "labeled",
			secret:     "",
			mutate:     func(ev *github.IssuesEvent) { ev.Issue.Labels = []*github.Label{{Name: new(testLabel)}} },
			wantStatus: http.StatusUnauthorized,
			wantPubl:   0,
		},
		{
			name:       "non-issue event is acknowledged and ignored",
			trigger:    "label",
			cfgLabel:   testLabel,
			eventType:  "ping",
			action:     "",
			secret:     testSecret,
			wantStatus: http.StatusAccepted,
			wantPubl:   0,
		},
		{
			name:      "pull request is ignored",
			trigger:   "label",
			cfgLabel:  testLabel,
			eventType: "issues",
			action:    "labeled",
			secret:    testSecret,
			mutate: func(ev *github.IssuesEvent) {
				ev.Issue.Labels = []*github.Label{{Name: new(testLabel)}}
				ev.Issue.PullRequestLinks = &github.PullRequestLinks{URL: new("https://api.github.com/repos/o/r/pulls/1")}
			},
			wantStatus: http.StatusAccepted,
			wantPubl:   0,
		},
		{
			name:      "closed issue is ignored",
			trigger:   "label",
			cfgLabel:  testLabel,
			eventType: "issues",
			action:    "labeled",
			secret:    testSecret,
			mutate: func(ev *github.IssuesEvent) {
				ev.Issue.State = new("closed")
				ev.Issue.Labels = []*github.Label{{Name: new(testLabel)}}
			},
			wantStatus: http.StatusAccepted,
			wantPubl:   0,
		},
		{
			name:       "unlabeled action never queues",
			trigger:    "label",
			cfgLabel:   testLabel,
			eventType:  "issues",
			action:     "unlabeled",
			secret:     testSecret,
			mutate:     func(ev *github.IssuesEvent) { ev.Issue.Labels = []*github.Label{{Name: new(testLabel)}} },
			wantStatus: http.StatusAccepted,
			wantPubl:   0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ev := issueEvent(t, tc.action, tc.mutate)
			payload, err := json.Marshal(ev)
			if err != nil {
				t.Fatalf("marshal event: %v", err)
			}
			// The receiver always trusts testSecret; tc.secret is what the
			// request is signed with, so a case can sign with the wrong (or no)
			// secret to exercise rejection.
			r := New(testSecret, tc.trigger, tc.cfgLabel, testBot, nil, nil)
			rec, published := serve(t, r, signedRequest(t, tc.secret, tc.eventType, payload))

			if rec.Code != tc.wantStatus {
				t.Errorf("status = %d, want %d", rec.Code, tc.wantStatus)
			}
			if len(published) != tc.wantPubl {
				t.Errorf("published %d envelopes, want %d", len(published), tc.wantPubl)
			}
		})
	}
}

func TestPublishedEnvelopeCarriesIssueIdentityAndKind(t *testing.T) {
	ev := issueEvent(t, "labeled", func(ev *github.IssuesEvent) {
		ev.Issue.Labels = []*github.Label{{Name: new(testLabel)}, {Name: new("bug")}}
	})
	payload, err := json.Marshal(ev)
	if err != nil {
		t.Fatalf("marshal event: %v", err)
	}
	r := New(testSecret, "label", testLabel, testBot, nil, nil)
	_, published := serve(t, r, signedRequest(t, testSecret, "issues", payload))

	if len(published) != 1 {
		t.Fatalf("published = %d, want 1", len(published))
	}
	task := published[0]
	if task.Owner != "owner" || task.Repo != "repo" || task.Number != 42 {
		t.Errorf("envelope identity = %s/%s#%d, want owner/repo#42", task.Owner, task.Repo, task.Number)
	}
	if task.Kind != workintake.KindBug {
		t.Errorf("envelope kind = %q, want %q", task.Kind, workintake.KindBug)
	}
	if !slices.Contains(task.Labels, testLabel) || !slices.Contains(task.Labels, "bug") {
		t.Errorf("envelope labels = %v, want to contain %q and %q", task.Labels, testLabel, "bug")
	}
	if task.IdempotencyKey() != "archie:owner/repo/42" {
		t.Errorf("idempotency key = %q, want archie:owner/repo/42", task.IdempotencyKey())
	}
}
