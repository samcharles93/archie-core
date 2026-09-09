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
