package workflow

import (
	"strings"
	"testing"
)

func TestRenderPRReviewSection(t *testing.T) {
	tests := []struct {
		name   string
		report ReviewReport
		empty  bool
		want   []string
	}{
		{
			name:   "zero value renders nothing",
			report: ReviewReport{},
			empty:  true,
		},
		{
			name:   "not run renders nothing",
			report: NewNotRunReviewReport("provider down"),
			empty:  true,
		},
		{
			name:   "zero findings is explicit",
			report: NewCompletedReviewReport(nil, "checked and cleared"),
			want:   []string{"Adversarial self-review ran", "found no defects"},
		},
		{
			name: "checked properties back a zero-finding review",
			report: ReviewReport{
				Status:  ReviewStatusCompleted,
				Checked: []ReviewCheck{{Property: "nil-safety of Foo callers", Evidence: "read all 4 callers"}},
			},
			want: []string{"found no defects", "Checked and cleared", "nil-safety of Foo callers", "read all 4 callers"},
		},
		{
			name: "checked properties also render alongside findings",
			report: ReviewReport{
				Status: ReviewStatusCompleted,
				Findings: []ReviewFinding{
					{File: "a.go", Line: 1, Defect: "warn", FailureScenario: "x", Verdict: ReviewVerdictPlausible, Level: ReviewLevelWarn, Category: ReviewCategoryOther},
				},
				Checked: []ReviewCheck{{Property: "error paths", Evidence: "traced two"}},
			},
			want: []string{"Non-blocking notes", "Checked and cleared", "error paths"},
		},
		{
			name: "non-blocking findings listed",
			report: NewCompletedReviewReport([]ReviewFinding{
				{File: "a.go", Line: 12, Defect: "style nit", FailureScenario: "x", Verdict: ReviewVerdictPlausible, Level: ReviewLevelWarn, Category: ReviewCategoryOther},
			}, "one note"),
			want: []string{"found no blocking defects", "a.go:12", "style nit"},
		},
		{
			name: "whole-file finding omits line",
			report: NewCompletedReviewReport([]ReviewFinding{
				{File: "README.md", Defect: "typo", FailureScenario: "x", Verdict: ReviewVerdictPlausible, Level: ReviewLevelWarn, Category: ReviewCategoryOther},
			}, ""),
			want: []string{"README.md", "typo"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := renderPRReviewSection(tt.report)
			if tt.empty {
				if got != "" {
					t.Fatalf("renderPRReviewSection = %q, want empty", got)
				}
				return
			}
			for _, w := range tt.want {
				if !strings.Contains(got, w) {
					t.Errorf("renderPRReviewSection missing %q in:\n%s", w, got)
				}
			}
		})
	}
}

func TestImplementPRBodyOmitsFindingsWhenReviewDidNotRun(t *testing.T) {
	tc := &TaskContext{BuildSummary: "did the thing", Task: &Task{}}
	body := implementPRBody(tc)
	if strings.Contains(body, "Adversarial review") {
		t.Fatalf("PR body includes a review section when no review ran:\n%s", body)
	}
	if !strings.Contains(body, "did the thing") {
		t.Fatalf("PR body lost the build summary:\n%s", body)
	}
}

func TestImplementPRBodyCarriesZeroFindingSection(t *testing.T) {
	tc := &TaskContext{
		BuildSummary: "did the thing",
		Task:         &Task{},
		ReviewReport: NewCompletedReviewReport(nil, "checked and cleared"),
	}
	body := implementPRBody(tc)
	if !strings.Contains(body, "found no defects") {
		t.Fatalf("PR body missing the zero-findings review section:\n%s", body)
	}
	if !strings.Contains(body, "did the thing") {
		t.Fatalf("PR body lost the build summary:\n%s", body)
	}
}

// TestStageReviewStashesReportForPRBody pins the integration between
// StageReview and implementPRBody: a passing review must land on
// tc.ReviewReport, or the PR body silently omits the findings section
// (renderPRReviewSection returns "" for a zero-value report).
func TestStageReviewStashesReportForPRBody(t *testing.T) {
	tc, _ := reviewTaskContext(t, true)
	tc.Reviewer = &fakeReviewer{report: NewCompletedReviewReport(nil, "clean")}

	if err := StageReview().Run(t.Context(), tc); err != nil {
		t.Fatalf("StageReview().Run() error = %v", err)
	}
	if !tc.ReviewReport.Ran() {
		t.Fatal("ReviewReport not stashed after a passing review; the PR body would omit the findings section")
	}
	if tc.ReviewReport.Status != ReviewStatusCompleted {
		t.Errorf("ReviewReport.Status = %q, want %q", tc.ReviewReport.Status, ReviewStatusCompleted)
	}
}
