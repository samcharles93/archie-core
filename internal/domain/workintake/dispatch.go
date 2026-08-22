package workintake

// This file is the single definition of what a dispatch trigger means: which
// triggers match on a label, and whether a concrete issue (its labels and
// assignees) is eligible work under a trigger. The daemon's poller and the
// forge webhook intake both consult it, so the two cannot drift -- the
// empty-label guard here is the one that previously regressed (3209a1f: an
// empty label matches every open issue via the forge API).

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

// containsAssignee matches logins case-insensitively: GitHub usernames are
// unique case-insensitively, and AssignedIssues' server-side assignee filter
// matches the same way -- an exact-case comparison here would silently miss
// what the poller finds whenever the configured bot_user's case differs from
// the login GitHub reports on the event.
func containsAssignee(assignees []string, botUser string) bool {
	return slices.ContainsFunc(assignees, func(a string) bool {
		return strings.EqualFold(a, botUser)
	})
}
