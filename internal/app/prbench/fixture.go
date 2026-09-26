package prbench

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/samcharles93/archie-core/internal/domain/workflow/prreview"
	"github.com/samcharles93/archie-core/internal/domain/workflow/prreview/bench"
)

// fixtureReviewer reads each problem's findings from <dir>/<id>.json, so the
// deterministic core and the judge can be measured before a live reviewer
// exists, and a live run's findings can be rescored without reviewing again.
type fixtureReviewer struct{ dir string }

type fixture struct {
	AIGenerated      float64          `json:"ai_generated"`
	BlastRadiusFiles int              `json:"blast_radius_files"`
	Findings         []fixtureFinding `json:"findings"`
}

type fixtureFinding struct {
	Dimension  string   `json:"dimension"`
	File       string   `json:"file"`
	LineStart  int      `json:"line_start"`
	LineEnd    int      `json:"line_end"`
	Severity   string   `json:"severity"`
	Category   string   `json:"category"`
	Title      string   `json:"title"`
	Body       string   `json:"body"`
	Suggestion string   `json:"suggestion"`
	Evidence   string   `json:"evidence"`
	Confidence float64  `json:"confidence"`
	Tags       []string `json:"tags"`
	Adversary  string   `json:"adversary"`
	Compound   bool     `json:"compound"`
	Blocking   bool     `json:"blocking"`
}

func (f fixtureReviewer) Review(_ context.Context, p bench.Problem) (bench.Review, error) {
	data, err := os.ReadFile(filepath.Join(f.dir, p.ID+".json"))
	if err != nil {
		return bench.Review{}, err
	}
	var fx fixture
	if err := json.Unmarshal(data, &fx); err != nil {
		return bench.Review{}, fmt.Errorf("decode %s findings: %w", p.ID, err)
	}
	review := bench.Review{Inputs: prreview.ScoreInputs{AIGenerated: fx.AIGenerated, BlastRadiusFiles: fx.BlastRadiusFiles}}
	for i, ff := range fx.Findings {
		switch prreview.Severity(ff.Severity) {
		case prreview.SeverityCritical, prreview.SeverityImportant, prreview.SeveritySuggestion, prreview.SeverityNitpick:
		default:
			return bench.Review{}, fmt.Errorf("%s finding %d: unknown severity %q", p.ID, i, ff.Severity)
		}
		review.Findings = append(review.Findings, prreview.Finding{
			Dimension: ff.Dimension, File: ff.File, LineStart: ff.LineStart, LineEnd: ff.LineEnd,
			Severity: prreview.Severity(ff.Severity), Category: ff.Category, Title: ff.Title, Body: ff.Body,
			Suggestion: ff.Suggestion, Evidence: ff.Evidence, Confidence: ff.Confidence, Tags: ff.Tags,
			Adversary: prreview.AdversaryVerdict(ff.Adversary), Compound: ff.Compound, Blocking: ff.Blocking,
		})
	}
	return review, nil
}
