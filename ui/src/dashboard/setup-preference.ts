export const SETUP_DISMISSAL_KEY = "archie.setup-complete.dismissed.v1";

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

/** The storage surface this needs, so it is testable without a browser. */
export type SetupStorage = Pick<Storage, "getItem" | "removeItem" | "setItem">;

export interface SetupPanelState {
  kind: "omit" | "incomplete" | "complete" | "dismissed";
  remaining: SetupStep[];
}

// setupPanelState reconciles persisted presentation state with current setup
// truth. If setup later becomes incomplete, an old dismissal is cleared so the
// operator cannot miss the new work.
export function setupPanelState(
  setup: Setup | null | undefined,
  storage: SetupStorage | undefined = globalThis.localStorage,
): SetupPanelState {
  if (!setup?.steps?.length) return { kind: "omit", remaining: [] };
  const remaining = setup.steps.filter((step) => !step.done);
  if (remaining.length) {
    storage?.removeItem?.(SETUP_DISMISSAL_KEY);
    return { kind: "incomplete", remaining };
  }
  if (storage?.getItem?.(SETUP_DISMISSAL_KEY) === "1") {
    return { kind: "dismissed", remaining: [] };
  }
  return { kind: "complete", remaining: [] };
}

export function dismissSetupComplete(storage: SetupStorage | undefined = globalThis.localStorage): void {
  storage?.setItem?.(SETUP_DISMISSAL_KEY, "1");
}
