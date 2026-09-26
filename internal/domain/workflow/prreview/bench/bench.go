// Package bench measures the PR reviewer against a set of pull requests whose
// real defects are known: each problem's golden comments. A reviewer produces
// findings, the deterministic core turns them into the comments a review would
// post, and an independent judge decides which golden each comment catches.
// See docs/prds/pr-review-agent.md, Verification.
package bench

import (
	"context"
	"fmt"
	"sync"

	"github.com/samcharles93/archie-core/internal/domain/workflow/prreview"
)

// Golden is one known defect in a problem's pull request.
type Golden struct {
	Comment  string `json:"comment"`
	Severity string `json:"severity"`
}

// Problem is one benchmark pull request and its known defects.
type Problem struct {
	ID      string   `json:"id"`
	PRURL   string   `json:"pr_url"`
	Goldens []Golden `json:"goldens"`
}

// Review is what a reviewer returns for one problem: its findings and the run
// context that scores them.
type Review struct {
	Findings []prreview.Finding
	Inputs   prreview.ScoreInputs
}

// Reviewer reviews one problem's pull request blind: it never sees the goldens.
type Reviewer interface {
	Review(ctx context.Context, p Problem) (Review, error)
}

// NoComment is a verdict's Comment when no posted comment catches the golden.
const NoComment = -1

// Verdict is the judge's decision about one golden: which posted comment, by
// index, identifies the same defect, or NoComment.
type Verdict struct {
	Golden  int    `json:"golden"`
	Comment int    `json:"comment"`
	Reason  string `json:"reason"`
}

// Hit reports whether a posted comment caught the golden.
func (v Verdict) Hit() bool { return v.Comment != NoComment }

// Judge matches a problem's goldens against the comments a review posted. It
// returns one verdict per golden.
type Judge interface {
	Judge(ctx context.Context, p Problem, comments []prreview.ScoredFinding) ([]Verdict, error)
}

// Result is one problem's outcome. Err is set when the reviewer or the judge
// failed, and then the problem is not scored at all: a failure is not a miss.
type Result struct {
	Problem  Problem                  `json:"problem"`
	Comments []prreview.ScoredFinding `json:"comments"`
	Verdicts []Verdict                `json:"verdicts"`
	Err      string                   `json:"error,omitempty"`
}

// Hits is how many goldens a posted comment caught.
func (r Result) Hits() int {
	n := 0
	for _, v := range r.Verdicts {
		if v.Hit() {
			n++
		}
	}
	return n
}

// MatchedComments is how many posted comments caught at least one golden.
func (r Result) MatchedComments() int {
	seen := map[int]bool{}
	for _, v := range r.Verdicts {
		if v.Hit() {
			seen[v.Comment] = true
		}
	}
	return len(seen)
}

// Run reviews and judges every problem, at most concurrency at a time, and
// returns one result per problem in the order given.
func Run(ctx context.Context, problems []Problem, reviewer Reviewer, judge Judge, concurrency int) []Result {
	results := make([]Result, len(problems))
	slots := make(chan struct{}, max(concurrency, 1))
	var wg sync.WaitGroup
	for i, p := range problems {
		wg.Go(func() {
			slots <- struct{}{}
			defer func() { <-slots }()
			results[i] = runOne(ctx, p, reviewer, judge)
		})
	}
	wg.Wait()
	return results
}

func runOne(ctx context.Context, p Problem, reviewer Reviewer, judge Judge) Result {
	res := Result{Problem: p}
	review, err := reviewer.Review(ctx, p)
	if err != nil {
		res.Err = fmt.Sprintf("review: %v", err)
		return res
	}
	res.Comments = prreview.CapInlineComments(prreview.Score(review.Findings, review.Inputs))
	if len(res.Comments) == 0 {
		res.Verdicts = misses(p)
		return res
	}
	verdicts, err := judge.Judge(ctx, p, res.Comments)
	if err == nil {
		err = checkVerdicts(p, len(res.Comments), verdicts)
	}
	if err != nil {
		res.Err = fmt.Sprintf("judge: %v", err)
		return res
	}
	res.Verdicts = verdicts
	return res
}

func misses(p Problem) []Verdict {
	out := make([]Verdict, len(p.Goldens))
	for i := range out {
		out[i] = Verdict{Golden: i, Comment: NoComment, Reason: "the review posted no comments"}
	}
	return out
}

// checkVerdicts requires exactly one verdict per golden, each naming a comment
// that was posted, so a judge that skips a golden cannot read as a miss.
func checkVerdicts(p Problem, comments int, verdicts []Verdict) error {
	seen := make([]bool, len(p.Goldens))
	for _, v := range verdicts {
		if v.Golden < 0 || v.Golden >= len(p.Goldens) {
			return fmt.Errorf("verdict for golden %d, which does not exist", v.Golden)
		}
		if seen[v.Golden] {
			return fmt.Errorf("two verdicts for golden %d", v.Golden)
		}
		seen[v.Golden] = true
		if v.Comment != NoComment && (v.Comment < 0 || v.Comment >= comments) {
			return fmt.Errorf("golden %d matched comment %d, which was not posted", v.Golden, v.Comment)
		}
	}
	for i, ok := range seen {
		if !ok {
			return fmt.Errorf("no verdict for golden %d", i)
		}
	}
	return nil
}

// Summary is the benchmark's aggregate over the problems that were scored.
type Summary struct {
	Problems int `json:"problems"`
	Failed   int `json:"failed"`
	Goldens  int `json:"goldens"`
	Hits     int `json:"hits"`
	Comments int `json:"comments"`
	Matched  int `json:"matched_comments"`
	// Recall is golden micro-recall: goldens caught over goldens scored.
	Recall float64 `json:"recall"`
	// Precision is golden-only precision: posted comments that caught a golden
	// over posted comments. A real defect no golden names counts against it.
	Precision float64 `json:"precision"`
}

// Summarize aggregates results. A failed problem counts only in Failed.
func Summarize(results []Result) Summary {
	var s Summary
	for _, r := range results {
		s.Problems++
		if r.Err != "" {
			s.Failed++
			continue
		}
		s.Goldens += len(r.Problem.Goldens)
		s.Hits += r.Hits()
		s.Comments += len(r.Comments)
		s.Matched += r.MatchedComments()
	}
	s.Recall = ratio(s.Hits, s.Goldens)
	s.Precision = ratio(s.Matched, s.Comments)
	return s
}

func ratio(n, d int) float64 {
	if d == 0 {
		return 0
	}
	return float64(n) / float64(d)
}
