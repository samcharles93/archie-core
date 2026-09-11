package forge

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestGitHubCreateReviewComments(t *testing.T) {
	c, mux := newTestClient(t)
	var got []map[string]any
	mux.HandleFunc("POST /repos/o/r/pulls/7/comments", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		got = append(got, body)
		writeJSON(t, w, map[string]any{"id": 1})
	})

	comments := []InlineReviewComment{
		{Path: "a.go", Line: 12, Body: "nil deref"},
		{Path: "b.go", Line: 3, Body: "```suggestion\nfixed\n```"},
	}
	if err := c.CreateReviewComments(t.Context(), "o", "r", 7, "headsha", comments); err != nil {
		t.Fatal(err)
	}

	if len(got) != 2 {
		t.Fatalf("posted %d comments, want 2", len(got))
	}
	if got[0]["path"] != "a.go" || got[0]["line"] != float64(12) || got[0]["side"] != "RIGHT" || got[0]["commit_id"] != "headsha" {
		t.Errorf("comment[0] = %v, want path=a.go line=12 side=RIGHT commit_id=headsha", got[0])
	}
	if got[1]["body"] != "```suggestion\nfixed\n```" {
		t.Errorf("comment[1].body = %v, want the suggestion block preserved", got[1]["body"])
	}
}

func TestGitHubCreateReviewCommentsError(t *testing.T) {
	c, mux := newTestClient(t)
	mux.HandleFunc("POST /repos/o/r/pulls/7/comments", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
	})

	err := c.CreateReviewComments(t.Context(), "o", "r", 7, "headsha", []InlineReviewComment{{Path: "a.go", Line: 1, Body: "x"}})
	if err == nil {
		t.Fatal("CreateReviewComments error = nil, want the forge failure surfaced")
	}
}

func TestGiteaCreateReviewComments(t *testing.T) {
	c, mux := newTestGiteaClient(t)
	var got map[string]any
	mux.HandleFunc("POST /api/v1/repos/o/r/pulls/7/reviews", func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatal(err)
		}
		writeJSON(t, w, map[string]any{"id": 1})
	})

	comments := []InlineReviewComment{
		{Path: "a.go", Line: 12, Body: "nil deref"},
		{Path: "b.go", Line: 3, Body: "race"},
	}
	if err := c.CreateReviewComments(t.Context(), "o", "r", 7, "headsha", comments); err != nil {
		t.Fatal(err)
	}

	// Gitea carries inline comments on one COMMENT-state review.
	if got["event"] != "COMMENT" || got["commit_id"] != "headsha" {
		t.Errorf("review = %v, want event=COMMENT commit_id=headsha", got)
	}
	posted, _ := got["comments"].([]any)
	if len(posted) != 2 {
		t.Fatalf("review carried %d comments, want 2", len(posted))
	}
	first, _ := posted[0].(map[string]any)
	if first["path"] != "a.go" || first["new_position"] != float64(12) {
		t.Errorf("comments[0] = %v, want path=a.go new_position=12", first)
	}
}
