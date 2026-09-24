/** One setup step as the daemon reports it. */
export interface SetupStep {
  title: string;
  detail?: string;
  done: boolean;
}

export interface Setup {
  steps: SetupStep[];
  operator?: string;
}

export interface SetupPanelState {
  /** omit: nothing to show — no setup data, or setup fully done. */
  kind: "omit" | "incomplete";
  remaining: SetupStep[];
}

// setupPanelState distinguishes the only two states worth rendering: there is
// setup work left (the checklist), or there is not (nothing — a configured
// daemon is the absence of a problem, not a card announcing it).
export function setupPanelState(
  setup: Setup | null | undefined,
): SetupPanelState {
  const remaining = (setup?.steps ?? []).filter((step) => !step.done);
  if (!setup?.steps?.length || !remaining.length) {
    return { kind: "omit", remaining: [] };
  }
  return { kind: "incomplete", remaining };
}
