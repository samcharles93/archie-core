package workflow

import "strings"

// Kind is the routing category an issue's labels select.
//
// It is the single axis both the workflow registry and the event bus route
// on. Before this existed the label table was written twice -- once here as
// a switch in Route, once in the NATS package as a subject lookup -- so
// adding a label meant remembering both, and the event bus had to know what
// a forge label was.
type Kind string

const (
	KindBug       Kind = "bug"
	KindFeature   Kind = "feature"
	KindBootstrap Kind = "bootstrap"

	// KindDefault is the routing category for issues carrying no
	// recognised label.
	KindDefault Kind = "default"
)

// labelKinds maps a forge issue label to its routing kind. This is the one
// place the label vocabulary is defined.
var labelKinds = map[string]Kind{
	"bug":       KindBug,
	"feature":   KindFeature,
	"bootstrap": KindBootstrap,
}

// kindWorkflows names the registry entry each kind prefers. Bootstrap
// exercises the full pipeline deterministically (no LLM spend) -- invites,
// clone, push, PR, labels.
var kindWorkflows = map[Kind]string{
	KindBug:       "tdd",
	KindFeature:   "feasibility",
	KindBootstrap: "bootstrap",
}

// KindForLabels returns the routing kind for a set of issue labels. The
// first recognised label wins; an empty or unrecognised set is KindDefault.
func KindForLabels(labels []string) Kind {
	for _, kind := range KindsForLabels(labels) {
		return kind
	}
	return KindDefault
}

// KindsForLabels returns every recognised kind in label order.
//
// Route needs the full ordered set rather than just the first: when a task
// is labelled "bug,feature" and no "tdd" workflow is registered, it must
// still fall through to "feasibility" rather than to the default.
func KindsForLabels(labels []string) []Kind {
	var kinds []Kind
	for _, label := range labels {
		if kind, ok := labelKinds[strings.TrimSpace(label)]; ok {
			kinds = append(kinds, kind)
		}
	}
	return kinds
}

// SplitLabels parses the comma-separated label form used on store.Task,
// dropping blanks so "bug,," and a trailing comma do not yield empty labels.
func SplitLabels(raw string) []string {
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	labels := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			labels = append(labels, trimmed)
		}
	}
	if len(labels) == 0 {
		return nil
	}
	return labels
}
