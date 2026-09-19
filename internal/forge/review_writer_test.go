package forge

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestGitHubCreateReviewComments(t *testing.T) {
	c, mux := newTestClient(t)
	// The head SHA is deliberately not "headsha": the comments are anchored to
	// the revision the forge reports for the pull request, never to a value the
	// caller supplied. The caller's reviewed revision gates the posting, it is
	// not the anchor itself.
	mux.HandleFunc("GET /repos/o/r/pulls/7", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, map[string]any{
			"number": 7, "title": "Add widget", "state": "open",
			"head": map[string]any{"ref": "feature/widget", "sha": "pulled-head-sha"},
			"base": map[string]any{"ref": "main", "sha": "basesha"},
		})
	})
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
	if err := c.CreateReviewComments(t.Context(), "o", "r", 7, "pulled-head-sha", comments); err != nil {
		t.Fatal(err)
	}

	if len(got) != 2 {
		t.Fatalf("posted %d comments, want 2", len(got))
	}
	if got[0]["path"] != "a.go" || got[0]["line"] != float64(12) || got[0]["side"] != "RIGHT" {
		t.Errorf("comment[0] = %v, want path=a.go line=12 side=RIGHT", got[0])
	}
	if got[0]["commit_id"] != "pulled-head-sha" {
		t.Errorf("comment[0].commit_id = %v, want the head pull requests.Get reported", got[0]["commit_id"])
	}
	if got[1]["body"] != "```suggestion\nfixed\n```" {
		t.Errorf("comment[1].body = %v, want the suggestion block preserved", got[1]["body"])
	}
}

// TestGitHubCreateReviewCommentsKeepsGoingAfterARefusal pins that one comment
// GitHub will not accept -- a line outside the diff, a file the push renamed --
// cannot silently drop the comments after it.
func TestGitHubCreateReviewCommentsKeepsGoingAfterARefusal(t *testing.T) {
	c, mux := newTestClient(t)
	mux.HandleFunc("GET /repos/o/r/pulls/7", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, map[string]any{
			"number": 7, "state": "open",
			"head": map[string]any{"ref": "f", "sha": "headsha"},
			"base": map[string]any{"ref": "main", "sha": "basesha"},
		})
	})
	attempts := 0
	mux.HandleFunc("POST /repos/o/r/pulls/7/comments", func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts == 1 {
			w.WriteHeader(http.StatusUnprocessableEntity)
			return
		}
		writeJSON(t, w, map[string]any{"id": 2})
	})

	err := c.CreateReviewComments(t.Context(), "o", "r", 7, "headsha", []InlineReviewComment{
		{Path: "a.go", Line: 1, Body: "refused"},
		{Path: "b.go", Line: 2, Body: "accepted"},
	})
	if err == nil {
		t.Error("CreateReviewComments error = nil, want the refused comment surfaced")
	}
	if attempts != 2 {
		t.Errorf("attempted %d comments, want 2: a refusal must not abandon the rest", attempts)
	}
}

// TestGitHubCreateReviewCommentsRefusesAnEmptyHeadSHA guards the anchor itself.
// An empty commit_id makes GitHub reject every comment with nothing that says
// why, so it fails before the first request instead.
func TestGitHubCreateReviewCommentsRefusesAnEmptyHeadSHA(t *testing.T) {
	c, mux := newTestClient(t)
	mux.HandleFunc("GET /repos/o/r/pulls/7", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, map[string]any{
			"number": 7, "state": "open",
			"head": map[string]any{"ref": "f"}, "base": map[string]any{"ref": "main", "sha": "basesha"},
		})
	})
	posted := false
	mux.HandleFunc("POST /repos/o/r/pulls/7/comments", func(w http.ResponseWriter, r *http.Request) {
		posted = true
		writeJSON(t, w, map[string]any{"id": 1})
	})

	err := c.CreateReviewComments(t.Context(), "o", "r", 7, "reviewed-sha", []InlineReviewComment{{Path: "a.go", Line: 1, Body: "x"}})
	if err == nil {
		t.Fatal("CreateReviewComments error = nil, want a missing-head-sha error")
	}
	if posted {
		t.Error("a comment was posted despite no head sha to anchor it to")
	}
}

// TestGitHubCreateReviewCommentsRefusesAMovedHead is the q9au drift guard. The
// line numbers in a finding describe one revision; if the pull request's head
// has moved since the review, GitHub anchors a line number against the CURRENT
// file and attaches the comment to whatever now occupies that line rather than
// failing. So the set is refused before the first comment is attempted, and the
// PR body remains the record.
func TestGitHubCreateReviewCommentsRefusesAMovedHead(t *testing.T) {
	c, mux := newTestClient(t)
	mux.HandleFunc("GET /repos/o/r/pulls/7", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, map[string]any{
			"number": 7, "state": "open",
			"head": map[string]any{"ref": "f", "sha": "moved-head-sha"},
			"base": map[string]any{"ref": "main", "sha": "basesha"},
		})
	})
	posted := false
	mux.HandleFunc("POST /repos/o/r/pulls/7/comments", func(w http.ResponseWriter, r *http.Request) {
		posted = true
		writeJSON(t, w, map[string]any{"id": 1})
	})

	err := c.CreateReviewComments(t.Context(), "o", "r", 7, "reviewed-sha", []InlineReviewComment{{Path: "a.go", Line: 12, Body: "nil deref"}})
	if err == nil {
		t.Fatal("CreateReviewComments error = nil, want a refusal for a head that moved off the reviewed revision")
	}
	if !strings.Contains(err.Error(), "reviewed-sha") || !strings.Contains(err.Error(), "moved-head-sha") {
		t.Errorf("error = %v, want it to name both revisions so an operator can tell what moved", err)
	}
	if posted {
		t.Error("a comment was anchored to a revision its line numbers were not measured on")
	}
}

