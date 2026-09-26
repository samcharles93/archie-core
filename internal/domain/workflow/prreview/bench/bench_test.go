package bench

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/samcharles93/archie-core/internal/domain/workflow/prreview"
)

type fakeReviewer map[string]Review

func (f fakeReviewer) Review(_ context.Context, p Problem) (Review, error) {
	r, ok := f[p.ID]
	if !ok {
		return Review{}, errors.New("no review")
	}
	return r, nil
}

type fakeJudge struct {
	verdicts map[string][]Verdict
	calls    int
}

func (f *fakeJudge) Judge(_ context.Context, p Problem, _ []prreview.ScoredFinding) ([]Verdict, error) {
	f.calls++
	return f.verdicts[p.ID], nil
}

func problem(id string, goldens int) Problem {
	return Problem{ID: id, Goldens: make([]Golden, goldens)}
}

func finding(file string, line int, confidence float64) prreview.Finding {
	return prreview.Finding{File: file, LineStart: line, LineEnd: line, Severity: prreview.SeverityImportant, Title: file, Confidence: confidence}
}

func TestRunScoresEachProblem(t *testing.T) {
	tests := []struct {
		name      string
		review    Review
		verdicts  []Verdict
		wantErr   string
		wantHits  int
		wantPosts int
		wantCalls int
	}{
		{
			name:      "hit and miss",
			review:    Review{Findings: []prreview.Finding{finding("a.go", 1, 0.9), finding("b.go", 1, 0.9)}},
			verdicts:  []Verdict{{Golden: 0, Comment: 1}, {Golden: 1, Comment: NoComment}},
			wantHits:  1,
			wantPosts: 2,
			wantCalls: 1,
		},
		{
			name:      "every finding under the floor posts nothing and skips the judge",
			review:    Review{Findings: []prreview.Finding{finding("a.go", 1, 0.1)}},
			wantCalls: 0,
		},
		{
			name:      "a judge that skips a golden fails the problem",
			review:    Review{Findings: []prreview.Finding{finding("a.go", 1, 0.9)}},
			verdicts:  []Verdict{{Golden: 0, Comment: 0}},
			wantErr:   "no verdict for golden 1",
			wantPosts: 1,
			wantCalls: 1,
		},
		{
			name:      "a judge that names an unposted comment fails the problem",
			review:    Review{Findings: []prreview.Finding{finding("a.go", 1, 0.9)}},
			verdicts:  []Verdict{{Golden: 0, Comment: 3}, {Golden: 1, Comment: NoComment}},
			wantErr:   "comment 3",
			wantPosts: 1,
			wantCalls: 1,
		},
		{
			name:      "duplicate verdicts fail the problem",
			review:    Review{Findings: []prreview.Finding{finding("a.go", 1, 0.9)}},
			verdicts:  []Verdict{{Golden: 0, Comment: 0}, {Golden: 0, Comment: 0}},
			wantErr:   "two verdicts",
			wantPosts: 1,
			wantCalls: 1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := problem("p", 2)
			judge := &fakeJudge{verdicts: map[string][]Verdict{"p": tt.verdicts}}
			got := Run(t.Context(), []Problem{p}, fakeReviewer{"p": tt.review}, judge, 1)[0]
			if tt.wantErr == "" && got.Err != "" || !strings.Contains(got.Err, tt.wantErr) {
				t.Fatalf("Err = %q, want containing %q", got.Err, tt.wantErr)
			}
			if tt.wantErr == "" && len(got.Verdicts) != len(p.Goldens) {
				t.Errorf("verdicts = %d, want one per golden", len(got.Verdicts))
			}
			if got.Hits() != tt.wantHits || len(got.Comments) != tt.wantPosts || judge.calls != tt.wantCalls {
				t.Errorf("hits, comments, judge calls = %d, %d, %d; want %d, %d, %d",
					got.Hits(), len(got.Comments), judge.calls, tt.wantHits, tt.wantPosts, tt.wantCalls)
			}
		})
	}
}

func TestRunKeepsProblemOrder(t *testing.T) {
	problems := []Problem{problem("a", 0), problem("b", 0), problem("c", 0)}
	results := Run(t.Context(), problems, fakeReviewer{"a": {}, "c": {}}, &fakeJudge{}, 3)
	for i, r := range results {
		if r.Problem.ID != problems[i].ID {
			t.Fatalf("result %d is %s, want %s", i, r.Problem.ID, problems[i].ID)
		}
	}
	if results[1].Err == "" {
		t.Error("the reviewer's failure on b was not recorded")
	}
}

func TestSummarize(t *testing.T) {
	comments := make([]prreview.ScoredFinding, 4)
	results := []Result{
		{
			Problem:  problem("a", 3),
			Comments: comments,
			// Two goldens caught by one comment: two hits, one matched comment.
			Verdicts: []Verdict{{Golden: 0, Comment: 2}, {Golden: 1, Comment: 2}, {Golden: 2, Comment: NoComment}},
		},
		{
			Problem:  problem("b", 1),
			Comments: comments[:1],
			Verdicts: []Verdict{{Golden: 0, Comment: 0}},
		},
		{Problem: problem("c", 5), Comments: comments, Err: "judge: down"},
	}
	got := Summarize(results)
	want := Summary{Problems: 3, Failed: 1, Goldens: 4, Hits: 3, Comments: 5, Matched: 2, Recall: 0.75, Precision: 0.4}
	if got != want {
		t.Errorf("Summarize = %+v\nwant        %+v", got, want)
	}
}
