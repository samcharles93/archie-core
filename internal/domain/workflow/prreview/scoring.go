package prreview

import (
	"cmp"
	"math"
	"slices"
	"strings"
)

// Severity is how bad a finding is, on the reviewer's own rubric. It drives
// weights, floors and ordering; whether the finding must be fixed before the
// change ships is a separate question, answered by the merge gate.
type Severity string

const (
	SeverityCritical   Severity = "critical"
	SeverityImportant  Severity = "important"
	SeveritySuggestion Severity = "suggestion"
	SeverityNitpick    Severity = "nitpick"
)

// AdversaryVerdict is what the adversary decided about one finding.
type AdversaryVerdict string

const (
	// AdversaryConfirmed is a finding the adversary reproduced or agreed with.
	AdversaryConfirmed AdversaryVerdict = "confirmed"
	// AdversaryChallenged is a finding the adversary argued against.
	AdversaryChallenged AdversaryVerdict = "challenged"
)

// ReviewEvent is the verdict a forge review submits. It has no approving value:
// archie reviews changes it did not write and does not own the merge decision,
// so the strongest thing it can say is that a blocking finding has to be fixed.
type ReviewEvent string

const (
	// ReviewEventComment posts the findings without gating the merge.
	ReviewEventComment ReviewEvent = "COMMENT"
	// ReviewEventRequestChanges asks for the blocking findings to be fixed
	// before the change merges.
	ReviewEventRequestChanges ReviewEvent = "REQUEST_CHANGES"
)

// The multipliers a finding or a run activates. They compound: a compound
// finding the adversary confirmed on a machine-written change with a wide blast
// radius carries all four.
const (
	MultiplierCompound            = "compound"
	MultiplierAdversaryConfirmed  = "adversary_confirmed"
	MultiplierAdversaryChallenged = "adversary_challenged"
	MultiplierAIGenerated         = "ai_generated_pr"
	MultiplierBlastRadiusHigh     = "blast_radius_high"
)

const (
	multiplierCompound            = 1.5
	multiplierAdversaryConfirmed  = 1.3
	multiplierAdversaryChallenged = 0.5
	multiplierAIGenerated         = 1.2
	multiplierBlastRadiusHigh     = 1.2
)

const (
	// BlastRadiusFileThreshold is the number of reached files above which a
	// change is treated as wide-reaching.
	BlastRadiusFileThreshold = 10
	// AIGeneratedThreshold is the intake confidence above which a change counts
	// as machine-written. Above, not at: a coin-flip is not evidence.
	AIGeneratedThreshold = 0.5
	// MaxInlineComments is how many inline comments one review posts. The cap is
	// a budget on the reader's attention, so what it cuts is the tail of the
	// ranked list, never a filter on what was found.
	MaxInlineComments = 25
)

// Finding is one review finding before scoring: what the reviewer observed,
// where, and the two flags that only a verdict can set.
type Finding struct {
	Dimension  string
	File       string
	LineStart  int
	LineEnd    int
	Severity   Severity
	Title      string
	Body       string
	Suggestion string
	Evidence   string
	Confidence float64
	Tags       []string
	// Adversary is the verdict the adversary reached about this finding, empty
	// when it did not reach one. It rides on the finding rather than on a
	// side-list keyed by title, so a verdict cannot land on another finding
	// that happens to share a title.
	Adversary AdversaryVerdict
	// Compound reports that this defect only exists in combination with
	// others, which is what the compound multiplier is for.
	Compound bool
	// Blocking is the merge gate's verdict: only a broken build, a security
	// hole, data loss, a contract break or a regression is blocking, and a
	// failed gate call leaves this false.
	Blocking bool
}

// ScoredFinding is a finding after scoring: ranked, canonicalised, and carrying
// the multipliers that moved it.
type ScoredFinding struct {
	Dimension   string
	File        string
	LineStart   int
	LineEnd     int
	Severity    Severity
	Title       string
	Body        string
	Suggestion  string
	Evidence    string
	Confidence  float64
	Tags        []string
	Score       float64
	Multipliers []string
	Blocking    bool
}

// ScoreInputs is the run context that moves every finding's score.
type ScoreInputs struct {
	// AIGenerated is intake's confidence, 0..1, that the change was written by
	// a machine.
	AIGenerated float64
	// BlastRadiusFiles is how many unchanged files the change reaches.
	BlastRadiusFiles int
}

