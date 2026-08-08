export const SETUP_DISMISSAL_KEY = "archie.setup-complete.dismissed.v1";

// setupPanelState reconciles persisted presentation state with current setup
// truth. If setup later becomes incomplete, an old dismissal is cleared so
// the operator cannot miss the new work.
export function setupPanelState(setup, storage = globalThis.localStorage) {
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

export function dismissSetupComplete(storage = globalThis.localStorage) {
  storage?.setItem?.(SETUP_DISMISSAL_KEY, "1");
}
