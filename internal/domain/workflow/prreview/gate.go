package prreview

import (
	"encoding/json"
	"fmt"

	"github.com/samcharles93/archie-core/internal/domain/workflow/task"
)

// Key is a scored finding's stable identity inside one gate document: file,
// line and title. Findings carry no ID, so this is what an operator's
// selection names and what filtering matches on. The document stores the key
// beside the finding (task.ReviewGateFinding), because the operator surface
// never sees this type.
func (f ScoredFinding) Key() string {
	return fmt.Sprintf("%s:%d:%s", f.File, f.LineStart, f.Title)
}

// GateFinding renders f as a document finding: the key an operator's
// selection names, beside the finding's own encoding.
func GateFinding(f ScoredFinding) task.ReviewGateFinding {
	data, err := json.Marshal(f)
	if err != nil {
		// ScoredFinding holds only strings, numbers, bools and slices; a
		// marshal failure would be a programming error.
		return task.ReviewGateFinding{Key: f.Key()}
	}
	return task.ReviewGateFinding{Key: f.Key(), Finding: data}
}

// GateFindings renders a whole scored review for the document.
func GateFindings(findings []ScoredFinding) []task.ReviewGateFinding {
	out := make([]task.ReviewGateFinding, len(findings))
	for i, f := range findings {
		out[i] = GateFinding(f)
	}
	return out
}

// PostedFindings decodes the findings an approve posts back into the review
// type. An entry whose finding does not decode is skipped rather than failing
// the post: the document's keys were produced by this package, so a
// decode failure means the row was not written by this code.
func PostedFindings(entries []task.ReviewGateFinding) []ScoredFinding {
	out := make([]ScoredFinding, 0, len(entries))
	for _, entry := range entries {
		var f ScoredFinding
		if err := json.Unmarshal(entry.Finding, &f); err != nil {
			continue
		}
		out = append(out, f)
	}
	return out
}
