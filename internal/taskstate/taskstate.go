// Package taskstate defines task statuses and which operator actions each
// allows. It has no dependencies.
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
	ActionRereview  Action = "rereview"
	ActionRetry     Action = "retry"
	ActionAbandon   Action = "abandon"
	ActionMerge     Action = "merge"
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

	// Completed means a workflow that opens no pull request finished its
	// work, such as a run with no repository.
	Completed = "completed"
)

// Terminal reports whether a status is an end state, from which no further
// work happens without an operator asking for it.
func Terminal(status string) bool {
	switch status {
	case Merged, Rejected, Dead, Declined, Completed:
		return true
	default:
		return false
	}
}

// Actions returns the actions valid for status, as a new slice. Reject is
// available in every non-terminal state; terminal states offer archive.
func Actions(status string) []Action {
	var actions []Action
	switch status {
	case Queued:
		actions = []Action{ActionCancel, ActionReject}
	case Running:
		actions = []Action{ActionStop, ActionReject}
	case WaitingHuman:
		actions = []Action{ActionApprove, ActionReject, ActionRereview}
	case Parked:
		actions = []Action{ActionRetry, ActionAbandon, ActionReject}
	case PROpen:
		actions = []Action{ActionMerge, ActionOpenPR, ActionOpenIssue, ActionReject}
	case Merged, Rejected, Dead, Declined, Completed:
		actions = []Action{ActionArchive}
	}
	return actions
}

// ActionTarget returns the status an action moves a task to. Actions that
// change no status (archive, and the forge links) report false.
func ActionTarget(action Action) (string, bool) {
	switch action {
	case ActionApprove, ActionRetry, ActionRereview:
		return Queued, true
	case ActionStop:
		return Parked, true
	case ActionCancel, ActionReject, ActionAbandon:
		return Declined, true
	case ActionMerge:
		return Merged, true
	default:
		return "", false
	}
}

// CheckAction rejects stale, illegal and unknown operator controls.
func CheckAction(status string, action Action) error {
	if slices.Contains(Actions(status), action) {
		return nil
	}
	return fmt.Errorf("action %q is not available while task is %s", action, status)
}

// CheckApprove allows approval only from WaitingHuman.
func CheckApprove(status string) error {
	if status != WaitingHuman {
		return fmt.Errorf("task is %s, not awaiting approval", status)
	}
	return nil
}

// CheckRetry allows retry only from Parked.
func CheckRetry(status string) error {
	if status != Parked {
		return fmt.Errorf("task is %s, not parked", status)
	}
	return nil
}

// RetryMode is the worktree mode for a retried task's next dispatch. Stored
// on the task row.
type RetryMode string

const (
	// RetryRefreshOntoBase starts the retry from the current base branch,
	// discarding anything an earlier attempt committed on the task branch.
	// It is the default for work that is not an open pull request.
	RetryRefreshOntoBase RetryMode = "refresh_onto_base"
	// RetryContinuePushedWork resumes the branch the task already pushed, so
	// the retry continues the work an earlier attempt committed instead of
	// resetting over it. It requires a branch to resume.
	RetryContinuePushedWork RetryMode = "continue_pushed_work"
)

// NormalizeRetryMode maps a stored retry mode to the mode it means. Empty and
// unknown values fall back to RefreshOntoBase: a retry that carries no explicit
// choice resets onto base, which is the safe default for work that is not an
// open pull request.
func NormalizeRetryMode(mode string) RetryMode {
	if RetryMode(mode) == RetryContinuePushedWork {
		return RetryContinuePushedWork
	}
	return RetryRefreshOntoBase
}

// ResolveRetryMode validates a retry mode; "" means RetryRefreshOntoBase and
// unknown values are rejected.
func ResolveRetryMode(mode string) (RetryMode, bool) {
	switch RetryMode(mode) {
	case "", RetryRefreshOntoBase:
		return RetryRefreshOntoBase, true
	case RetryContinuePushedWork:
		return RetryContinuePushedWork, true
	default:
		return "", false
	}
}

// RetryModeMeta describes how to present a retry mode. The dashboard renders
// the operator's choice from this catalog rather than keeping its own copy.
type RetryModeMeta struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	Description string `json:"description"`
	Default     bool   `json:"default"`
	// RequiresBranch reports that the mode can only land when the task has a
	// branch to resume. The dashboard disables it otherwise; the action refuses
	// the same combination, so the UI check is a convenience, not the rule.
	RequiresBranch bool `json:"requires_branch"`
}

// RetryModes returns the presentation catalog for the retry modes, in display
// order. Callers receive a fresh slice.
func RetryModes() []RetryModeMeta {
	return []RetryModeMeta{
		{ID: string(RetryRefreshOntoBase), Label: "Refresh onto base", Description: "Start the retry from the current base branch, discarding work already pushed on this task's branch.", Default: true},
		{ID: string(RetryContinuePushedWork), Label: "Continue pushed work", Description: "Resume the branch this task already pushed, keeping its commits.", RequiresBranch: true},
	}
}

