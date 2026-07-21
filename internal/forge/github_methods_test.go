package forge

import (
	"encoding/json"
	"net/http"
	"testing"
)

func writeJSON(t *testing.T, w http.ResponseWriter, v any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		t.Fatal(err)
	}
}

func TestAssignedIssuesExcludesPRs(t *testing.T) {
	c, mux := newTestClient(t)
	mux.HandleFunc("GET /repos/o/r/issues", func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("assignee"); got != "archie-bot" {
			t.Errorf("assignee = %q", got)
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

func TestIssuesWithLabelExcludesPRs(t *testing.T) {
	c, mux := newTestClient(t)
	mux.HandleFunc("GET /repos/o/r/issues", func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("labels"); got != "archie" {
			t.Errorf("labels = %q", got)
		}
		writeJSON(t, w, []map[string]any{
			{"number": 3, "title": "labelled", "body": "b"},
		})
	})

	issues, err := c.IssuesWithLabel(t.Context(), "o", "r", "archie")
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) != 1 || issues[0].Number != 3 {
		t.Fatalf("issues = %+v", issues)
	}
}

func TestComment(t *testing.T) {
	c, mux := newTestClient(t)
	mux.HandleFunc("POST /repos/o/r/issues/5/comments", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["body"] != "hello" {
			t.Errorf("comment body = %q", body["body"])
		}
		writeJSON(t, w, map[string]any{"id": 42})
	})

	id, err := c.Comment(t.Context(), "o", "r", 5, "hello")
	if err != nil || id != 42 {
		t.Fatalf("Comment = (%d, %v)", id, err)
	}
}

func TestRepliesAfterExcludesBotAndOldComments(t *testing.T) {
	c, mux := newTestClient(t)
	mux.HandleFunc("GET /repos/o/r/issues/5/comments", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, []map[string]any{
			{"id": 1, "user": map[string]any{"login": "human"}, "body": "old, ignored"},
			{"id": 2, "user": map[string]any{"login": "archie-bot"}, "body": "bot comment, excluded"},
			{"id": 3, "user": map[string]any{"login": "human"}, "body": "the reply"},
		})
	})

	replies, err := c.RepliesAfter(t.Context(), "o", "r", 5, 1, "archie-bot")
	if err != nil {
		t.Fatal(err)
	}
	if len(replies) != 1 || replies[0].ID != 3 || replies[0].Body != "the reply" || replies[0].User != "human" {
		t.Fatalf("replies = %+v", replies)
	}
}

func TestCreatePR(t *testing.T) {
	c, mux := newTestClient(t)
	mux.HandleFunc("POST /repos/o/r/pulls", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, map[string]any{"number": 9})
	})

	n, err := c.CreatePR(t.Context(), "o", "r", "title", "head", "base", "body")
	if err != nil || n != 9 {
		t.Fatalf("CreatePR = (%d, %v)", n, err)
	}
}

func TestPRState(t *testing.T) {
	tests := []struct {
		name string
		json map[string]any
		want string
	}{
		{"merged", map[string]any{"merged": true, "state": "closed"}, "merged"},
		{"open", map[string]any{"merged": false, "state": "open"}, "open"},
		{"closed", map[string]any{"merged": false, "state": "closed"}, "closed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, mux := newTestClient(t)
			mux.HandleFunc("GET /repos/o/r/pulls/7", func(w http.ResponseWriter, r *http.Request) {
				writeJSON(t, w, tt.json)
			})
			got, err := c.PRState(t.Context(), "o", "r", 7)
			if err != nil || got != tt.want {
				t.Fatalf("PRState = (%q, %v), want %q", got, err, tt.want)
			}
		})
	}
}

func TestCloseIssueWithComment(t *testing.T) {
	c, mux := newTestClient(t)
	var commented, edited bool
	mux.HandleFunc("POST /repos/o/r/issues/5/comments", func(w http.ResponseWriter, r *http.Request) {
		commented = true
		writeJSON(t, w, map[string]any{"id": 1})
	})
	mux.HandleFunc("PATCH /repos/o/r/issues/5", func(w http.ResponseWriter, r *http.Request) {
		edited = true
		var body map[string]string
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["state"] != "closed" {
			t.Errorf("state = %q", body["state"])
		}
		writeJSON(t, w, map[string]any{"number": 5, "state": "closed"})
	})

	if err := c.CloseIssue(t.Context(), "o", "r", 5, "won't do"); err != nil {
		t.Fatal(err)
	}
	if !commented || !edited {
		t.Fatalf("commented=%v edited=%v", commented, edited)
	}
}

func TestCloseIssueWithoutComment(t *testing.T) {
	c, mux := newTestClient(t)
	mux.HandleFunc("POST /repos/o/r/issues/5/comments", func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("should not comment when comment is empty")
	})
	mux.HandleFunc("PATCH /repos/o/r/issues/5", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, map[string]any{"number": 5, "state": "closed"})
	})

	if err := c.CloseIssue(t.Context(), "o", "r", 5, ""); err != nil {
		t.Fatal(err)
	}
}

func TestReact(t *testing.T) {
	c, mux := newTestClient(t)
	mux.HandleFunc("POST /repos/o/r/issues/5/reactions", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["content"] != "+1" {
			t.Errorf("content = %q", body["content"])
		}
		writeJSON(t, w, map[string]any{"id": 1, "content": "+1"})
	})

	if err := c.React(t.Context(), "o", "r", 5, "+1"); err != nil {
		t.Fatal(err)
	}
}

