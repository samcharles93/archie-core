import { defineStore } from "pinia";
import {
  computed,
  onUnmounted,
  reactive,
  ref,
  watch,
  type ComputedRef,
} from "vue";

import { setAuthenticationStateHandler, subscribeEvents } from "@/lib/api";
import type { StatusKind } from "@/lib/status";
import type { StreamState } from "@/lib/stream-state";
import {
  resourcesForEvent,
  type LiveEvent,
  type LiveResource,
} from "./live-events";

export const useLiveUpdatesStore = defineStore("live-updates", () => {
  const streamState = ref<StreamState | "connecting">("connecting");
  const connectionRevision = ref(0);
  const authenticationRequired = ref(false);
  const activity = ref<LiveEvent[]>([]);
  const revisions = reactive<Record<LiveResource, number>>({
    tasks: 0,
    captures: 0,
    curators: 0,
    skills: 0,
    updates: 0,
  });
  let unsubscribe: (() => void) | undefined;

  const streamKind: ComputedRef<StatusKind> = computed(() => {
    if (streamState.value === "live") return "ok";
    return streamState.value === "unavailable" ? "danger" : "warn";
  });

  function receive(raw: unknown): void {
    const event = raw as LiveEvent;
    activity.value = [event, ...activity.value].slice(0, 50);
    for (const resource of resourcesForEvent(event)) revisions[resource] += 1;
  }

  function initialize(): void {
    if (unsubscribe) return;
    setAuthenticationStateHandler((required) => {
      authenticationRequired.value = required;
    });
    unsubscribe = subscribeEvents(receive, (state) => {
      streamState.value = state;
      if (state === "live") connectionRevision.value += 1;
    });
  }

  return {
    activity,
    authenticationRequired,
    connectionRevision,
    revisions,
    streamKind,
    streamState,
    initialize,
    receive,
  };
});

/** Re-read one mounted projection when SSE says its backend resource changed. */
export function useLiveResource(
  resource: LiveResource | null,
  load: () => void,
  debounceMs = 100,
): void {
  const live = useLiveUpdatesStore();
  let timer: ReturnType<typeof setTimeout> | undefined;
  const revision = () =>
    resource === null
      ? live.connectionRevision
      : [live.revisions[resource], live.connectionRevision];
  const stop = watch(revision, () => {
    clearTimeout(timer);
    timer = setTimeout(load, debounceMs);
  });
  onUnmounted(() => {
    clearTimeout(timer);
    stop();
  });
}
