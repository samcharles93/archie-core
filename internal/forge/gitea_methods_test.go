package forge

import (
	"net/http"
	"testing"
)

func TestGiteaAssignedIssuesExcludesPRs(t *testing.T) {
	c, mux := newTestGiteaClient(t)
	mux.HandleFunc("GET /api/v1/repos/o/r/issues", func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("assigned_by"); got != "archie-bot" {
			t.Errorf("assigned_by = %q", got)
		}
		writeJSON(t, w, []map[string]any{
			{"number": 1, "title": "real issue", "body": "b", "labels": []map[string]any{{"name": "bug"}}},
			{"number": 2, "title": "a pr", "pull_request": map[string]any{"url": "x"}},
		})
	})

	issues, err := c.AssignedIssues(t.Context(), "o", "r", "archie-bot")
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) != 1 || issues[0].Number != 1 || issues[0].Title != "real issue" || len(issues[0].Labels) != 1 || issues[0].Labels[0] != "bug" {
		t.Fatalf("issues = %+v", issues)
	}
}

func TestGiteaIssuesWithLabelExcludesPRs(t *testing.T) {
	c, mux := newTestGiteaClient(t)
	mux.HandleFunc("GET /api/v1/repos/o/r/issues", func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("labels"); got != "widget" {
			t.Errorf("labels = %q", got)
		}
		writeJSON(t, w, []map[string]any{
			{"number": 3, "title": "labelled", "body": "b"},
		})
	})

	issues, err := c.IssuesWithLabel(t.Context(), "o", "r", "widget")
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) != 1 || issues[0].Number != 3 {
		t.Fatalf("issues = %+v", issues)
	}
}

func TestGiteaAssignedIssuesError(t *testing.T) {
	c, mux := newTestGiteaClient(t)
	mux.HandleFunc("GET /api/v1/repos/o/r/issues", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})

	if _, err := c.AssignedIssues(t.Context(), "o", "r", "archie-bot"); err == nil {
		t.Fatal("AssignedIssues() error = nil, want error")
	}
}

func TestGiteaComment(t *testing.T) {
	c, mux := newTestGiteaClient(t)
	mux.HandleFunc("POST /api/v1/repos/o/r/issues/5/comments", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, map[string]any{"id": 42})
	})

	id, err := c.Comment(t.Context(), "o", "r", 5, "hello")
	if err != nil {
		t.Fatal(err)
	}
	if id != 42 {
		t.Fatalf("id = %d, want 42", id)
	}
}

func TestGiteaRepliesAfterExcludesOldAndSelf(t *testing.T) {
	c, mux := newTestGiteaClient(t)
	mux.HandleFunc("GET /api/v1/repos/o/r/issues/5/comments", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, []map[string]any{
			{"id": 1, "body": "old", "user": map[string]any{"login": "someone"}},
			{"id": 3, "body": "mine", "user": map[string]any{"login": "archie-bot"}},
			{"id": 4, "body": "new reply", "user": map[string]any{"login": "someone"}},
		})
	})

	replies, err := c.RepliesAfter(t.Context(), "o", "r", 5, 2, "archie-bot")
	if err != nil {
		t.Fatal(err)
	}
	if len(replies) != 1 || replies[0].ID != 4 || replies[0].Body != "new reply" {
		t.Fatalf("replies = %+v", replies)
	}
}

func TestGiteaCreatePR(t *testing.T) {
	c, mux := newTestGiteaClient(t)
	mux.HandleFunc("POST /api/v1/repos/o/r/pulls", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, map[string]any{"number": 7})
	})

	num, err := c.CreatePR(t.Context(), "o", "r", "title", "head", "base", "body")
	if err != nil {
		t.Fatal(err)
	}
	if num != 7 {
		t.Fatalf("num = %d, want 7", num)
	}
}

func TestGiteaCreatePRError(t *testing.T) {
	c, mux := newTestGiteaClient(t)
	mux.HandleFunc("POST /api/v1/repos/o/r/pulls", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
	})

	if _, err := c.CreatePR(t.Context(), "o", "r", "title", "head", "base", "body"); err == nil {
		t.Fatal("CreatePR() error = nil, want error")
	}
}

