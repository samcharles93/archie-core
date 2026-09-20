/** Naming and numbers shared by the workflow tables. */

const WORKFLOW_LABELS: Record<string, string> = {
  bootstrap: "Bootstrap",
  implement: "Implement",
  tdd: "TDD",
  feasibility: "Feasibility check",
};

/** A workflow's display name. An unknown one is still shown, not hidden: a new
 * workflow on the server should be visible before the UI knows its proper
 * name. */
export function workflowLabel(name: string | undefined | null): string {
  if (name && WORKFLOW_LABELS[name]) return WORKFLOW_LABELS[name];
  if (!name) return "Unknown";
  return name.charAt(0).toUpperCase() + name.slice(1).replace(/_/g, " ");
}

/** A percentage, or 0 when there is no whole to divide by. Accepts the absent
 * case because the stats endpoint omits a field it has no data for. */
export function pct(part: number | undefined | null, whole: number | undefined | null): number {
  if (!whole || !part) return 0;
  return Math.round((part / whole) * 100);
}

export function formatMs(ms: number | undefined | null): string {
  if (!ms) return "—";
  if (ms < 1000) return `${ms} ms`;
  const secs = ms / 1000;
  if (secs < 60) return `${secs.toFixed(1)} s`;
  return `${(secs / 60).toFixed(1)} min`;
}
