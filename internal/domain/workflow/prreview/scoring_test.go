package prreview

import (
	"reflect"
	"slices"
	"strings"
	"testing"
)

// scored builds the finding every scoring case starts from: one advisory
// finding in one place, whose severity, confidence and flags the case varies.
func scored(severity Severity, confidence float64) Finding {
	return Finding{
		Dimension:  "behaviour",
		File:       "pkg/a.go",
		LineStart:  10,
		LineEnd:    12,
		Severity:   severity,
		Title:      "the defect",
		Body:       "what goes wrong",
		Suggestion: "how to fix it",
		Evidence:   "the code",
		Confidence: confidence,
		Tags:       []string{"tag"},
	}
}

func scoreOne(t *testing.T, finding Finding, inputs ScoreInputs) ScoredFinding {
	t.Helper()

	found := Score([]Finding{finding}, inputs)
	if len(found) != 1 {
		t.Fatalf("Score() returned %d findings, want the one it was given", len(found))
	}
	return found[0]
}

func TestScoreWeighsSeverityConfidenceAndContext(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		severity    Severity
		confidence  float64
		adversary   AdversaryVerdict
		compound    bool
		inputs      ScoreInputs
		wantScore   float64
		wantApplied []string
	}{
		{name: "critical", severity: SeverityCritical, confidence: 1, wantScore: 1},
		{name: "important", severity: SeverityImportant, confidence: 1, wantScore: 0.7},
		{name: "suggestion", severity: SeveritySuggestion, confidence: 1, wantScore: 0.3},
		{name: "nitpick", severity: SeverityNitpick, confidence: 1, wantScore: 0.1},
		{name: "confidence scales the weight", severity: SeverityImportant, confidence: 0.5, wantScore: 0.35},
		{name: "a low confidence scales it down", severity: SeveritySuggestion, confidence: 0.5, wantScore: 0.15},
		{
			name: "a compound finding compounds", severity: SeverityCritical, confidence: 0.5, compound: true,
			wantScore: 0.75, wantApplied: []string{MultiplierCompound},
		},
		{
			name: "an adversary confirmation raises the score", severity: SeverityImportant, confidence: 0.5, adversary: AdversaryConfirmed,
			wantScore: 0.455, wantApplied: []string{MultiplierAdversaryConfirmed},
		},
		{
			name: "an adversary challenge lowers it", severity: SeverityImportant, confidence: 0.5, adversary: AdversaryChallenged,
			wantScore: 0.175, wantApplied: []string{MultiplierAdversaryChallenged},
		},
		{
			name: "a machine-written change raises it", severity: SeveritySuggestion, confidence: 0.5, inputs: ScoreInputs{AIGenerated: 0.51},
			wantScore: 0.18, wantApplied: []string{MultiplierAIGenerated},
		},
		{
			name: "a wide blast radius raises it", severity: SeveritySuggestion, confidence: 0.5, inputs: ScoreInputs{BlastRadiusFiles: 11},
			wantScore: 0.18, wantApplied: []string{MultiplierBlastRadiusHigh},
		},
		{
			name: "multipliers compound", severity: SeverityCritical, confidence: 0.5, compound: true, adversary: AdversaryConfirmed,
			inputs:    ScoreInputs{AIGenerated: 0.51, BlastRadiusFiles: 11},
			wantScore: 1.404, wantApplied: []string{
				MultiplierCompound, MultiplierAdversaryConfirmed, MultiplierAIGenerated, MultiplierBlastRadiusHigh,
			},
		},
		{
			name: "an AI confidence at the threshold does not raise the score", severity: SeveritySuggestion, confidence: 1,
			inputs: ScoreInputs{AIGenerated: 0.5}, wantScore: 0.3,
		},
		{
			name: "a blast radius at the threshold does not raise the score", severity: SeveritySuggestion, confidence: 1,
			inputs: ScoreInputs{BlastRadiusFiles: 10}, wantScore: 0.3,
		},
		{name: "a severity alias is read as the rubric's word", severity: Severity("high"), confidence: 0.5, wantScore: 0.5},
		{name: "a differently spelled severity is still canonical", severity: Severity("Critical"), confidence: 1, wantScore: 1},
		{name: "an unknown severity is read as a suggestion", severity: Severity("concerning"), confidence: 0.5, wantScore: 0.15},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			finding := scored(test.severity, test.confidence)
			finding.Adversary = test.adversary
			finding.Compound = test.compound

			got := scoreOne(t, finding, test.inputs)
			if got.Score != test.wantScore {
				t.Errorf("Score = %v, want %v", got.Score, test.wantScore)
			}
			if len(got.Multipliers) != len(test.wantApplied) {
				t.Fatalf("Multipliers = %v, want %v", got.Multipliers, test.wantApplied)
			}
			for i := range got.Multipliers {
				if got.Multipliers[i] != test.wantApplied[i] {
					t.Errorf("Multipliers[%d] = %q, want %q", i, got.Multipliers[i], test.wantApplied[i])
				}
			}
		})
	}
}

