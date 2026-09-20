import { computed, ref } from "vue";

import { ApiError, api } from "@/lib/api";
import type {
  ConfigView,
  DangerousActions,
  Lifecycle,
  UpdateStatus,
  VersionComponent,
} from "./types";

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

function isMissing(err: unknown): boolean {
  // 501 is a deployment that did not wire the capability, which is a
  // legitimate state and a different thing from a broken one.
  return err instanceof ApiError && err.status === 501;
}

// ── The configuration projection ────────────────────────────────────────────

export const config = ref<ConfigView | null>(null);
export const configError = ref<string | null>(null);

/** Whether this process can apply configuration changes. False means every row
 * renders as a value: the write routes answer 503, so a control would only
 * invite a failure. An unknown config (still loading, or unreadable) is not
 * read-only, so only an explicit false is treated as one. */
export const configEditable = computed(() => config.value?.editable !== false);

/** Nothing to render: either the read failed, or archied is running without a
 * config file wired into the dashboard. */
export const configUnavailable = computed(
  () => Boolean(configError.value) || Object.keys(config.value ?? {}).length === 0,
);

export async function loadConfig(): Promise<void> {
  try {
    config.value = await api.config<ConfigView>();
    configError.value = null;
  } catch (err) {
    configError.value = errorText(err);
  }
}

// ── Updates ─────────────────────────────────────────────────────────────────

export const versionComponents = ref<VersionComponent[]>([]);
export const versionError = ref<string | null>(null);
/** Update checking is not wired on this deployment. */
export const versionMissing = ref(false);

export async function loadVersion(): Promise<void> {
  try {
    const data = await api.version<{ components?: VersionComponent[] }>();
    versionComponents.value = data?.components || [];
    versionError.value = null;
    versionMissing.value = false;
  } catch (err) {
    versionMissing.value = isMissing(err);
    if (!versionMissing.value) versionError.value = errorText(err);
  }
}

export const update = ref<UpdateStatus | null>(null);

export async function loadUpdate(): Promise<void> {
  try {
    update.value = await api.chatUpdate<UpdateStatus>();
  } catch (err) {
    // Absent rather than failed on a deployment that did not wire it: nothing
    // to show, not an error banner (archie-core-tf20).
    update.value = isMissing(err) ? null : { error: errorText(err) };
  }
}

export async function deferUpdate(snapshot: unknown): Promise<void> {
  await api.chatUpdateDefer(snapshot);
  await loadUpdate();
}

export async function installUpdate(snapshot: unknown): Promise<void> {
  await api.chatUpdateInstall(snapshot);
  await loadUpdate();
}

// ── Dangerous actions ───────────────────────────────────────────────────────

export const dangerous = ref<DangerousActions | null>(null);

export async function loadDangerous(): Promise<void> {
  try {
    dangerous.value = await api.chatDangerous<DangerousActions>();
  } catch (err) {
    dangerous.value = isMissing(err) ? null : { error: errorText(err) };
  }
}

export async function requestDangerous(kind: string, spec: unknown): Promise<void> {
  await api.chatDangerousRequest(kind, spec);
  await loadDangerous();
}

export async function decideDangerous(id: string, decision: string): Promise<void> {
  await api.chatDangerousDecision(id, decision);
  await loadDangerous();
}

// ── Work lifecycle ──────────────────────────────────────────────────────────

export const lifecycle = ref<Lifecycle | null>(null);
export const lifecycleError = ref<string | null>(null);

export async function loadLifecycle(): Promise<void> {
  try {
    lifecycle.value = await api.taskMeta<Lifecycle>();
    lifecycleError.value = null;
  } catch (err) {
    lifecycleError.value = errorText(err);
  }
}
