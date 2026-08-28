// Package taskstate owns the task lifecycle vocabulary and the rules for
// operator-initiated actions on a task.
//
// It exists because those rules had two implementations. The dashboard and
// chat both offer approve and reject, and they had drifted: rejecting from
// the dashboard left a task ClosedWontDo, cancelling the same task from chat
// left it Rejected. Two operators looking at the same queue saw different
// states for the same decision, and Rejected -- which the PR reconciler uses
// for "the pull request was closed without merging" -- no longer meant one
// thing.
//
// internal/gateway deliberately does not import internal/store, so it kept a
// hand-synced copy of the status strings with a comment asking future editors
// to keep them aligned. That is the same defect one level down. This package
// has no dependencies, so both can import it and the copy can go.
package taskstate

import (
	"fmt"
	"slices"
)

// Action is an operator control whose availability is determined solely by a
// task's persisted lifecycle state. The Web API returns these values with each
// task so every presentation surface derives from this table instead of
// keeping its own copy.
type Action string

const (
	ActionCancel    Action = "cancel"
	ActionStop      Action = "stop"
	ActionApprove   Action = "approve"
	ActionReject    Action = "reject"
	ActionRetry     Action = "retry"
	ActionAbandon   Action = "abandon"
	ActionOpenPR    Action = "open_pr"
	ActionOpenIssue Action = "open_issue"
	ActionArchive   Action = "archive"
)

// Task lifecycle statuses. The store persists these strings, so they are
// part of the on-disk format: renaming one is a migration, not a rename.
const (
	Queued       = "queued"
	Running      = "running"
	WaitingHuman = "waiting_human"
	PROpen       = "pr_open"
	Merged       = "merged"
	Parked       = "parked"
	Dead         = "dead"

	// Rejected means the forge closed the pull request without merging it.
	// It is an outcome archie observed, not a decision an operator made --
	// use Declined for that.
	Rejected = "rejected"

	// Declined means an operator refused the work. Whether they said so from
	// the dashboard or from chat, the task lands here.
	Declined = "closed_wont_do"
)

// Terminal reports whether a status is an end state, from which no further
// work happens without an operator asking for it.
func Terminal(status string) bool {
	switch status {
	case Merged, Rejected, Dead, Declined:
		return true
	default:
		return false
	}
}

// Actions returns the ordered controls valid for status. Callers receive a
// fresh slice so presentation code cannot mutate the lifecycle table.
//
// Reject is deliberately present in every non-terminal state: an operator can
// refuse work no matter where it currently sits, and the refusal always lands
// in the terminal Declined state. Terminal states are already resolved, so
// they only expose archive.
func Actions(status string) []Action {
	var actions []Action
	switch status {
	case Queued:
		actions = []Action{ActionCancel, ActionReject}
	case Running:
		actions = []Action{ActionStop, ActionReject}
	case WaitingHuman:
		actions = []Action{ActionApprove, ActionReject}
	case Parked:
		actions = []Action{ActionRetry, ActionAbandon, ActionReject}
	case PROpen:
		actions = []Action{ActionOpenPR, ActionOpenIssue, ActionReject}
	case Merged, Rejected, Dead, Declined:
		actions = []Action{ActionArchive}
	}
	return actions
}

// CheckAction rejects stale, illegal and unknown operator controls.
func CheckAction(status string, action Action) error {
	if slices.Contains(Actions(status), action) {
		return nil
	}
	return fmt.Errorf("action %q is not available while task is %s", action, status)
}

// CheckApprove reports whether a task in this status can be approved.
//
// Approval releases work that is waiting on a human decision, so it is only
// meaningful from WaitingHuman. Approving anything else would either restart
// finished work or race a task that is already running.
func CheckApprove(status string) error {
	if status != WaitingHuman {
		return fmt.Errorf("task is %s, not awaiting approval", status)
	}
	return nil
}

// CheckRetry reports whether a task in this status can be retried.
//
// Only a parked task can be retried: parking is how archie says "this failed
// in a way a human might fix". A dead task has spent its retries and needs
// the cap raised or the work re-filed, not another attempt.
func CheckRetry(status string) error {
	if status != Parked {
		return fmt.Errorf("task is %s, not parked", status)
	}
	return nil
}

// CheckDecline reports whether a task in this status can be declined.
//
// The legacy chat command expresses cancel, stop, reject and abandon as one
// operation. Because Reject is now offered in every non-terminal state, that
// command is available everywhere a task is still live; unknown and terminal
// states fail closed instead of permitting an arbitrary transition.
func CheckDecline(status string) error {
	for _, action := range Actions(status) {
		switch action {
		case ActionCancel, ActionStop, ActionReject, ActionAbandon:
			return nil
		}
	}
	return fmt.Errorf("task cannot be declined while %s", status)
}
