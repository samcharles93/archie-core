/**
 * Changed files for one attempt, from `GET /api/tasks/{id}/changes`.
 *
 * The capture happens at the producer: archied records the diffstat at the
 * moment it commits or pushes an attempt's work. So the honest empty state is
 * "no capture was recorded for this attempt", never "no files changed" -- a run
 * that predates capture, or one that produced no commit, has no capture and
 * nothing here can turn that into a claim about the repository.
 */
import type { Capture, CaptureTotals, TaskRecord } from "./task-run";

// The change-status labels are served with the rest of the task vocabulary
// (/api/task-meta), keyed by the daemon's own Change* constants.
export { changeStatusLabel as fileStatusLabel } from "@/lib/task-meta";

/**
 * The forge links come from the same projection the task list uses: the task's
 * own repo/PR URLs, or the capture's if the server attached them. A link is
 * rendered only when there is a URL to render -- a capture with no forge
 * coordinates shows its owner/repo and PR number as text rather than a dead
 * link.
 */
export function captureLinks(capture: Capture | undefined, task: TaskRecord | null): { repo: string; pr: string } {
  const repo = capture?.repo_url || task?.repo_url || "";
  const samePR =
    task && String(task.pr_number ?? "") !== "" && String(task.pr_number) === String(capture?.pr_number ?? "");
  const pr = capture?.pr_url || (samePR ? task?.pr_url || "" : "");
  return { repo, pr };
}

export function totalsLabel(totals: CaptureTotals = {}): string {
  const files = Number(totals.files) || 0;
  const parts = [`${files} file${files === 1 ? "" : "s"}`];
  if (totals.additions) parts.push(`+${totals.additions}`);
  if (totals.deletions) parts.push(`−${totals.deletions}`);
  return parts.join(" · ");
}