// Score deduplicates exact duplicates, scores what is left, drops the findings
// under their severity's confidence floor and ranks the rest by score. The same
// findings always produce the same scores and the same order.
func Score(findings []Finding, inputs ScoreInputs) []ScoredFinding {
	scored := make([]ScoredFinding, 0, len(findings))
	seen := map[dedupKey]bool{}
	for _, finding := range findings {
		severity := normalizeSeverity(finding.Severity)
		key := dedupKey{file: finding.File, lineStart: finding.LineStart, lineEnd: finding.LineEnd, severity: severity}
		if seen[key] {
			continue
		}
		seen[key] = true
		if finding.Confidence < confidenceFloor(severity) {
			continue
		}
		score, applied := scoreOf(finding, severity, inputs)
		scored = append(scored, ScoredFinding{
			Dimension:   finding.Dimension,
			File:        finding.File,
			LineStart:   finding.LineStart,
			LineEnd:     finding.LineEnd,
			Severity:    severity,
			Title:       finding.Title,
			Body:        finding.Body,
			Suggestion:  finding.Suggestion,
			Evidence:    finding.Evidence,
			Confidence:  finding.Confidence,
			Tags:        finding.Tags,
			Score:       score,
			Multipliers: applied,
			Blocking:    finding.Blocking,
		})
	}

	slices.SortStableFunc(scored, func(a, b ScoredFinding) int { return cmp.Compare(b.Score, a.Score) })
	return scored
}

// dedupKey is what makes two findings the same finding: one place, one
// severity. The wording is not part of it -- two reviewers describing one
// defect are one comment -- and a different severity at the same place is a
// different claim about it.
type dedupKey struct {
	file      string
	lineStart int
	lineEnd   int
	severity  Severity
}

// scoreOf applies the rubric: the severity's weight times the reviewer's
// confidence, then every multiplier the finding and the run activate. The
// order is fixed, because the same findings have to produce the same score.
func scoreOf(finding Finding, severity Severity, inputs ScoreInputs) (float64, []string) {
	score := severityWeight(severity) * finding.Confidence
	var applied []string

	if finding.Compound {
		score *= multiplierCompound
		applied = append(applied, MultiplierCompound)
	}
	switch finding.Adversary {
	case AdversaryConfirmed:
		score *= multiplierAdversaryConfirmed
		applied = append(applied, MultiplierAdversaryConfirmed)
	case AdversaryChallenged:
		score *= multiplierAdversaryChallenged
		applied = append(applied, MultiplierAdversaryChallenged)
	}
	if inputs.AIGenerated > AIGeneratedThreshold {
		score *= multiplierAIGenerated
		applied = append(applied, MultiplierAIGenerated)
	}
	if inputs.BlastRadiusFiles > BlastRadiusFileThreshold {
		score *= multiplierBlastRadiusHigh
		applied = append(applied, MultiplierBlastRadiusHigh)
	}
	return round3(score), applied
}

// round3 rounds a score to three decimals. A score is read by humans and
// compared by the sort, and a full float64 mantissa invites ties that are not
// ties.
func round3(score float64) float64 { return math.Round(score*1000) / 1000 }

// severityWeight is the rubric's weight for a severity.
func severityWeight(severity Severity) float64 {
	switch severity {
	case SeverityCritical:
		return 1
	case SeverityImportant:
		return 0.7
	case SeveritySuggestion:
		return 0.3
	default:
		return 0.1
	}
}

// confidenceFloor is the confidence under which a finding of this severity is
// dropped: a vague crash is worth a look, a vague nitpick is not worth a
// reader's time.
func confidenceFloor(severity Severity) float64 {
	switch severity {
	case SeverityCritical:
		return 0.2
	case SeverityImportant:
		return 0.3
	default:
		return 0.4
	}
}

// severityAliases maps the words a reviewer model emits onto the rubric.
var severityAliases = map[Severity]Severity{
	Severity("high"):    SeverityCritical,
	Severity("blocker"): SeverityCritical,
	Severity("medium"):  SeverityImportant,
	Severity("major"):   SeverityImportant,
	Severity("minor"):   SeveritySuggestion,
	Severity("low"):     SeveritySuggestion,
	Severity("info"):    SeverityNitpick,
	Severity("trivia"):  SeverityNitpick,
	Severity("trivial"): SeverityNitpick,
}

// normalizeSeverity reads a severity as one of the rubric's four words, however
// it is spelled or capitalised. A word the rubric does not know is read as a
// suggestion: the lowest weight that keeps a plausible finding in the review
// rather than the highest, because an unrecognised word is not evidence of
// severity.
func normalizeSeverity(severity Severity) Severity {
	lowered := Severity(strings.ToLower(strings.TrimSpace(string(severity))))
	switch lowered {
	case SeverityCritical, SeverityImportant, SeveritySuggestion, SeverityNitpick:
		return lowered
	}
	if canonical, known := severityAliases[lowered]; known {
		return canonical
	}
	return SeveritySuggestion
}

// CapInlineComments cuts a ranked list down to the comments one review posts.
// It is applied to the ranked list, so what it cuts is the lowest-scoring tail.
func CapInlineComments(findings []ScoredFinding) []ScoredFinding {
	if len(findings) > MaxInlineComments {
		return findings[:MaxInlineComments]
	}
	return findings
}

// ReviewEventFor decides the event a review is submitted with: a blocking
// finding requests changes, and anything else -- including a review that found
// nothing -- is a comment. A pipeline that found nothing has said nothing about
// the change, and an approval from a reviewer that did not look is worse than
// silence.
func ReviewEventFor(findings []ScoredFinding) ReviewEvent {
	for _, finding := range findings {
		if finding.Blocking {
			return ReviewEventRequestChanges
		}
	}
	return ReviewEventComment
}