func TestScoreDropsFindingsUnderTheirConfidenceFloor(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		severity   Severity
		confidence float64
		wantKept   bool
	}{
		{name: "critical under its floor", severity: SeverityCritical, confidence: 0.19},
		{name: "critical at its floor", severity: SeverityCritical, confidence: 0.2, wantKept: true},
		{name: "important under its floor", severity: SeverityImportant, confidence: 0.29},
		{name: "important at its floor", severity: SeverityImportant, confidence: 0.3, wantKept: true},
		{name: "suggestion under its floor", severity: SeveritySuggestion, confidence: 0.39},
		{name: "suggestion at its floor", severity: SeveritySuggestion, confidence: 0.4, wantKept: true},
		{name: "nitpick under its floor", severity: SeverityNitpick, confidence: 0.39},
		{name: "nitpick at its floor", severity: SeverityNitpick, confidence: 0.4, wantKept: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got := Score([]Finding{scored(test.severity, test.confidence)}, ScoreInputs{})
			if kept := len(got) == 1; kept != test.wantKept {
				t.Errorf("Score() kept %d findings, want kept = %t", len(got), test.wantKept)
			}
		})
	}
}

func TestScoreMergesFindingsThatShareAFileLinesAndCategory(t *testing.T) {
	t.Parallel()

	// Two findings are one claim when they name the same file, their line
	// ranges overlap and they carry the same category. The wording and the
	// severity are not part of that: two reviewers describing one defect at one
	// place have made one finding, however differently they graded it.
	first := scored(SeverityImportant, 0.8)
	first.Title = "first"

	tests := []struct {
		name   string
		second Finding
		want   []string
	}{
		{
			name:   "the same defect in the same place is one finding",
			second: func() Finding { f := first; f.Title = "second"; f.Body = "worded differently"; return f }(),
			want:   []string{"first"},
		},
		{
			name:   "the same place with another severity is still one finding",
			second: func() Finding { f := first; f.Title = "second"; f.Severity = SeverityCritical; return f }(),
			want:   []string{"second"},
		},
		{
			name:   "another spelling of the same severity is the same finding",
			second: func() Finding { f := first; f.Title = "second"; f.Severity = Severity("major"); return f }(),
			want:   []string{"first"},
		},
		{
			name:   "the same lines under another category are another finding",
			second: func() Finding { f := first; f.Title = "second"; f.Category = "security"; return f }(),
			want:   []string{"first", "second"},
		},
		{
			name:   "a finding elsewhere in the same file is another finding",
			second: func() Finding { f := first; f.Title = "second"; f.LineStart = 40; f.LineEnd = 42; return f }(),
			want:   []string{"first", "second"},
		},
		{
			name:   "the same lines in another file are another finding",
			second: func() Finding { f := first; f.Title = "second"; f.File = "pkg/b.go"; return f }(),
			want:   []string{"first", "second"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got := Score([]Finding{first, test.second}, ScoreInputs{})
			titles := make([]string, 0, len(got))
			for _, finding := range got {
				titles = append(titles, finding.Title)
			}
			// The ranking is asserted elsewhere; here the question is which
			// findings survived, so the titles are compared as a set.
			slices.Sort(titles)
			wantStrings(t, titles, test.want)
		})
	}
}