func TestGiteaPRState(t *testing.T) {
	tests := []struct {
		name    string
		status  int
		body    map[string]any
		want    string
		wantErr bool
	}{
		{name: "open", status: http.StatusOK, body: map[string]any{"number": 1, "state": "open", "merged": false}, want: "open"},
		{name: "merged", status: http.StatusOK, body: map[string]any{"number": 1, "state": "closed", "merged": true}, want: "merged"},
		{name: "not found treated as closed", status: http.StatusNotFound, body: map[string]any{"message": "not found"}, want: "closed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, mux := newTestGiteaClient(t)
			mux.HandleFunc("GET /api/v1/repos/o/r/pulls/1", func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.status)
				if tt.body != nil {
					writeJSON(t, w, tt.body)
				}
			})

			state, err := c.PRState(t.Context(), "o", "r", 1)
			if tt.wantErr {
				if err == nil {
					t.Fatal("PRState() error = nil, want error")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if state != tt.want {
				t.Fatalf("state = %q, want %q", state, tt.want)
			}
		})
	}
}

func TestGiteaCloseIssueWithComment(t *testing.T) {
	c, mux := newTestGiteaClient(t)
	var commented, closed bool
	mux.HandleFunc("POST /api/v1/repos/o/r/issues/9/comments", func(w http.ResponseWriter, r *http.Request) {
		commented = true
		writeJSON(t, w, map[string]any{"id": 1})
	})
	mux.HandleFunc("PATCH /api/v1/repos/o/r/issues/9", func(w http.ResponseWriter, r *http.Request) {
		closed = true
		writeJSON(t, w, map[string]any{"number": 9, "state": "closed"})
	})

	if err := c.CloseIssue(t.Context(), "o", "r", 9, "final comment"); err != nil {
		t.Fatal(err)
	}
	if !commented || !closed {
		t.Fatalf("commented=%t closed=%t, want both true", commented, closed)
	}
}

func TestGiteaCloseIssueWithoutComment(t *testing.T) {
	c, mux := newTestGiteaClient(t)
	var commented bool
	mux.HandleFunc("POST /api/v1/repos/o/r/issues/9/comments", func(w http.ResponseWriter, r *http.Request) {
		commented = true
	})
	mux.HandleFunc("PATCH /api/v1/repos/o/r/issues/9", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, map[string]any{"number": 9, "state": "closed"})
	})

	if err := c.CloseIssue(t.Context(), "o", "r", 9, ""); err != nil {
		t.Fatal(err)
	}
	if commented {
		t.Fatal("commented = true, want false when comment is empty")
	}
}

func TestGiteaCreateIssue(t *testing.T) {
	c, mux := newTestGiteaClient(t)
	mux.HandleFunc("POST /api/v1/repos/o/r/issues", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, map[string]any{"number": 11})
	})

	num, err := c.CreateIssue(t.Context(), "o", "r", "title", "body", nil)
	if err != nil {
		t.Fatal(err)
	}
	if num != 11 {
		t.Fatalf("num = %d, want 11", num)
	}
}

func TestGiteaReact(t *testing.T) {
	c, mux := newTestGiteaClient(t)
	var got string
	mux.HandleFunc("POST /api/v1/repos/o/r/issues/1/reactions", func(w http.ResponseWriter, r *http.Request) {
		got = "called"
		writeJSON(t, w, map[string]any{"content": "+1"})
	})

	if err := c.React(t.Context(), "o", "r", 1, "+1"); err != nil {
		t.Fatal(err)
	}
	if got != "called" {
		t.Fatal("reaction endpoint was not called")
	}
}

func TestGiteaAcceptInvitationsIsNoop(t *testing.T) {
	c, _ := newTestGiteaClient(t)
	if err := c.AcceptInvitations(t.Context()); err != nil {
		t.Fatalf("AcceptInvitations() = %v, want nil", err)
	}
}

func TestGiteaVerifyPush(t *testing.T) {
	tests := []struct {
		name    string
		status  int
		body    map[string]any
		wantErr bool
	}{
		{name: "has push", status: http.StatusOK, body: map[string]any{"permissions": map[string]any{"push": true}}},
		{name: "no push", status: http.StatusOK, body: map[string]any{"permissions": map[string]any{"push": false}}, wantErr: true},
		{name: "not found", status: http.StatusNotFound, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, mux := newTestGiteaClient(t)
			mux.HandleFunc("GET /api/v1/repos/o/r", func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.status)
				if tt.body != nil {
					writeJSON(t, w, tt.body)
				}
			})

			err := c.VerifyPush(t.Context(), "o", "r")
			if tt.wantErr && err == nil {
				t.Fatal("VerifyPush() error = nil, want error")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("VerifyPush() error = %v, want nil", err)
			}
		})
	}
}

