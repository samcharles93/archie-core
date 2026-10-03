package tools

// ListLimit returns input["limit"], defaulting to def and clamped to
// maxLimit. Accepts float64 and int.
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