func TestScoreMergesTwoReviewersOfOneLine(t *testing.T) {
	t.Parallel()

	// The commonest duplicate there is: two reviewers both cite one line, and
	// neither finding names a category, because nothing sets one yet. An empty
	// category is a category.
	confident := scored(SeverityImportant, 0.9)
	confident.Title = "cited by the first reviewer"
	confident.LineStart = 7
	confident.LineEnd = 7
	lessConfident := scored(SeverityImportant, 0.5)
	lessConfident.Title = "cited by the second reviewer"
	lessConfident.LineStart = 7
	lessConfident.LineEnd = 7

	orders := []struct {
		name     string
		findings []Finding
	}{
		{name: "the confident reviewer first", findings: []Finding{confident, lessConfident}},
		{name: "the less confident reviewer first", findings: []Finding{lessConfident, confident}},
	}

	for _, order := range orders {
		t.Run(order.name, func(t *testing.T) {
			t.Parallel()

			got := Score(order.findings, ScoreInputs{})
			if len(got) != 1 {
				t.Fatalf("Score() returned %d findings for one cited line, want 1", len(got))
			}
			if got[0].Title != confident.Title {
				t.Errorf("Score() kept %q, want %q", got[0].Title, confident.Title)
			}
			if got[0].LineStart != 7 || got[0].LineEnd != 7 {
				t.Errorf("Score() kept lines %d-%d, want 7-7", got[0].LineStart, got[0].LineEnd)
			}
		})
	}
}

func TestScoreMergesOverlappingFindingsInAnyOrder(t *testing.T) {
	t.Parallel()

	// Three reviewers report one defect over a chain of ranges: 1-2, 2-3 and
	// 3-4. Each range overlaps the next, so the three are one claim about one
	// place even though the first and the last do not touch -- and the answer
	// cannot depend on which reviewer was asked first. Every finding scores the
	// same, so nothing but the clustering can decide it.
	chain := []Finding{
		chainFinding("reported 1-2", 1, 2),
		chainFinding("reported 2-3", 2, 3),
		chainFinding("reported 3-4", 3, 4),
	}

	for _, order := range permutations(len(chain)) {
		findings := make([]Finding, 0, len(chain))
		for _, index := range order {
			findings = append(findings, chain[index])
		}

		got := Score(findings, ScoreInputs{})
		if len(got) != 1 {
			// Two overlapping findings that tie can only be told apart by the
			// order they were handed in, which is the defect this pins.
			t.Fatalf("Score() over order %v returned %d findings, want one cluster", order, len(got))
		}
		if got[0].Title != chain[0].Title {
			t.Errorf("Score() over order %v kept %q, want %q", order, got[0].Title, chain[0].Title)
		}
		if got[0].LineStart != 1 || got[0].LineEnd != 4 {
			t.Errorf("Score() over order %v kept lines %d-%d, want the cluster's 1-4", order, got[0].LineStart, got[0].LineEnd)
		}
	}
}

func TestScoreKeepsABlockingDuplicateSweptIntoAMerge(t *testing.T) {
	t.Parallel()

	// The merge gate ruled on both duplicates and only one of them is
	// blocking. The merge keeps the higher-scoring wording, and the verdict has
	// to survive it: an advisory duplicate must not be able to swallow a
	// blocking one, whatever order they arrive in.
	blocking := scored(SeverityCritical, 0.5)
	blocking.Title = "blocking, reported with less confidence"
	blocking.Blocking = true
	advisory := scored(SeverityImportant, 0.9)
	advisory.Title = "advisory, reported with more confidence"

	orders := []struct {
		name     string
		findings []Finding
	}{
		{name: "the advisory duplicate first", findings: []Finding{advisory, blocking}},
		{name: "the blocking duplicate first", findings: []Finding{blocking, advisory}},
	}

	for _, order := range orders {
		t.Run(order.name, func(t *testing.T) {
			t.Parallel()

			got := Score(order.findings, ScoreInputs{})
			if len(got) != 1 {
				t.Fatalf("Score() returned %d findings for one place, want 1", len(got))
			}
			if got[0].Title != advisory.Title {
				t.Errorf("Score() kept %q, want the higher-scoring %q", got[0].Title, advisory.Title)
			}
			if !got[0].Blocking {
				t.Errorf("Score() kept the advisory wording and dropped the blocking verdict: %+v", got[0])
			}
			if event := ReviewEventFor(got); event != ReviewEventRequestChanges {
				t.Errorf("ReviewEventFor() = %q, want %q", event, ReviewEventRequestChanges)
			}
		})
	}
}

// chainFinding builds one finding of the overlapping chain the order test
// scores: the same defect, in the same file and category, at its own range.
func chainFinding(title string, lineStart, lineEnd int) Finding {
	finding := scored(SeverityImportant, 0.8)
	finding.Title = title
	finding.LineStart = lineStart
	finding.LineEnd = lineEnd
	return finding
}

