import { computed, ref } from "vue";

import { api } from "@/lib/api";
import type { ConfigView } from "./types";

/**
 * The System pages' shared state.
 *
 * Six routes read the same configuration projection, and several of them read
 * more than one endpoint, so the data lives in a module rather than being
 * threaded down through a chain of props. Each page calls the loaders it needs
 * and owns when; the cards read the refs.
 */

/** How archied's own handlers report a failure: a plain-text reason in the
 * body, surfaced by lib/api as the Error's message. */
export function errorText(err: unknown): string {
  return String((err as Error).message || err);
}

// ── The configuration projection ────────────────────────────────────────────

export const config = ref<ConfigView | null>(null);
export const configError = ref<string | null>(null);

/** Nothing to render: either the read failed, or archied is running without a
 * config file wired into the dashboard. */
export const configUnavailable = computed(
  () =>
    Boolean(configError.value) || Object.keys(config.value ?? {}).length === 0,
);

export async function loadConfig(): Promise<void> {
  try {
    config.value = await api.config<ConfigView>();
    configError.value = null;
  } catch (err) {
    configError.value = errorText(err);
  }
}

