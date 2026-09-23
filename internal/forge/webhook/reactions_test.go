package webhook

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/go-github/v78/github"

	"github.com/samcharles93/archie-core/internal/domain/workintake"
)

// reviewEvent builds a signed pull_request_review delivery, GitHub's
// low-latency path for "a human reacted to archie's PR" (decision 2).
func reviewEvent(t *testing.T, action string, mutate func(*github.PullRequestReviewEvent)) *http.Request {
	t.Helper()
	ev := &github.PullRequestReviewEvent{
		Action: new(action),
		Review: &github.PullRequestReview{
			ID:    new(int64(7)),
			State: new("requested_changes"),
			Body:  new("please fix the loop"),
			User:  &github.User{Login: new("alice")},
		},
		PullRequest: &github.PullRequest{Number: new(42)},
		Repo: &github.Repository{
			Name:  new("widgets"),
			Owner: &github.User{Login: new("acme")},
		},
	}
	if mutate != nil {
		mutate(ev)
	}
	payload, err := json.Marshal(ev)
	if err != nil {
		t.Fatal(err)
	}
	return signedRequest(t, testSecret, "pull_request_review", payload)
}

func reviewCommentEvent(t *testing.T, action string, mutate func(*github.PullRequestReviewCommentEvent)) *http.Request {
	t.Helper()
	ev := &github.PullRequestReviewCommentEvent{
		Action: new(action),
		Comment: &github.PullRequestComment{
			ID:                  new(int64(11)),
			PullRequestReviewID: new(int64(7)),
			Body:                new("also this"),
			Path:                new("x.go"),
			Line:                new(3),
			User:                &github.User{Login: new("bob")},
		},
		PullRequest: &github.PullRequest{Number: new(42)},
		Repo: &github.Repository{
			Name:  new("widgets"),
			Owner: &github.User{Login: new("acme")},
		},
	}
	if mutate != nil {
		mutate(ev)
	}
	payload, err := json.Marshal(ev)
	if err != nil {
		t.Fatal(err)
	}
	return signedRequest(t, testSecret, "pull_request_review_comment", payload)
}

func serveReaction(t *testing.T, r *Receiver, req *http.Request) (*httptest.ResponseRecorder, []workintake.ReviewCommentEnvelope) {
	t.Helper()
	var published []workintake.ReviewCommentEnvelope
	r.reactionPublish = func(_ context.Context, e workintake.ReviewCommentEnvelope) error {
		published = append(published, e)
		return nil
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec, published
}

func newReactionReceiver() *Receiver {
	return New(testSecret, "label", testLabel, testBot, nil, nil, nil)
}

func TestReceiverPublishesASubmittedReviewAsAReaction(t *testing.T) {
	r := newReactionReceiver()
	rec, published := serveReaction(t, r, reviewEvent(t, "submitted", nil))

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", rec.Code)
	}
	if len(published) != 1 {
		t.Fatalf("published = %d, want 1", len(published))
	}
	e := published[0]
	if e.Kind != workintake.ReviewReactionReview || e.ReviewID != 7 || e.Owner != "acme" || e.Repo != "widgets" || e.PRNumber != 42 {
		t.Errorf("reaction = %+v, want review 7 on acme/widgets#42", e)
	}
	if e.State != "requested_changes" || e.Body != "please fix the loop" || e.Author != "alice" {
		t.Errorf("reaction content = %+v, want the review's own state/body/author", e)
	}
	// The idempotency key is kinded (decision 3): a review ID and a comment
	// ID come from independent forge sequences and must never collide.
	if e.IdempotencyKey() == "" {
		t.Error("reaction carried no idempotency key")
	}
}

func TestReceiverPublishesACreatedCommentAsAReaction(t *testing.T) {
	r := newReactionReceiver()
	rec, published := serveReaction(t, r, reviewCommentEvent(t, "created", nil))

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", rec.Code)
	}
	if len(published) != 1 {
		t.Fatalf("published = %d, want 1", len(published))
	}
	e := published[0]
	if e.Kind != workintake.ReviewReactionComment || e.CommentID != 11 || e.ReviewID != 7 {
		t.Errorf("reaction = %+v, want comment 11 collected under review 7", e)
	}
}

func TestReceiverIgnoresNonActionReviewActivity(t *testing.T) {
	tests := []struct {
		name   string
		kind   string
		action string
	}{
		{"dismissed review", "review", "dismissed"},
		{"edited review", "review", "edited"},
		{"deleted review", "review", "deleted"},
		{"edited comment", "comment", "edited"},
		{"deleted comment", "comment", "deleted"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := newReactionReceiver()
			var req *http.Request
			if tt.kind == "review" {
				req = reviewEvent(t, tt.action, nil)
			} else {
				req = reviewCommentEvent(t, tt.action, nil)
			}
			rec, published := serveReaction(t, r, req)
			if rec.Code != http.StatusAccepted {
				t.Fatalf("status = %d, want 202", rec.Code)
			}
			if len(published) != 0 {
				t.Errorf("published %d reactions for action %q", len(published), tt.action)
			}
		})
	}
}

func TestReceiverReactionWithoutTheCapabilityIsUntouched(t *testing.T) {
	// A receiver composed without the reaction publisher (nil) must not
	// panic or publish when a review event arrives: the reaction path is an
	// optional capability, the issue path is the receiver's core contract.
	r := New(testSecret, "label", testLabel, testBot, nil, nil, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, reviewEvent(t, "submitted", nil))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", rec.Code)
	}
}

func TestReceiverRateLimitsReactionDeliveries(t *testing.T) {
	r := newReactionReceiver()
	// Drive the limiter to its per-window ceiling with one remote, then
	// assert the next reaction from that remote is refused with 429 while a
	// different remote still passes.
	remote := "10.0.0.1:1234"
	for i := range reactionRateLimit {
		req := reviewEvent(t, "submitted", nil)
		req.RemoteAddr = remote
		rec, _ := serveReaction(t, r, req)
		if rec.Code != http.StatusAccepted {
			t.Fatalf("delivery %d within the window: status = %d, want 202", i+1, rec.Code)
		}
	}
	req := reviewEvent(t, "submitted", nil)
	req.RemoteAddr = remote
	rec, published := serveReaction(t, r, req)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("over-limit status = %d, want 429", rec.Code)
	}
	if len(published) != 0 {
		t.Errorf("over-limit delivery published %d reactions", len(published))
	}

	otherReq := reviewEvent(t, "submitted", nil)
	otherReq.RemoteAddr = "10.0.0.2:1234"
	rec, published = serveReaction(t, r, otherReq)
	if rec.Code != http.StatusAccepted || len(published) != 1 {
		t.Errorf("different remote blocked: status = %d, published = %d", rec.Code, len(published))
	}
}