// permutations returns every ordering of the indexes 0..n-1, so a test can ask
// the same question of every way the same findings could have been handed over.
func permutations(n int) [][]int {
	orders := [][]int{{}}
	for len(orders[0]) < n {
		next := make([][]int, 0, len(orders))
		for _, order := range orders {
			for candidate := range n {
				if slices.Contains(order, candidate) {
					continue
				}
				next = append(next, append(slices.Clone(order), candidate))
			}
		}
		orders = next
	}
	return orders
}

func TestScoreAppliesTheConfidenceFloorBeforeDeduplication(t *testing.T) {
	t.Parallel()

	// Both findings are the same defect in the same place, so the duplicate
	// rule reads them as one finding: one is under important's confidence
	// floor and one is over it.
	below := scored(SeverityImportant, 0.29)
	below.Title = "below floor"
	above := scored(SeverityImportant, 0.8)
	above.Title = "above floor"

	tests := []struct {
		name     string
		findings []Finding
	}{
		{name: "a below-floor finding first does not suppress the above-floor one", findings: []Finding{below, above}},
		{name: "an above-floor finding first survives the below-floor one", findings: []Finding{above, below}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got := Score(test.findings, ScoreInputs{})
			if len(got) != 1 {
				t.Fatalf("Score() returned %d findings, want the one above the confidence floor", len(got))
			}
			if got[0].Title != "above floor" {
				t.Errorf("Score() kept %q, want %q", got[0].Title, "above floor")
			}
		})
	}
}

func TestScoreMergesFindingsWhoseLinesOverlap(t *testing.T) {
	t.Parallel()

	// The same defect reported at 10-12 and at 10-13 by two reviewers, the
	// second less sure of it than the first.
	higher := scored(SeverityImportant, 0.8)
	higher.LineStart = 10
	higher.LineEnd = 12
	higher.Title = "higher"
	lower := scored(SeverityImportant, 0.6)
	lower.LineStart = 10
	lower.LineEnd = 13
	lower.Title = "lower"

	// 12 and 13 do not share a line, so these are two findings.
	before := scored(SeverityImportant, 0.8)
	before.LineStart = 12
	before.LineEnd = 12
	before.Title = "before the gap"
	after := scored(SeverityImportant, 0.8)
	after.LineStart = 13
	after.LineEnd = 13
	after.Title = "after the gap"

	// The same lines under another category are another claim about them.
	other := scored(SeverityImportant, 0.8)
	other.LineStart = 10
	other.LineEnd = 13
	other.Title = "another category"
	other.Category = "security"

	tests := []struct {
		name     string
		findings []Finding
		want     []string
	}{
		{
			name:     "an overlapping range collapses to the higher-scoring finding",
			findings: []Finding{higher, lower},
			want:     []string{"higher"},
		},
		{
			name:     "input order does not decide which overlapping finding survives",
			findings: []Finding{lower, higher},
			want:     []string{"higher"},
		},
		{
			name:     "ranges that touch but do not overlap are two findings",
			findings: []Finding{before, after},
			want:     []string{"after the gap", "before the gap"},
		},
		{
			name:     "an overlapping range of another category is another finding",
			findings: []Finding{higher, other},
			want:     []string{"another category", "higher"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got := Score(test.findings, ScoreInputs{})
			titles := make([]string, 0, len(got))
			for _, finding := range got {
				titles = append(titles, finding.Title)
			}
			// The ranking is asserted elsewhere; here the question is which
			// findings survived, so the titles are compared as a set.
			slices.Sort(titles)
			wantStrings(t, titles, test.want)
		})
	}
}