func TestGiteaCreateReviewComments(t *testing.T) {
	c, mux := newTestGiteaClient(t)
	mux.HandleFunc("GET /api/v1/repos/o/r/pulls/7", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, map[string]any{
			"number": 7, "title": "Add widget", "state": "open", "merged": false,
			"head": map[string]any{"ref": "feature/widget", "sha": "pulled-head-sha"},
			"base": map[string]any{"ref": "main", "sha": "basesha"},
		})
	})
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
	if err := c.CreateReviewComments(t.Context(), "o", "r", 7, "pulled-head-sha", comments); err != nil {
		t.Fatal(err)
	}

	// Gitea carries inline comments on one COMMENT-state review.
	if got["event"] != "COMMENT" || got["commit_id"] != "pulled-head-sha" {
		t.Errorf("review = %v, want event=COMMENT and the reported head sha", got)
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

func TestGiteaCreateReviewCommentsRefusesAnEmptyHeadSHA(t *testing.T) {
	c, mux := newTestGiteaClient(t)
	mux.HandleFunc("GET /api/v1/repos/o/r/pulls/7", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, map[string]any{
			"number": 7, "state": "open", "merged": false,
			"head": map[string]any{"ref": "f"}, "base": map[string]any{"ref": "main", "sha": "basesha"},
		})
	})
	reviewed := false
	mux.HandleFunc("POST /api/v1/repos/o/r/pulls/7/reviews", func(w http.ResponseWriter, r *http.Request) {
		reviewed = true
		writeJSON(t, w, map[string]any{"id": 1})
	})

	err := c.CreateReviewComments(t.Context(), "o", "r", 7, "reviewed-sha", []InlineReviewComment{{Path: "a.go", Line: 1, Body: "x"}})
	if err == nil {
		t.Fatal("CreateReviewComments error = nil, want a missing-head-sha error")
	}
	if reviewed {
		t.Error("a review was submitted despite no head sha to anchor it to")
	}
}

// TestGiteaCreateReviewCommentsRefusesAMovedHead is the Gitea half of the q9au
// drift guard: the same refusal, reached before the review is submitted, because
// Gitea's inline comments live on that one review and a wrong line is applied
// with one click.
func TestGiteaCreateReviewCommentsRefusesAMovedHead(t *testing.T) {
	c, mux := newTestGiteaClient(t)
	mux.HandleFunc("GET /api/v1/repos/o/r/pulls/7", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, map[string]any{
			"number": 7, "state": "open", "merged": false,
			"head": map[string]any{"ref": "f", "sha": "moved-head-sha"},
			"base": map[string]any{"ref": "main", "sha": "basesha"},
		})
	})
	reviewed := false
	mux.HandleFunc("POST /api/v1/repos/o/r/pulls/7/reviews", func(w http.ResponseWriter, r *http.Request) {
		reviewed = true
		writeJSON(t, w, map[string]any{"id": 1})
	})

	err := c.CreateReviewComments(t.Context(), "o", "r", 7, "reviewed-sha", []InlineReviewComment{{Path: "a.go", Line: 12, Body: "nil deref"}})
	if err == nil {
		t.Fatal("CreateReviewComments error = nil, want a refusal for a head that moved off the reviewed revision")
	}
	if reviewed {
		t.Error("a review was submitted against a revision its line numbers were not measured on")
	}
}

// TestReviewCommentWriterIsOptional pins the degrade path the forgerpc server
// relies on: a forge that cannot post inline comments must not satisfy the
// interface, so the caller is told rather than handed a silent no-op.
func TestReviewCommentWriterIsOptional(t *testing.T) {
	if _, ok := any(&NoopForge{}).(ReviewCommentWriter); ok {
		t.Error("NoopForge satisfies ReviewCommentWriter; the capability must be absent, not faked")
	}
	for _, c := range []any{&GitHubClient{}, &GiteaClient{}} {
		if _, ok := c.(ReviewCommentWriter); !ok {
			t.Errorf("%T does not satisfy ReviewCommentWriter", c)
		}
	}
}

// Compile-time guard: the workflow stage reaches this through a Forger whose
// signature carries the domain's own comment value, so the conversion is the
// caller's. The interface carries the revision the caller MEASURED, never a
// commit to anchor to: the anchor stays a per-forge detail (GitHub pins
// commit_id, Gitea anchors the review), while the caller's revision is only ever
// compared against the head the forge reads for itself.
var _ = func(ctx context.Context, c ReviewCommentWriter) error {
	return c.CreateReviewComments(ctx, "o", "r", 1, "reviewed-sha", nil)
}
