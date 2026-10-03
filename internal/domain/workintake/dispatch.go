package workintake

import (
	"slices"
	"strings"
)

// RequiresLabel reports whether the dispatch trigger matches on a label and
// therefore needs a non-empty label to be meaningful.
func RequiresLabel(trigger string) bool {
	return trigger == "label" || trigger == "either"
}

// MatchesDispatch reports whether an issue is eligible work under the dispatch
// trigger. labels and assignees are the issue's current labels and assignee
// logins. An empty label never matches, even when the trigger is "label":
// a missing label must not be mistaken for "every issue" (3209a1f).
func MatchesDispatch(trigger, label, botUser string, labels, assignees []string) bool {
	switch trigger {
	case "label":
		return label != "" && containsLabel(labels, label)
	case "either":
		return containsAssignee(assignees, botUser) || (label != "" && containsLabel(labels, label))
	default: // "assignee"
		return containsAssignee(assignees, botUser)
	}
}

// containsLabel matches label names exactly: GitHub label names are
// case-sensitive and "Bug" and "bug" can coexist as distinct labels, matching
// IssuesWithLabel's exact-match server-side filter.
func containsLabel(labels []string, needle string) bool {
	return slices.Contains(labels, needle)
}

// containsAssignee matches logins case-insensitively.
func containsAssignee(assignees []string, botUser string) bool {
	return slices.ContainsFunc(assignees, func(a string) bool {
		return strings.EqualFold(a, botUser)
	})
}
