package tools

// ListLimit resolves a limit value from a tool input map, falling back to
// def for anything absent or non-positive and clamping to at most max.
//
// JSON numbers decode as float64, but a model that emits an integer through
// a different provider path can arrive as int, so both are accepted.
func ListLimit(input map[string]any, def, maxLimit int) int {
	var limit int
	switch v := input["limit"].(type) {
	case float64:
		limit = int(v)
	case int:
		limit = v
	}
	if limit <= 0 {
		return def
	}
	if limit > maxLimit {
		return maxLimit
	}
	return limit
}