func TestGiteaLinkBranch(t *testing.T) {
	c, mux := newTestGiteaClient(t)
	mux.HandleFunc("POST /api/v1/repos/o/r/issues/3/refs", func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "token token" {
			t.Errorf("Authorization = %q", got)
		}
		w.WriteHeader(http.StatusOK)
	})

	if err := c.LinkBranch(t.Context(), "o", "r", 3, "feature-branch"); err != nil {
		t.Fatal(err)
	}
}

func TestGiteaLinkBranchError(t *testing.T) {
	c, mux := newTestGiteaClient(t)
	mux.HandleFunc("POST /api/v1/repos/o/r/issues/3/refs", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})

	if err := c.LinkBranch(t.Context(), "o", "r", 3, "feature-branch"); err == nil {
		t.Fatal("LinkBranch() error = nil, want error")
	}
}

func TestGiteaSetStateLabelSwapsLabels(t *testing.T) {
	c, mux := newTestGiteaClient(t)
	mux.HandleFunc("GET /api/v1/repos/o/r/issues/4", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, map[string]any{
			"number": 4,
			"labels": []map[string]any{
				{"id": 1, "name": "archie:queued"},
				{"id": 2, "name": "keep-me"},
			},
		})
	})
	var deletedID int64
	mux.HandleFunc("DELETE /api/v1/repos/o/r/issues/4/labels/1", func(w http.ResponseWriter, r *http.Request) {
		deletedID = 1
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("GET /api/v1/repos/o/r/labels", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, []map[string]any{{"id": 9, "name": "archie:working"}})
	})
	var added []int64
	mux.HandleFunc("POST /api/v1/repos/o/r/issues/4/labels", func(w http.ResponseWriter, r *http.Request) {
		added = []int64{9}
		writeJSON(t, w, []map[string]any{{"id": 9, "name": "archie:working"}})
	})

	known := []string{"archie:queued", "archie:working", "archie:waiting"}
	c.SetStateLabel(t.Context(), "o", "r", 4, "archie:working", known)

	if deletedID != 1 {
		t.Errorf("deletedID = %d, want 1 (archie:queued removed)", deletedID)
	}
	if len(added) != 1 || added[0] != 9 {
		t.Errorf("added = %v, want [9]", added)
	}
}

func TestGiteaSetStateLabelAlreadyPresentIsNoop(t *testing.T) {
	c, mux := newTestGiteaClient(t)
	mux.HandleFunc("GET /api/v1/repos/o/r/issues/4", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, map[string]any{
			"number": 4,
			"labels": []map[string]any{{"id": 9, "name": "archie:working"}},
		})
	})
	called := false
	mux.HandleFunc("POST /api/v1/repos/o/r/issues/4/labels", func(w http.ResponseWriter, r *http.Request) {
		called = true
	})

	c.SetStateLabel(t.Context(), "o", "r", 4, "archie:working", []string{"archie:working"})

	if called {
		t.Fatal("AddIssueLabels was called, want no-op when label already present")
	}
}

func TestGiteaSetStateLabelEmptyClearsAll(t *testing.T) {
	c, mux := newTestGiteaClient(t)
	mux.HandleFunc("GET /api/v1/repos/o/r/issues/4", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, map[string]any{
			"number": 4,
			"labels": []map[string]any{{"id": 1, "name": "archie:queued"}},
		})
	})
	deleted := false
	mux.HandleFunc("DELETE /api/v1/repos/o/r/issues/4/labels/1", func(w http.ResponseWriter, r *http.Request) {
		deleted = true
		w.WriteHeader(http.StatusOK)
	})
	added := false
	mux.HandleFunc("POST /api/v1/repos/o/r/issues/4/labels", func(w http.ResponseWriter, r *http.Request) {
		added = true
	})

	c.SetStateLabel(t.Context(), "o", "r", 4, "", []string{"archie:queued"})

	if !deleted {
		t.Error("deleted = false, want true (archie:queued removed)")
	}
	if added {
		t.Error("added = true, want false (empty label clears all)")
	}
}

func TestLabelNamesGitea(t *testing.T) {
	names := labelNamesGitea(nil)
	if names != nil {
		t.Fatalf("labelNamesGitea(nil) = %v, want nil", names)
	}
}
