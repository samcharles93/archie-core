// The freeze-dried /api/task-meta catalog the dashboard renders before the
// served one arrives, or when archied is unreachable; task-meta.ts replaces it
// with the live catalog.

/** A lifecycle status as the server describes it. */
export interface StatusMeta {
  id: string;
  label: string;
  kind: string;
  needs_you?: boolean;
}

/** An operator control as the server describes it. */
export interface ActionMeta {
  id: string;
  label: string;
  kind: string;
  confirm?: string;
}

/** A changes_captured file status as the server describes it. */
export interface ChangeStatusMeta {
  id: string;
  label: string;
}

/** A retry worktree mode as the server describes it. */
export interface RetryModeMeta {
  id: string;
  label: string;
  description: string;
  default?: boolean;
  requires_branch?: boolean;
}

export const DEFAULT_STATUSES: StatusMeta[] = [
  { id: "queued", label: "Queued", kind: "idle" },
  { id: "running", label: "Working", kind: "info" },
  {
    id: "waiting_human",
    label: "Waiting for you",
    kind: "warn",
    needs_you: true,
  },
  { id: "pr_open", label: "In review", kind: "ok" },
  { id: "merged", label: "Merged", kind: "ok" },
  { id: "completed", label: "Done", kind: "ok" },
  { id: "parked", label: "Parked", kind: "warn", needs_you: true },
  { id: "dead", label: "Stopped (too many retries)", kind: "danger" },
  { id: "rejected", label: "Rejected", kind: "danger" },
  { id: "closed_wont_do", label: "Won't do", kind: "idle" },
];

export const DEFAULT_ACTIONS: ActionMeta[] = [
  {
    id: "cancel",
    label: "Cancel",
    kind: "quiet",
    confirm: `Cancel "{title}"? This closes the forge issue.`,
  },
  {
    id: "stop",
    label: "Stop",
    kind: "primary",
    confirm: `Stop "{title}"? Recoverable work will remain parked.`,
  },
  { id: "approve", label: "Approve", kind: "primary" },
  { id: "rereview", label: "Re-review", kind: "quiet" },
  {
    id: "reject",
    label: "Reject",
    kind: "quiet",
    confirm: `Reject "{title}"? This closes the forge issue.`,
  },
  { id: "retry", label: "Retry", kind: "primary" },
  {
    id: "abandon",
    label: "Abandon",
    kind: "quiet",
    confirm: `Abandon "{title}"? Archie stops working on it; the forge issue stays open.`,
  },
  {
    id: "archive",
    label: "Archive",
    kind: "quiet",
    confirm: `Archive the local record for "{title}"?`,
  },
  {
    id: "merge",
    label: "Merge",
    kind: "primary",
    confirm: `Merge the pull request for "{title}"?`,
  },
  { id: "open_pr", label: "Open PR", kind: "link" },
  { id: "open_issue", label: "Open issue", kind: "link" },
];

export const DEFAULT_CHANGE_STATUSES: ChangeStatusMeta[] = [
  { id: "added", label: "Added" },
  { id: "modified", label: "Modified" },
  { id: "deleted", label: "Deleted" },
  { id: "renamed", label: "Renamed" },
  { id: "typechange", label: "Type changed" },
];

export const DEFAULT_RETRY_MODES: RetryModeMeta[] = [
  {
    id: "refresh_onto_base",
    label: "Refresh onto base",
    description:
      "Start the retry from the current base branch, discarding work already pushed on this task's branch.",
    default: true,
  },
  {
    id: "continue_pushed_work",
    label: "Continue pushed work",
    description:
      "Resume the branch this task already pushed, keeping its commits.",
    requires_branch: true,
  },
];

export const DEFAULT_CONFIG_SCHEMA = "archie/task-config@1";
