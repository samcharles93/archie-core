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
	Dimension string
	File      string
	LineStart int
	LineEnd   int
	Severity  Severity
	// Category is what the finding is about, and the third of the three things
	// two findings have to share to be one claim: the PRD merges exact
	// duplicates on (file, overlapping lines, category). No producer sets it
	// yet, so an empty category is a category -- two findings that both leave
	// it empty are duplicates of each other when they also share a file and a
	// line.
	Category   string
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
	Category    string
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

// Score drops the findings under their severity's confidence floor, scores
// what is left, merges the duplicates among the survivors and ranks the rest by
// score. The same findings always produce the same scores and the same order,
// whatever order they were handed in.
//
// The floor comes before the merge, so a discarded finding cannot shadow a kept
// one, and the merge is a clustering rather than a scan, so a chain of
// overlapping findings collapses the same way however it was listed.
func Score(findings []Finding, inputs ScoreInputs) []ScoredFinding {
	scored := make([]ScoredFinding, 0, len(findings))
	for _, finding := range findings {
		severity := normalizeSeverity(finding.Severity)
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
			Category:    finding.Category,
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

	ranked := mergeDuplicates(scored)
	slices.SortFunc(ranked, compareScored)
	return ranked
}

// mergeDuplicates collapses the findings that are one claim about one place.
// Two findings are one claim when they name the same file and category and
// their line ranges overlap -- the PRD's exact-duplicate rule, with the wording
// and the severity left out of it. Overlap is transitive, so a chain 1-2, 2-3,
// 3-4 is one claim and not two.
func mergeDuplicates(findings []ScoredFinding) []ScoredFinding {
	ordered := slices.Clone(findings)
	slices.SortFunc(ordered, compareIdentity)

	merged := make([]ScoredFinding, 0, len(ordered))
	for start := 0; start < len(ordered); {
		end := start + 1
		for end < len(ordered) && sameKey(ordered[start], ordered[end]) {
			end++
		}
		merged = append(merged, mergeRun(ordered[start:end])...)
		start = end
	}
	return merged
}

// sameKey reports whether two findings of a canonically ordered list are in the
// same (file, category) group, which is how far the grouping reaches before the
// line ranges decide.
func sameKey(a, b ScoredFinding) bool {
	return a.File == b.File && a.Category == b.Category
}

// mergeRun unions the overlapping ranges of one (file, category) group into
// clusters. The group arrives ordered by line, so a cluster is a run of members
// whose start is inside the range the run has already covered -- which is what
// makes the overlap transitive: 1-2, 2-3 and 3-4 are one cluster even though
// 1-2 and 3-4 do not touch. A run that begins where the previous one ended is
// two clusters: line 13 and the range 10-12 are two places.
//
// The cluster contributes the highest-scoring member, because that is the one
// that says the most, over the union of the cluster's lines, because the
// cluster is the claim and it covers every line of it. It is blocking when any
// member is: a merge may discard wording, never a verdict.
func mergeRun(group []ScoredFinding) []ScoredFinding {
	clusters := make([]findingCluster, 0, len(group))
	for _, member := range group {
		if len(clusters) > 0 && member.LineStart <= clusters[len(clusters)-1].last {
			clusters[len(clusters)-1].add(member)
			continue
		}
		clusters = append(clusters, newFindingCluster(member))
	}

	merged := make([]ScoredFinding, 0, len(clusters))
	for _, cluster := range clusters {
		merged = append(merged, cluster.finding())
	}
	return merged
}

// findingCluster is the run of overlapping findings one output finding stands
// for: the best member's wording and score, the union of the members' lines,
// and the disjunction of their verdicts. The best member is kept whole, so
// choosing between two members reads the fields they carry and never the
// cluster's widened range.
type findingCluster struct {
	best     ScoredFinding
	first    int
	last     int
	blocking bool
}

func newFindingCluster(member ScoredFinding) findingCluster {
	return findingCluster{best: member, first: member.LineStart, last: member.LineEnd, blocking: member.Blocking}
}

// add folds a member whose range overlaps the cluster into it.
func (c *findingCluster) add(member ScoredFinding) {
	c.last = max(c.last, member.LineEnd)
	c.blocking = c.blocking || member.Blocking
	if compareScored(member, c.best) < 0 {
		c.best = member
	}
}

// finding is the one finding the cluster contributes.
func (c *findingCluster) finding() ScoredFinding {
	merged := c.best
	merged.LineStart = c.first
	merged.LineEnd = c.last
	merged.Blocking = c.blocking
	return merged
}

// compareScored is the order findings are ranked in: the highest score first,
// ties broken by the finding itself. It is a total order over what a scored
// finding carries, so it is also what picks a cluster's member and what makes
// any permutation of the same findings produce the same list in the same order.
func compareScored(a, b ScoredFinding) int {
	if order := cmp.Compare(b.Score, a.Score); order != 0 {
		return order
	}
	return compareIdentity(a, b)
}

// compareIdentity orders two findings by their own content: where they are,
// then what they say. It carries no score, so it is the order the merge walks
// rows in -- it has to be by line for the ranges to be unioned -- and it is the
// tie-break that keeps tied findings out of the order they arrived in.
func compareIdentity(a, b ScoredFinding) int {
	if order := cmp.Or(
		cmp.Compare(a.File, b.File),
		cmp.Compare(a.Category, b.Category),
		cmp.Compare(a.LineStart, b.LineStart),
		cmp.Compare(a.LineEnd, b.LineEnd),
		cmp.Compare(string(a.Severity), string(b.Severity)),
		cmp.Compare(a.Dimension, b.Dimension),
		cmp.Compare(a.Title, b.Title),
		cmp.Compare(a.Body, b.Body),
		cmp.Compare(a.Suggestion, b.Suggestion),
		cmp.Compare(a.Evidence, b.Evidence),
		cmp.Compare(a.Confidence, b.Confidence),
		slices.Compare(a.Tags, b.Tags),
		slices.Compare(a.Multipliers, b.Multipliers),
	); order != 0 {
		return order
	}
	// The gating finding comes first, so a pair that differs in nothing else
	// still has one order.
	if a.Blocking == b.Blocking {
		return 0
	}
	if a.Blocking {
		return -1
	}
	return 1
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
