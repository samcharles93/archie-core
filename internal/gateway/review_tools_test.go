package gateway

import (
	"context"
	"errors"
	"testing"
)

type fakePRReviewer struct {
	gotIdentity *string
	gotRepo     string
	gotNumber   int
	result      PRReviewResult
	err         error
}

func (f *fakePRReviewer) ReviewPR(_ context.Context, identity *string, repo string, number int) (PRReviewResult, error) {
	f.gotIdentity = identity
	f.gotRepo = repo
	f.gotNumber = number
	return f.result, f.err
}

func TestReviewToolOmittedWhenReviewerNil(t *testing.T) {
	if entries := ReviewTools(nil, "archie"); len(entries) != 0 {
		t.Fatalf("ReviewTools(nil) = %d entries, want 0 -- a tool that cannot run must not be advertised", len(entries))
	}
}

func TestReviewToolAppearsWhenReviewerNonNil(t *testing.T) {
	entries := ReviewTools(&fakePRReviewer{}, "archie")
	if len(entries) != 1 {
		t.Fatalf("ReviewTools() = %d entries, want 1", len(entries))
	}
	if err := entries[0].Validate(); err != nil {
		t.Fatalf("entry.Validate() = %v", err)
	}
}

func TestReviewPRToolBindsIdentityAndReturnsFindings(t *testing.T) {
	reviewer := &fakePRReviewer{
		result: PRReviewResult{
			Repo: "acme/widget", Number: 7, Model: "openai/gpt-5", Status: "completed",
			Findings: []PRReviewFinding{{File: "a.go", Line: 3, Defect: "nil deref", FailureScenario: "x", Verdict: "confirmed", Level: "error", Category: "nil-risk", Blocking: true}},
		},
	}
	entry := toolNamed(t, ReviewTools(reviewer, "archie"), "review_pr")

	out, err := entry.Handler(context.Background(), map[string]any{
		"repo": "acme/widget", "pr_number": float64(7), "identity": "someone-else",
	})
	if err != nil {
		t.Fatal(err)
	}

	// The model supplied an identity; it must be ignored in favour of the
	// bound identity.
	if reviewer.gotIdentity == nil || *reviewer.gotIdentity != "archie" {
		t.Fatalf("identity = %v, want bound \"archie\"", reviewer.gotIdentity)
	}
	if reviewer.gotRepo != "acme/widget" || reviewer.gotNumber != 7 {
		t.Fatalf("ReviewPR called with (%q, %d), want (acme/widget, 7)", reviewer.gotRepo, reviewer.gotNumber)
	}

	result, ok := out.(PRReviewResult)
	if !ok {
		t.Fatalf("handler returned %T, want PRReviewResult", out)
	}
	if result.Status != "completed" || len(result.Findings) != 1 || !result.Findings[0].Blocking {
		t.Fatalf("result = %+v, want completed with one blocking finding", result)
	}
}

func TestReviewPRToolValidatesInput(t *testing.T) {
	reviewer := &fakePRReviewer{}
	entry := toolNamed(t, ReviewTools(reviewer, "archie"), "review_pr")

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
}

func TestReviewPRToolSurfacesReviewerError(t *testing.T) {
	reviewer := &fakePRReviewer{err: errors.New("review already in progress")}
	entry := toolNamed(t, ReviewTools(reviewer, "archie"), "review_pr")

	if _, err := entry.Handler(context.Background(), map[string]any{
		"repo": "acme/widget", "pr_number": float64(7),
	}); err == nil {
		t.Fatal("handler error = nil, want the reviewer failure surfaced")
	}
}
