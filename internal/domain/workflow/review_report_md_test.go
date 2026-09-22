package workflow

import (
	"strings"
	"testing"
)

func mdFixture() ReviewReport {
	disposition := ReviewDisposition("fixed")
	return ReviewReport{
		Status:  ReviewStatusCompleted,
		Summary: "The nil-deref path is reachable from a malformed payload.",
		Findings: []ReviewFinding{
			{
				File: "handler.go", Line: 42, Defect: "nil deref on empty payload",
				FailureScenario: "POST with a body lacking the field crashes the handler.",
				Suggestion:      "guard the field before use", Verdict: ReviewVerdictConfirmed,
				Level: ReviewLevelError, Category: "correctness", Disposition: disposition,
			},
			{
				File: "store.go", Line: 9, Defect: "query loses the filter",
				FailureScenario: "paged reads return rows from other scopes",
				Verdict:         ReviewVerdictPlausible, Level: ReviewLevelWarn,
			},
		},
		Checked: []ReviewCheck{
			{Property: "nil-safety of Foo callers", Evidence: "read all 4 callers; each nil-checks"},
		},
	}
}

func TestReviewReportRendersFindingsAsMarkdown(t *testing.T) {
	got := mdFixture().RenderMarkdown("Review of acme/widget#42")
	for _, want := range []string{
		"# Review of acme/widget#42",
		"The nil-deref path is reachable",
		"### handler.go:42",
		"**Defect:** nil deref on empty payload",
		"**Failure scenario:** POST with a body lacking the field crashes the handler.",
		"error (confirmed)",
		"Suggested fix: guard the field before use",
		"Disposition: fixed",
		"### store.go:9",
		"warn (plausible)",
		"## Checked clean",
		"- nil-safety of Foo callers — read all 4 callers; each nil-checks",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("rendered report missing %q:\n%s", want, got)
		}
	}
}

func TestReviewReportRendersNotRunStatus(t *testing.T) {
	report := NewNotRunReviewReport("no reviewer model configured")
	got := report.RenderMarkdown("Review of acme/widget#42")
	if !strings.Contains(got, "no reviewer model configured") {
		t.Fatalf("skip reason missing:\n%s", got)
	}
	if strings.Contains(got, "## Findings") || strings.Contains(got, "## Checked clean") {
		t.Fatalf("a not-run report must not render finding or check sections:\n%s", got)
	}
}

func TestReviewReportRendersACleanReview(t *testing.T) {
	report := ReviewReport{
		Status:  ReviewStatusCompleted,
		Summary: "No defects found.",
		Checked: []ReviewCheck{{Property: "p", Evidence: "e"}},
	}
	got := report.RenderMarkdown("Review")
	if strings.Contains(got, "### ") {
		t.Fatalf("a zero-finding review must not render finding sections:\n%s", got)
	}
	if !strings.Contains(got, "No defects found.") || !strings.Contains(got, "- p — e") {
		t.Fatalf("summary and checks missing:\n%s", got)
	}
}
