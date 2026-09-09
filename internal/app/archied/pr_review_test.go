package archied

import (
	"context"
	"log/slog"
	"testing"

	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/domain/workflow"
	"github.com/samcharles93/archie-core/internal/forge"
)

func TestSplitOwnerRepo(t *testing.T) {
	tests := []struct {
		in      string
		owner   string
		repo    string
		wantErr bool
	}{
		{"acme/widget", "acme", "widget", false},
		{"acme/widget/service", "acme", "widget/service", false},
		{"acme", "", "", true},
		{"/widget", "", "", true},
		{"acme/", "", "", true},
		{"", "", "", true},
	}
	for _, tt := range tests {
		owner, repo, err := splitOwnerRepo(tt.in)
		if tt.wantErr {
			if err == nil {
				t.Errorf("splitOwnerRepo(%q) error = nil, want error", tt.in)
			}
			continue
		}
		if err != nil {
			t.Errorf("splitOwnerRepo(%q) error = %v", tt.in, err)
			continue
		}
		if owner != tt.owner || repo != tt.repo {
			t.Errorf("splitOwnerRepo(%q) = (%q, %q), want (%q, %q)", tt.in, owner, repo, tt.owner, tt.repo)
		}
	}
}

func TestPRIssueText(t *testing.T) {
	tests := []struct {
		name string
		pr   forge.PullRequest
		want string
	}{
		{"title only", forge.PullRequest{Title: "Fix login"}, "Fix login"},
		{"title and body", forge.PullRequest{Title: "Fix login", Body: "The form 500s."}, "Fix login\n\nThe form 500s."},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := prIssueText(tt.pr); got != tt.want {
				t.Errorf("prIssueText = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestMapReviewResult(t *testing.T) {
	report := workflow.ReviewReport{
		Status:  workflow.ReviewStatusCompleted,
		Summary: "checked and cleared",
		Findings: []workflow.ReviewFinding{
			{File: "a.go", Line: 3, Defect: "nil deref", FailureScenario: "x", Verdict: workflow.ReviewVerdictConfirmed, Level: workflow.ReviewLevelError, Category: workflow.ReviewCategoryNilRisk},
			{File: "b.go", Line: 0, Defect: "style", FailureScenario: "y", Verdict: workflow.ReviewVerdictPlausible, Level: workflow.ReviewLevelWarn, Category: workflow.ReviewCategoryOther},
		},
	}
	got := mapReviewResult("acme/widget", 7, "openai/gpt-5", report)

	if got.Repo != "acme/widget" || got.Number != 7 || got.Model != "openai/gpt-5" || got.Status != "completed" || got.Summary != "checked and cleared" {
		t.Fatalf("result meta = %+v", got)
	}
	if len(got.Findings) != 2 {
		t.Fatalf("findings = %d, want 2", len(got.Findings))
	}
	if !got.Findings[0].Blocking {
		t.Error("confirmed error finding should be blocking")
	}
	if got.Findings[1].Blocking {
		t.Error("plausible warn finding must not be blocking")
	}
	if got.Findings[0].Line != 3 {
		t.Errorf("line = %d, want 3", got.Findings[0].Line)
	}
}

func TestMapReviewResultNotRunPreservesReason(t *testing.T) {
	report := workflow.NewNotRunReviewReport("no reviewer model configured")
	got := mapReviewResult("acme/widget", 7, "", report)
	if got.Status != "not_run" || got.Reason != "no reviewer model configured" {
		t.Fatalf("not_run result = %+v, want status not_run and reason preserved", got)
	}
}

func TestPrReviewerAuthorize(t *testing.T) {
	r := &prReviewer{allowed: map[string]map[string]bool{
		"":       {"acme/widget": true, "acme/core": true},
		"archie": {"acme/widget": true},
	}}
	archie := "archie"
	other := "other"

	tests := []struct {
		name     string
		identity *string
		owner    string
		repo     string
		wantErr  bool
	}{
		{"identity own repo", &archie, "acme", "widget", false},
		{"identity foreign repo", &archie, "acme", "core", true},
		{"unknown identity", &other, "acme", "widget", true},
		{"operator any configured", nil, "acme", "core", false},
		{"operator unconfigured repo", nil, "acme", "other", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := r.authorize(tt.identity, tt.owner, tt.repo)
			if tt.wantErr && err == nil {
				t.Errorf("authorize(%v, %s/%s) error = nil, want error", tt.identity, tt.owner, tt.repo)
			}
			if !tt.wantErr && err != nil {
				t.Errorf("authorize(%v, %s/%s) error = %v, want nil", tt.identity, tt.owner, tt.repo, err)
			}
		})
	}
}

func TestPrReviewerSingleflight(t *testing.T) {
	r := &prReviewer{}
	key := "acme/widget#7"
	if !r.begin(key) {
		t.Fatal("first begin = false, want true")
	}
	if r.begin(key) {
		t.Fatal("concurrent begin = true, want false (deterministic single-flight)")
	}
	r.end(key)
	if !r.begin(key) {
		t.Fatal("begin after end = false, want true")
	}
}

func TestBuildReviewAllowlist(t *testing.T) {
	cfg := config.Config{
		Identities: []config.IdentityConfig{
			{Name: "archie", Repos: []config.Repo{{Owner: "acme", Name: "widget"}}},
		},
	}
	allowed := buildReviewAllowlist(cfg)

	if !allowed["archie"]["acme/widget"] {
		t.Errorf("allowlist missing acme/widget for identity archie: %+v", allowed)
	}
	if allowed["archie"]["acme/core"] {
		t.Errorf("allowlist leaked acme/core to identity archie: %+v", allowed)
	}
	if _, ok := allowed["nobody"]; ok {
		t.Errorf("allowlist contains unknown identity nobody: %+v", allowed)
	}
}

// reviewCapableForge satisfies both forge.Forge (via embedding) and
// forge.PullRequestReader, so boot.prReviewer can be exercised in isolation.
type reviewCapableForge struct {
	forge.Forge
}

func (reviewCapableForge) GetPullRequest(context.Context, string, string, int) (forge.PullRequest, error) {
	return forge.PullRequest{}, nil
}

// TestPrReviewerIsMemoized pins the cross-channel single-flight fix: every
// gateway must share one prReviewer so a review of the same PR requested from
// two channels at once deduplicates instead of running twice.
func TestPrReviewerIsMemoized(t *testing.T) {
	b := &boot{forgeClient: reviewCapableForge{Forge: forge.NewNoop(slog.New(slog.DiscardHandler))}}

	first := b.prReviewer()
	if first == nil {
		t.Fatal("prReviewer() = nil, want a reviewer")
	}
	if second := b.prReviewer(); first != second {
		t.Fatal("prReviewer() returned distinct instances; channels would not share the in-flight set")
	}
}