func TestSetStateLabelSwapsLabels(t *testing.T) {
	c, mux := newTestClient(t)
	var removed, added, created []string

	mux.HandleFunc("GET /repos/o/r/issues/5", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, map[string]any{
			"number": 5,
			"labels": []map[string]any{{"name": "archie:queued"}, {"name": "bug"}},
		})
	})
	mux.HandleFunc("DELETE /repos/o/r/issues/5/labels/archie:queued", func(w http.ResponseWriter, r *http.Request) {
		removed = append(removed, "archie:queued")
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("POST /repos/o/r/labels", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		created = append(created, body["name"])
		writeJSON(t, w, map[string]any{"name": body["name"], "color": body["color"]})
	})
	mux.HandleFunc("POST /repos/o/r/issues/5/labels", func(w http.ResponseWriter, r *http.Request) {
		var body []string
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		added = append(added, body...)
		writeJSON(t, w, []map[string]any{})
	})

	known := []string{"archie:queued", "archie:working", "archie:waiting", "archie:pr", "archie:parked"}
	c.SetStateLabel(t.Context(), "o", "r", 5, "archie:working", known)

	if len(removed) != 1 || removed[0] != "archie:queued" {
		t.Fatalf("removed = %v", removed)
	}
	if len(created) != 1 || created[0] != "archie:working" {
		t.Fatalf("created = %v", created)
	}
	if len(added) != 1 || added[0] != "archie:working" {
		t.Fatalf("added = %v", added)
	}

	// Second call for the same label reuses the ensured-labels cache and
	// skips label creation.
	mux.HandleFunc("GET /repos/o/r/issues/6", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, map[string]any{"number": 6, "labels": []map[string]any{}})
	})
	mux.HandleFunc("POST /repos/o/r/issues/6/labels", func(w http.ResponseWriter, r *http.Request) {
		var body []string
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		added = append(added, body...)
		writeJSON(t, w, []map[string]any{})
	})
	c.SetStateLabel(t.Context(), "o", "r", 6, "archie:working", known)
	if len(created) != 1 {
		t.Fatalf("expected label creation to be cached, created = %v", created)
	}
}

func TestSetStateLabelAlreadyPresentIsNoop(t *testing.T) {
	c, mux := newTestClient(t)
	mux.HandleFunc("GET /repos/o/r/issues/5", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, map[string]any{
			"number": 5,
			"labels": []map[string]any{{"name": "archie:working"}},
		})
	})
	mux.HandleFunc("POST /repos/o/r/issues/5/labels", func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("should not add a label that's already present")
	})
	mux.HandleFunc("POST /repos/o/r/labels", func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("should not create a label that's already present")
	})

	c.SetStateLabel(t.Context(), "o", "r", 5, "archie:working", []string{"archie:working"})
}

func TestSetStateLabelEmptyClearsAll(t *testing.T) {
	c, mux := newTestClient(t)
	var removed []string
	mux.HandleFunc("GET /repos/o/r/issues/5", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, map[string]any{
			"number": 5,
			"labels": []map[string]any{{"name": "archie:working"}},
		})
	})
	mux.HandleFunc("DELETE /repos/o/r/issues/5/labels/archie:working", func(w http.ResponseWriter, r *http.Request) {
		removed = append(removed, "archie:working")
		w.WriteHeader(http.StatusOK)
	})

	c.SetStateLabel(t.Context(), "o", "r", 5, "", []string{"archie:working"})
	if len(removed) != 1 {
		t.Fatalf("removed = %v", removed)
	}
}

func TestVerifyPush(t *testing.T) {
	t.Run("has push", func(t *testing.T) {
		c, mux := newTestClient(t)
		mux.HandleFunc("GET /repos/o/r", func(w http.ResponseWriter, r *http.Request) {
			writeJSON(t, w, map[string]any{"permissions": map[string]bool{"push": true}})
		})
		if err := c.VerifyPush(t.Context(), "o", "r"); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("no push", func(t *testing.T) {
		c, mux := newTestClient(t)
		mux.HandleFunc("GET /repos/o/r", func(w http.ResponseWriter, r *http.Request) {
			writeJSON(t, w, map[string]any{"permissions": map[string]bool{"push": false}})
		})
		if err := c.VerifyPush(t.Context(), "o", "r"); err == nil {
			t.Fatal("expected error for missing push permission")
		}
	})

	t.Run("api error", func(t *testing.T) {
		c, mux := newTestClient(t)
		mux.HandleFunc("GET /repos/o/r", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		})
		if err := c.VerifyPush(t.Context(), "o", "r"); err == nil {
			t.Fatal("expected error for 404")
		}
	})
}

func TestAcceptInvitations(t *testing.T) {
	c, mux := newTestClient(t)
	var accepted []string
	mux.HandleFunc("GET /user/repository_invitations", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, []map[string]any{
			{"id": 1, "repository": map[string]any{"full_name": "o/ok"}},
			{"id": 2, "repository": map[string]any{"full_name": "o/fails"}},
		})
	})
	mux.HandleFunc("PATCH /user/repository_invitations/1", func(w http.ResponseWriter, r *http.Request) {
		accepted = append(accepted, "1")
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("PATCH /user/repository_invitations/2", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})

	// A single failing invitation must not stop the others from being
	// processed or fail the whole call.
	if err := c.AcceptInvitations(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(accepted) != 1 || accepted[0] != "1" {
		t.Fatalf("accepted = %v", accepted)
	}
}

func TestAcceptInvitationsListError(t *testing.T) {
	c, mux := newTestClient(t)
	mux.HandleFunc("GET /user/repository_invitations", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	if err := c.AcceptInvitations(t.Context()); err == nil {
		t.Fatal("expected error")
	}
}
