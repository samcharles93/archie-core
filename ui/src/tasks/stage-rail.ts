/**
 * What a stage status means, and what it does not.
 *
 * A stage fails when its Go function returned an error. There is NO exit code,
 * no pass/fail verdict, and no notion of "the work was correct" -- a stage that
 * returned nil after producing a poor diff is `ok`. So the rail never renders
 * a checkmark, a badge or an exit status; it renders the status word, the
 * duration and the error text, and says plainly what `ok` means. The agent's
 * own self-reported outcome (`agent_finish`) is shown separately, labelled as
 * the agent's report rather than as archie's verdict.
 */
import type { StatusKind } from "@/lib/status";

import { describeTimelineEvent, type TimelineLine } from "./timeline-event";
import type { TaskEvent } from "./task-run";

export interface StageStatusMeta {
  label: string;
  kind: StatusKind;
}

const STAGE_STATUS: Record<string, StageStatusMeta> = {
  ok: { label: "ok", kind: "ok" },
  failed: { label: "failed", kind: "danger" },
  interrupted: { label: "interrupted", kind: "warn" },
  running: { label: "running", kind: "info" },
  unknown: { label: "unknown", kind: "idle" },
};

export function stageStatusMeta(status?: string | null): StageStatusMeta {
  return STAGE_STATUS[status ?? ""] || { label: status || "unknown", kind: "idle" };
}

/**
 * agentReports returns the agent's own account of a stage: the `agent_finish`
 * events whose attempt and stage both match. Returning an array rather than one
 * value keeps a stage that ran two agents honest instead of showing the last.
 */
export function agentReports(
  events: TaskEvent[] | null | undefined,
  attemptNumber: number | null | undefined,
  stage: string | undefined,
): TimelineLine[] {
  return (events || [])
    .filter(
      (ev) =>
        ev &&
        ev.kind === "agent_finish" &&
        Number(ev.attempt) === Number(attemptNumber) &&
        (ev.stage || "") === (stage || ""),
    )
    .map((ev) => describeTimelineEvent(ev));
}
