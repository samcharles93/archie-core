import type {
  ApplyState,
  ApplyStatusRow,
  ResourceDescriptor,
} from "../stores/control-plane.ts";

/** A resource's apply mode, as the control-plane catalog serves it. */
export type ApplyMode = ResourceDescriptor["apply_mode"];

/** Badge tones a row or banner can carry. */
export type ApplyTone = "ok" | "warn" | "danger" | "idle";

/** A change to a restart-required resource cannot apply in-process: only a
 * process restart carries it. Every other mode applies without one, so a
 * process reporting an older version is a reporting lag, not a restart owed. */
export function needsRestart(mode: ApplyMode): boolean {
  return mode === "restart-required";
}

/** Titles of restart-required resources whose processes have not all caught
 * up: the Restart pending banner's whole content. A live-apply resource is
 * never listed, however far behind a process reports. */
export function restartPendingTitles(
  resources: Pick<ResourceDescriptor, "kind" | "title" | "apply_mode">[],
  rowsFor: (kind: string) => ApplyStatusRow[],
): string[] {
  return resources
    .filter((resource) => needsRestart(resource.apply_mode))
    .filter((resource) =>
      rowsFor(resource.kind).some((row) => row.state === "pending-restart"),
    )
    .map((resource) => resource.title);
}

const LABELS: Record<ApplyState, string> = {
  running: "Running",
  "pending-restart": "Restart pending",
  failed: "Failed",
  unknown: "Not reporting",
  "not-reporting": "Never reported",
};

/** The wording for one row's state. The derived state is apply-mode-agnostic:
 * `pending-restart` only says the process reports an older version, and the
 * mode says whether that is a restart owed or a reporting lag. A live kind
 * reads as applied, and a lag never borrows the restart wording. */
export function applyStateLabel(state: ApplyState, applyMode: ApplyMode): string {
  if (applyMode === "live") {
    if (state === "running") return "Applied";
    if (state === "pending-restart") return "Behind";
  }
  return LABELS[state];
}

const TONES: Record<ApplyState, ApplyTone> = {
  running: "ok",
  "pending-restart": "warn",
  failed: "danger",
  unknown: "warn",
  "not-reporting": "idle",
};

/** The badge tone for one row's state. A live kind that is behind is not a
 * warning: nothing is broken and no restart is owed, so it stays neutral. */
export function applyStateTone(state: ApplyState, applyMode: ApplyMode): ApplyTone {
  if (state === "pending-restart" && applyMode === "live") return "idle";
  return TONES[state];
}
