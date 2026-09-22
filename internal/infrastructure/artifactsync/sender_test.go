package artifactsync

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func sampleArtifact() Artifact {
	return Artifact{
		Title:  "Review of PR #7",
		Source: "# Review\n\none finding, one check",
		Meta: Meta{
			Format:     "markdown",
			Category:   "pr-review",
			Visibility: "account-only",
			Egress:     "none",
			Actor:      "system:pr-reviewer",
			Origin:     "pr-review",
			RequestID:  "artifact:pr-review:acme/widget#7",
			Version:    "r1",
			SentAt:     time.Date(2026, 9, 22, 16, 45, 0, 0, time.UTC),
			Task:       &Task{Owner: "acme", Repo: "widget", Number: 7},
		},
	}
}

func TestPublishPostsTheContractBody(t *testing.T) {
	var gotPath, gotAuth string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotAuth = r.URL.Path, r.Header.Get("Authorization")
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &gotBody)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"ok":true,"id":"a_1","url":"https://offloaded.dev/collab?document=a_1"}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "secret-token")
	url, err := c.Publish(context.Background(), sampleArtifact())
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if url != "https://offloaded.dev/collab?document=a_1" {
		t.Fatalf("url = %q, want the editor deep link", url)
	}
	if gotPath != "/api/artifacts" {
		t.Fatalf("path = %q, want /api/artifacts", gotPath)
	}
	if gotAuth != "Bearer secret-token" {
		t.Fatalf("Authorization = %q, want the bearer token", gotAuth)
	}
	if gotBody["format"] != "offloaded-collab-artifact" {
		t.Fatalf("top-level format = %v, want the contract envelope", gotBody["format"])
	}
	if gotBody["title"] != "Review of PR #7" || gotBody["source"] != sampleArtifact().Source {
		t.Fatalf("title/source did not round-trip: %v", gotBody)
	}
	artifact, ok := gotBody["artifact"].(map[string]any)
	if !ok {
		t.Fatalf("artifact block missing: %v", gotBody)
	}
	if artifact["format"] != "markdown" || artifact["category"] != "pr-review" {
		t.Fatalf("artifact format/category = %v", artifact)
	}
	if artifact["actor"] != "system:pr-reviewer" || artifact["source"] != "pr-review" {
		t.Fatalf("attribution = %v", artifact)
	}
	if artifact["requestId"] != "artifact:pr-review:acme/widget#7" || artifact["version"] != "r1" {
		t.Fatalf("idempotency fields = %v", artifact)
	}
	if artifact["visibility"] != "account-only" || artifact["egress"] != "none" {
		t.Fatalf("visibility/egress = %v", artifact)
	}
	task, ok := artifact["task"].(map[string]any)
	if !ok || task["owner"] != "acme" || task["repo"] != "widget" {
		t.Fatalf("task coordinates = %v", artifact["task"])
	}
}

func TestPublishDoesNotRetryAClientError(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusBadRequest} {
		var attempts atomic.Int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			attempts.Add(1)
			w.WriteHeader(status)
			_, _ = w.Write([]byte(`{"error":"source is required"}`))
		}))
		_, err := NewClient(srv.URL, "tok").Publish(context.Background(), sampleArtifact())
		srv.Close()
		if err == nil {
			t.Fatalf("status %d: Publish error = nil, want failure", status)
		}
		if attempts.Load() != 1 {
			t.Fatalf("status %d attempts = %d, want exactly one attempt", status, attempts.Load())
		}
	}
}

func TestPublishSurfacesTheFieldReasonOn400(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"source is required"}`))
	}))
	defer srv.Close()
	_, err := NewClient(srv.URL, "tok").Publish(context.Background(), sampleArtifact())
	if err == nil || !strings.Contains(err.Error(), "source is required") {
		t.Fatalf("error = %v, want it to carry the field reason", err)
	}
}

func TestPublishRetries503UntilSuccess(t *testing.T) {
	var attempts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if attempts.Add(1) < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"ok":true,"id":"a_2","url":"u"}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "tok")
	c.wait = time.Millisecond
	url, err := c.Publish(context.Background(), sampleArtifact())
	if err != nil || url != "u" {
		t.Fatalf("Publish = (%q, %v), want success after two 503s", url, err)
	}
	if attempts.Load() != 3 {
		t.Fatalf("attempts = %d, want 3", attempts.Load())
	}
}

func TestPublishFailsWhen503Persists(t *testing.T) {
	var attempts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "tok")
	c.wait = time.Millisecond
	if _, err := c.Publish(context.Background(), sampleArtifact()); err == nil {
		t.Fatal("Publish error = nil, want failure after exhausting retries")
	}
	if attempts.Load() != 4 {
		t.Fatalf("attempts = %d, want the initial attempt plus three retries", attempts.Load())
	}
}
