package gateway

import (
	"context"
	"errors"
	"testing"
)

func TestReviewToolOmittedWhenCreatorNil(t *testing.T) {
	if entries := ReviewTools(nil, "archie"); len(entries) != 0 {
		t.Fatalf("ReviewTools(nil) = %d entries, want 0 -- a tool that cannot run must not be advertised", len(entries))
	}
}

func TestReviewToolAppearsWhenCreatorNonNil(t *testing.T) {
	entries := ReviewTools(&fakeCreator{}, "archie")
	if len(entries) != 1 {
		t.Fatalf("ReviewTools() = %d entries, want 1", len(entries))
	}
	if err := entries[0].Validate(); err != nil {
		t.Fatalf("entry.Validate() = %v", err)
	}
}

func TestReviewPRToolQueuesReviewTask(t *testing.T) {
	creator := &fakeCreator{id: 42}
	entry := toolNamed(t, ReviewTools(creator, "archie"), "review_pr")

	out, err := entry.Handler(context.Background(), map[string]any{
		"repo": "acme/widget", "pr_number": float64(7), "identity": "someone-else",
	})
	if err != nil {
		t.Fatal(err)
	}

	got := creator.got
	if got.Workflow != "pr-review" {
		t.Errorf("workflow = %q, want pr-review -- the operator trigger runs the pipeline, it does not review in the turn", got.Workflow)
	}
	if got.Repo != "acme/widget" {
		t.Errorf("repo = %q, want acme/widget -- CreateTask authorizes against it", got.Repo)
	}
	// The model supplied an identity; it must be ignored in favour of the
	// bound identity.
	if got.Identity != "archie" {
		t.Errorf("identity = %q, want bound %q", got.Identity, "archie")
	}
	if n, ok := got.Inputs["pr_number"].(int); !ok || n != 7 {
		t.Errorf("inputs[pr_number] = %#v, want int 7", got.Inputs["pr_number"])
	}
	if got.Title == "" || got.Body == "" {
		t.Errorf("title/body = %q/%q, want both set so the queued task is identifiable", got.Title, got.Body)
	}

	result, ok := out.(TaskSpawnResult)
	if !ok {
		t.Fatalf("handler returned %T, want TaskSpawnResult", out)
	}
	if result.ID != 42 {
		t.Errorf("result.ID = %d, want 42", result.ID)
	}
}

func TestReviewPRToolValidatesInput(t *testing.T) {
	creator := &fakeCreator{}
	entry := toolNamed(t, ReviewTools(creator, "archie"), "review_pr")

	tests := []struct {
		name  string
		input map[string]any
	}{
		{"missing repo", map[string]any{"pr_number": float64(7)}},
		{"missing number", map[string]any{"repo": "acme/widget"}},
		{"zero number", map[string]any{"repo": "acme/widget", "pr_number": float64(0)}},
		{"negative number", map[string]any{"repo": "acme/widget", "pr_number": float64(-1)}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := entry.Handler(context.Background(), tt.input); err == nil {
				t.Fatalf("handler error = nil, want validation error for %v", tt.input)
			}
		})
	}
	if len(creator.got.Inputs) != 0 || creator.got.Repo != "" {
		t.Errorf("rejected input still reached CreateTask: %+v", creator.got)
	}
}

func TestReviewPRToolSurfacesCreatorError(t *testing.T) {
	creator := &fakeCreator{err: errors.New("repo not configured")}
	entry := toolNamed(t, ReviewTools(creator, "archie"), "review_pr")

	if _, err := entry.Handler(context.Background(), map[string]any{
		"repo": "acme/widget", "pr_number": float64(7),
	}); err == nil {
		t.Fatal("handler error = nil, want the creator failure surfaced")
	}
}
