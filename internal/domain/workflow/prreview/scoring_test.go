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

func TestScoreMergesExactDuplicates(t *testing.T) {
	t.Parallel()

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
			name:   "the same place with another severity is another finding",
			second: func() Finding { f := first; f.Title = "second"; f.Severity = SeverityCritical; return f }(),
			want:   []string{"first", "second"},
		},
		{
			name:   "the same severity elsewhere is another finding",
			second: func() Finding { f := first; f.Title = "second"; f.LineStart = 40; f.LineEnd = 42; return f }(),
			want:   []string{"first", "second"},
		},
		{
			name:   "another spelling of the same severity is the same finding",
			second: func() Finding { f := first; f.Title = "second"; f.Severity = Severity("major"); return f }(),
			want:   []string{"first"},
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

func TestScoreRanksByScoreAndKeepsInputOrderOnTies(t *testing.T) {
	t.Parallel()

	low := scored(SeveritySuggestion, 0.5)
	low.Title = "low"
	low.LineStart = 1
	high := scored(SeverityCritical, 0.9)
	high.Title = "high"
	high.LineStart = 2
	firstTie := scored(SeverityImportant, 0.5)
	firstTie.Title = "tie-first"
	firstTie.LineStart = 3
	secondTie := scored(SeverityImportant, 0.5)
	secondTie.Title = "tie-second"
	secondTie.LineStart = 4

	findings := []Finding{low, high, firstTie, secondTie}

	got := Score(findings, ScoreInputs{})
	titles := make([]string, 0, len(got))
	for _, finding := range got {
		titles = append(titles, finding.Title)
	}
	wantStrings(t, titles, []string{"high", "tie-first", "tie-second", "low"})

	// The same findings always produce the same scores and the same order.
	if again := Score(findings, ScoreInputs{}); !reflect.DeepEqual(got, again) {
		t.Errorf("a second scoring pass differs:\n%+v\n%+v", got, again)
	}
}

func TestScoreCopiesTheFindingItScores(t *testing.T) {
	t.Parallel()

	finding := scored(SeverityImportant, 0.8)
	got := scoreOne(t, finding, ScoreInputs{})

	if got.File != finding.File || got.Dimension != finding.Dimension || got.Title != finding.Title ||
		got.Body != finding.Body || got.Suggestion != finding.Suggestion || got.Evidence != finding.Evidence ||
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