func TestScoreRanksByScoreThenByTheFindingItself(t *testing.T) {
	t.Parallel()

	// Every finding here covers a line of its own: the ties are two findings,
	// not one reported twice. A tie is broken by the finding's own content, so
	// no permutation of the same findings can rank them differently.
	low := scored(SeveritySuggestion, 0.5)
	low.Title = "low"
	low.LineStart = 1
	low.LineEnd = 1
	high := scored(SeverityCritical, 0.9)
	high.Title = "high"
	high.LineStart = 2
	high.LineEnd = 2
	firstTie := scored(SeverityImportant, 0.5)
	firstTie.Title = "tie-first"
	firstTie.LineStart = 3
	firstTie.LineEnd = 3
	secondTie := scored(SeverityImportant, 0.5)
	secondTie.Title = "tie-second"
	secondTie.LineStart = 4
	secondTie.LineEnd = 4

	findings := []Finding{low, high, firstTie, secondTie}

	got := Score(findings, ScoreInputs{})
	titles := make([]string, 0, len(got))
	for _, finding := range got {
		titles = append(titles, finding.Title)
	}
	wantStrings(t, titles, []string{"high", "tie-first", "tie-second", "low"})

	// The same findings always produce the same scores and the same order,
	// whatever order they were handed in.
	for _, order := range permutations(len(findings)) {
		permuted := make([]Finding, 0, len(findings))
		for _, index := range order {
			permuted = append(permuted, findings[index])
		}
		if again := Score(permuted, ScoreInputs{}); !reflect.DeepEqual(got, again) {
			t.Errorf("scoring order %v differs:\n%+v\n%+v", order, got, again)
		}
	}
}

func TestScoreCopiesTheFindingItScores(t *testing.T) {
	t.Parallel()

	finding := scored(SeverityImportant, 0.8)
	finding.Category = "security"
	got := scoreOne(t, finding, ScoreInputs{})

	if got.File != finding.File || got.Dimension != finding.Dimension || got.Title != finding.Title ||
		got.Body != finding.Body || got.Suggestion != finding.Suggestion || got.Evidence != finding.Evidence ||
		got.Category != finding.Category ||
		got.LineStart != finding.LineStart || got.LineEnd != finding.LineEnd || got.Confidence != finding.Confidence {
		t.Errorf("ScoredFinding = %+v, want the finding it was scored from", got)
	}
	if got.Severity != SeverityImportant {
		t.Errorf("Severity = %q, want %q", got.Severity, SeverityImportant)
	}
	if !reflect.DeepEqual(got.Tags, finding.Tags) {
		t.Errorf("Tags = %v, want %v", got.Tags, finding.Tags)
	}
}

func TestCapInlineCommentsKeepsTheHighestScoring(t *testing.T) {
	t.Parallel()

	findings := make([]Finding, 0, 30)
	for index := range 30 {
		finding := scored(SeverityCritical, 1-float64(index)/100)
		finding.Title = "finding " + strings.Repeat("x", index+1)
		finding.LineStart = index + 1
		finding.LineEnd = index + 1
		findings = append(findings, finding)
	}

	ranked := Score(findings, ScoreInputs{})
	if len(ranked) != 30 {
		t.Fatalf("Score() returned %d findings, want 30", len(ranked))
	}

	capped := CapInlineComments(ranked)
	if len(capped) != 25 {
		t.Fatalf("CapInlineComments() returned %d findings, want 25", len(capped))
	}
	if !reflect.DeepEqual(capped, ranked[:25]) {
		t.Errorf("CapInlineComments() cut from the wrong end:\n%+v", capped)
	}

	short := ranked[:3]
	if got := CapInlineComments(short); len(got) != 3 {
		t.Errorf("CapInlineComments() returned %d findings for a list under the cap, want 3", len(got))
	}
}

func TestReviewEventFor(t *testing.T) {
	t.Parallel()

	advisory := make([]ScoredFinding, 0, 25)
	for index := range 25 {
		finding := scoreOne(t, scored(SeverityImportant, 0.8), ScoreInputs{})
		finding.Title = "advisory " + strings.Repeat("x", index+1)
		advisory = append(advisory, finding)
	}
	postCap := append(append([]ScoredFinding{}, advisory...), ScoredFinding{Title: "blocking", Blocking: true})

	tests := []struct {
		name     string
		findings []ScoredFinding
		want     ReviewEvent
	}{
		{name: "a review that found nothing is still a comment", want: ReviewEventComment},
		{name: "advisory findings do not gate the merge", findings: advisory, want: ReviewEventComment},
		{name: "a blocking finding asks for changes", findings: []ScoredFinding{{Blocking: true}}, want: ReviewEventRequestChanges},
		{name: "a blocking finding past the comment cap still asks for changes", findings: postCap, want: ReviewEventRequestChanges},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			if got := ReviewEventFor(test.findings); got != test.want {
				t.Errorf("ReviewEventFor() = %q, want %q", got, test.want)
			}
		})
	}
}
