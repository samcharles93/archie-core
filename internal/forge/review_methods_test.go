package forge

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestGitHubListReviews(t *testing.T) {
	c, mux := newTestClient(t)
	mux.HandleFunc("GET /repos/o/r/pulls/7/reviews", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, []map[string]any{
			{"id": 1, "state": "APPROVED", "user": map[string]any{"login": "alice"}},
			{"id": 2, "state": "CHANGES_REQUESTED", "user": map[string]any{"login": "bob"}},
		})
	})

	reviews, err := c.ListReviews(t.Context(), "o", "r", 7, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(reviews) != 2 {
		t.Fatalf("reviews = %d, want 2", len(reviews))
	}
	if reviews[0].ID != 1 || reviews[0].Author != "alice" || reviews[0].State != ReviewStateApproved {
		t.Errorf("reviews[0] = %+v", reviews[0])
	}
	if reviews[1].State != ReviewStateRequestedChanges {
		t.Errorf("reviews[1].State = %q, want %q", reviews[1].State, ReviewStateRequestedChanges)
	}

	// sinceID cursor excludes already-seen reviews.
	reviews, err = c.ListReviews(t.Context(), "o", "r", 7, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(reviews) != 1 || reviews[0].ID != 2 {
		t.Fatalf("sinceID-filtered reviews = %+v, want only id 2", reviews)
	}
}

func TestGitHubListReviewComments(t *testing.T) {
	c, mux := newTestClient(t)
	mux.HandleFunc("GET /repos/o/r/pulls/7/comments", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, []map[string]any{
			{"id": 10, "body": "nil deref here", "path": "a.go", "original_line": 42, "in_reply_to_id": 5, "user": map[string]any{"login": "alice"}},
		})
	})

	comments, err := c.ListReviewComments(t.Context(), "o", "r", 7, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(comments) != 1 {
		t.Fatalf("comments = %d, want 1", len(comments))
	}
	got := comments[0]
	if got.ID != 10 || got.Author != "alice" || got.Body != "nil deref here" ||
		got.Path != "a.go" || got.Line != 42 || got.InReplyTo != 5 {
		t.Errorf("comment = %+v", got)
	}
}

func TestGitHubReplyToReview(t *testing.T) {
	c, mux := newTestClient(t)
	var gotBody map[string]any
	mux.HandleFunc("POST /repos/o/r/pulls/7/comments", func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatal(err)
		}
		writeJSON(t, w, map[string]any{"id": 99})
	})

	if err := c.ReplyToReview(t.Context(), "o", "r", 7, 42, "fixed"); err != nil {
		t.Fatal(err)
	}
	if gotBody["body"] != "fixed" || gotBody["in_reply_to"] != float64(42) {
		t.Errorf("request body = %v, want body=fixed in_reply_to=42", gotBody)
	}
}

func TestGiteaListReviews(t *testing.T) {
	c, mux := newTestGiteaClient(t)
	mux.HandleFunc("GET /api/v1/repos/o/r/pulls/7/reviews", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, []map[string]any{
			{"id": 1, "state": "APPROVED", "user": map[string]any{"login": "alice"}},
			{"id": 2, "state": "REQUEST_CHANGES", "user": map[string]any{"login": "bob"}},
		})
	})

	reviews, err := c.ListReviews(t.Context(), "o", "r", 7, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(reviews) != 2 {
		t.Fatalf("reviews = %d, want 2", len(reviews))
	}
	if reviews[0].State != ReviewStateApproved || reviews[0].Author != "alice" {
		t.Errorf("reviews[0] = %+v", reviews[0])
	}
	if reviews[1].State != ReviewStateRequestedChanges {
		t.Errorf("reviews[1].State = %q, want %q", reviews[1].State, ReviewStateRequestedChanges)
	}
}

func TestGiteaListReviewComments(t *testing.T) {
	c, mux := newTestGiteaClient(t)
	mux.HandleFunc("GET /api/v1/repos/o/r/pulls/7/reviews", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, []map[string]any{{"id": 3, "state": "COMMENT", "user": map[string]any{"login": "alice"}}})
	})
	mux.HandleFunc("GET /api/v1/repos/o/r/pulls/7/reviews/3/comments", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, []map[string]any{
			{"id": 20, "body": "race here", "path": "b.go", "position": 12, "user": map[string]any{"login": "bob"}},
		})
	})

	comments, err := c.ListReviewComments(t.Context(), "o", "r", 7, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(comments) != 1 {
		t.Fatalf("comments = %d, want 1", len(comments))
	}
	got := comments[0]
	if got.ID != 20 || got.Author != "bob" || got.Body != "race here" || got.Path != "b.go" || got.Line != 12 {
		t.Errorf("comment = %+v", got)
	}

	// sinceID cursor excludes already-seen comments.
	comments, err = c.ListReviewComments(t.Context(), "o", "r", 7, 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(comments) != 0 {
		t.Fatalf("sinceID-filtered comments = %+v, want empty", comments)
	}
}

func TestGiteaReplyToReview(t *testing.T) {
	c, mux := newTestGiteaClient(t)
	var got map[string]any
	mux.HandleFunc("POST /api/v1/repos/o/r/pulls/7/comments/42/replies", func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatal(err)
		}
		writeJSON(t, w, map[string]any{"id": 99})
	})

	if err := c.ReplyToReview(t.Context(), "o", "r", 7, 42, "fixed"); err != nil {
		t.Fatal(err)
	}
	if got["body"] != "fixed" {
		t.Errorf("request body = %v, want body=fixed", got)
	}
}

func TestNormalizeReviewState(t *testing.T) {
	tests := map[string]string{
		"APPROVED":          ReviewStateApproved,
		"CHANGES_REQUESTED": ReviewStateRequestedChanges,
		"COMMENTED":         ReviewStateCommented,
		"DISMISSED":         ReviewStateDismissed,
		"SOMETHING_NEW":     "something_new",
	}
	for in, want := range tests {
		if got := normalizeReviewState(in); got != want {
			t.Errorf("normalizeReviewState(%q) = %q, want %q", in, got, want)
		}
	}
}
