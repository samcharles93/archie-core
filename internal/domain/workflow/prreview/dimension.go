package prreview

import (
	"cmp"
	"slices"
)

type Dimension struct {
	Name         string
	Prompt       string
	TargetFiles  []string
	ContextFiles []string
	Priority     float64
}

// MergeDimensions deduplicates dimensions by name, keeping the higher
// priority, sorts by priority and keeps the first limit. Ties keep input
// order.
func MergeDimensions(lensOutputs [][]Dimension, limit int) []Dimension {
	byName := make(map[string]Dimension)
	order := make([]string, 0)
	for _, lens := range lensOutputs {
		for _, d := range lens {
			existing, ok := byName[d.Name]
			if !ok {
				order = append(order, d.Name)
				byName[d.Name] = d
				continue
			}
			if d.Priority > existing.Priority {
				byName[d.Name] = d
			}
		}
	}
	merged := make([]Dimension, 0, len(order))
	for _, name := range order {
		merged = append(merged, byName[name])
	}
	slices.SortStableFunc(merged, func(a, b Dimension) int {
		return cmp.Compare(b.Priority, a.Priority)
	})
	if len(merged) > limit {
		merged = merged[:limit]
	}
	return merged
}

// Returns nil at or below AIGeneratedThreshold -- a coin-flip is not evidence,
// so it is not added.
func HallucinationDimension(aiGenerated float64) *Dimension {
	if aiGenerated <= AIGeneratedThreshold {
		return nil
	}
	return &Dimension{
		Name: "hallucination-check",
		Prompt: "This PR's description or commit messages read as likely machine-written. " +
			"Check every claim the PR makes -- what it says it does, any cited behaviour, API, or test result -- " +
			"against what the diff actually implements. Report a finding for any claim, citation, or described " +
			"behaviour that the diff does not actually carry out.",
		Priority: 1,
	}
}
