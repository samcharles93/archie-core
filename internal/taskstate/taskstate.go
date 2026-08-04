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

import "fmt"

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
// Work that is still progressing can be declined -- including a running task,
// which is the case most worth being able to stop. Callers that can interrupt
// running work should do so before recording the state, or the task may write
// its own outcome afterwards and win.
//
// Parked is refused along with the terminal states. A parked task is not
// progressing, so there is nothing to stop; it is retried, or left. Both the
// dashboard and chat already behaved this way and this package preserves it
// rather than widening the rule while unifying it. If declining a parked task
// is ever wanted, change it here and both paths get it at once -- which is
// the point.
func CheckDecline(status string) error {
	if Terminal(status) || status == Parked {
		return fmt.Errorf("task is already %s", status)
	}
	return nil
}
