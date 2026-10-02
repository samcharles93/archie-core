/**
 * The retry worktree choice, as the dashboard builds it.
 *
 * The module exists so the wire shape, the disable rule and the
 * initial-selection rule are testable under the node module runner rather
 * than buried in the SFC. The choice the operator makes here must travel to
 * the daemon: the retry action carries it, the task row persists it, and the
 * daemon's worktree preparation reads it (taskstate.RetryMode).
 */

/** The catalog shape /api/task-meta serves for one retry mode. */
export interface RetryModeOption {
  id: string;
  label: string;
  description: string;
  default?: boolean;
  requires_branch?: boolean;
}

/** One option as the retry dialog renders it. */
export interface RetryChoice {
  id: string;
  label: string;
  description: string;
  disabled: boolean;
}

/**
 * retryChoices is the operator's options for one task. A mode that needs a
 * branch is disabled when the task has none: the server refuses that
 * combination, so the dialog never sends a request it knows will conflict.
 */
export function retryChoices(
  modes: readonly RetryModeOption[],
  branch: string | null | undefined,
): RetryChoice[] {
  const hasBranch = Boolean(branch && branch.trim());
  return modes.map((mode) => ({
    id: mode.id,
    label: mode.label,
    description: mode.description,
    disabled: Boolean(mode.requires_branch) && !hasBranch,
  }));
}

/**
 * initialRetryMode is the mode the dialog opens on: the task's own persisted
 * choice when the catalog still names it, otherwise the server's default.
 * Reading the persisted value is what keeps a remediation retry continuing
 * its branch rather than resetting over the pull request, while a first retry
 * still opens on the explicit refresh default.
 */
export function initialRetryMode(
  modes: readonly RetryModeOption[],
  persisted: string | null | undefined,
): string {
  if (persisted && modes.some((mode) => mode.id === persisted)) {
    return persisted;
  }
  return modes.find((mode) => mode.default)?.id ?? modes[0]?.id ?? "";
}

/**
 * initialRetryChoice is the option the dialog actually selects: the task's
 * persisted mode when it is offered and enabled, otherwise the first enabled
 * option. It exists so a persisted mode the task can no longer use -- say
 * continue_pushed_work on a task whose branch is gone -- cannot be submitted
 * as a preselected-but-disabled value.
 */
export function initialRetryChoice(
  choices: readonly RetryChoice[],
  persisted: string | null | undefined,
): string {
  const match = persisted
    ? choices.find((choice) => choice.id === persisted)
    : undefined;
  if (match && !match.disabled) return match.id;
  return choices.find((choice) => !choice.disabled)?.id ?? "";
}

/** retryPayload is the action body a retry posts for the chosen mode. */
export function retryPayload(mode: string): { retry_mode: string } {
  return { retry_mode: mode };
}
