package prreview

// Depth is how many review dimensions a run affords, driven by the size of
// the change.
type Depth string

const (
	DepthQuick    Depth = "quick"
	DepthStandard Depth = "standard"
	DepthDeep     Depth = "deep"
)

const (
	quickLinesCeiling    = 100
	standardLinesCeiling = 500
)

// The dimension caps each depth affords.
const (
	maxDimensionsQuick    = 3
	maxDimensionsStandard = 6
	maxDimensionsDeep     = 12
)

// ClassifyDepth derives a review depth from the number of changed lines
// (additions plus deletions).
func ClassifyDepth(changedLines int) Depth {
	switch {
	case changedLines < quickLinesCeiling:
		return DepthQuick
	case changedLines < standardLinesCeiling:
		return DepthStandard
	default:
		return DepthDeep
	}
}

// MaxDimensionsFor returns how many review dimensions a depth affords. An
// unrecognised depth gets the cheapest cap, never the most expensive: a typo
// or a stale value must not silently buy a deep run's budget.
func MaxDimensionsFor(depth Depth) int {
	switch depth {
	case DepthStandard:
		return maxDimensionsStandard
	case DepthDeep:
		return maxDimensionsDeep
	default:
		return maxDimensionsQuick
	}
}

// An empty explicit depth means no override was given.
func ResolveDepth(changedLines int, explicit Depth) Depth {
	if explicit != "" {
		return explicit
	}
	return ClassifyDepth(changedLines)
}