// CheckDecline allows declining any non-terminal task.
func CheckDecline(status string) error {
	for _, action := range Actions(status) {
		switch action {
		case ActionCancel, ActionStop, ActionReject, ActionAbandon:
			return nil
		}
	}
	return fmt.Errorf("task cannot be declined while %s", status)
}

// StatusMeta describes how to present a lifecycle status. It is the single
// source for the dashboard's label, pill severity and "needs you" grouping, so
// the frontend never has to keep a hand-synced copy of the vocabulary.
type StatusMeta struct {
	ID       string `json:"id"`
	Label    string `json:"label"`
	Kind     string `json:"kind"`      // pill severity: idle, info, ok, warn, danger
	NeedsYou bool   `json:"needs_you"` // counts toward the "Needs you" filter
}

// ActionMeta describes how to present an operator control. Confirmation text
// uses "{title}" as a placeholder for the task title.
type ActionMeta struct {
	ID      string `json:"id"`
	Label   string `json:"label"`
	Kind    string `json:"kind"`    // button variant: primary, quiet, danger, link
	Confirm string `json:"confirm"` // optional confirm prompt, may be ""
}

// Statuses returns the presentation catalog for every lifecycle status, in
// display order. Callers receive a fresh slice.
func Statuses() []StatusMeta {
	return []StatusMeta{
		{ID: Queued, Label: "Queued", Kind: "idle"},
		{ID: Running, Label: "Working", Kind: "info"},
		{ID: WaitingHuman, Label: "Waiting for you", Kind: "warn", NeedsYou: true},
		{ID: PROpen, Label: "In review", Kind: "ok"},
		{ID: Merged, Label: "Merged", Kind: "ok"},
		{ID: Completed, Label: "Done", Kind: "ok"},
		{ID: Parked, Label: "Parked", Kind: "warn", NeedsYou: true},
		{ID: Dead, Label: "Stopped (too many retries)", Kind: "danger"},
		{ID: Rejected, Label: "Rejected", Kind: "danger"},
		{ID: Declined, Label: "Won't do", Kind: "idle"},
	}
}

// ActionCatalog returns the presentation catalog for every operator control.
// Callers receive a fresh slice.
func ActionCatalog() []ActionMeta {
	return []ActionMeta{
		{ID: string(ActionCancel), Label: "Cancel", Kind: "quiet", Confirm: `Cancel "{title}"? This closes the forge issue.`},
		{ID: string(ActionStop), Label: "Stop", Kind: "primary", Confirm: `Stop "{title}"? Recoverable work will remain parked.`},
		{ID: string(ActionApprove), Label: "Approve", Kind: "primary"},
		{ID: string(ActionRereview), Label: "Re-review", Kind: "quiet"},
		{ID: string(ActionReject), Label: "Reject", Kind: "quiet", Confirm: `Reject "{title}"? This closes the forge issue.`},
		{ID: string(ActionRetry), Label: "Retry", Kind: "primary"},
		{ID: string(ActionAbandon), Label: "Abandon", Kind: "quiet", Confirm: `Abandon "{title}"? Archie stops working on it; the forge issue stays open.`},
		{ID: string(ActionArchive), Label: "Archive", Kind: "quiet", Confirm: `Archive the local record for "{title}"?`},
		{ID: string(ActionMerge), Label: "Merge", Kind: "primary", Confirm: `Merge the pull request for "{title}"?`},
		{ID: string(ActionOpenPR), Label: "Open PR", Kind: "link"},
		{ID: string(ActionOpenIssue), Label: "Open issue", Kind: "link"},
	}
}

// Park classes: the producer-side answer to "what kind of intervention does
// this park need?". The class is recorded at the park site, where the cause is
// known, and is never inferred later from reason text. It is part of the
// on-disk format.
const (
	// ParkNeedsHuman parks need an operator decision: gate failures,
	// review verdicts, diff caps, remediation round caps. It is the
	// default, so an unclassified park always reads as operator-actionable.
	ParkNeedsHuman ParkClass = "needs_human"
	// ParkTransient parks are environmental: storage, container pool,
	// grants, transport. Nothing about the work is wrong; a requeue can
	// succeed. Nothing retries them automatically yet -- the class is
	// recorded so a future retry pass has a safe, queryable input.
	ParkTransient ParkClass = "transient"
	// ParkTerminal parks cannot be retried into success: the repo left the
	// config, the owning identity retired. An operator archives these.
	ParkTerminal ParkClass = "terminal"
)

// ParkClass is the persisted park classification string.
type ParkClass = string

// NormalizeParkClass maps a park class written by a producer to the value
// the store persists. Empty and unknown fall back to NeedsHuman: the safe
// misread is "an operator should look at this", never "nothing to do".
func NormalizeParkClass(class string) ParkClass {
	switch class {
	case ParkTransient, ParkTerminal:
		return class
	default:
		return ParkNeedsHuman
	}
}
