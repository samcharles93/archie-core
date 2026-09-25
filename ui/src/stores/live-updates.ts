import { defineStore } from "pinia";
import {
  computed,
  onUnmounted,
  reactive,
  ref,
  watch,
  type ComputedRef,
} from "vue";

import { setAuthenticationStateHandler, subscribeEvents, type StreamFrame } from "../lib/api.ts";
import type { StatusKind } from "@/lib/status";
import { reconnectDelay, type StreamState } from "../lib/stream-state.ts";
import {
  resourcesForEvent,
  type LiveEvent,
  type LiveResource,
} from "./live-events.ts";

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
  let cursor = "";
  let logsReaders = 0;
  const listeners = new Map<string, Set<(data: unknown) => void>>();

  const streamKind: ComputedRef<StatusKind> = computed(() => {
    if (streamState.value === "live") return "ok";
    return streamState.value === "unavailable" ? "danger" : "warn";
  });

  function receive(raw: unknown): void {
    const event = raw as LiveEvent;
    activity.value = [event, ...activity.value].slice(0, 50);
    for (const resource of resourcesForEvent(event)) revisions[resource] += 1;
  }

  function handleFrame(frame: StreamFrame, lastEventId: string): void {
    if (lastEventId) cursor = lastEventId;
    if (frame.topic === "tasks") receive(frame.data);
    for (const listener of listeners.get(frame.topic) ?? []) listener(frame.data);
  }

  // The browser retries a dropped stream itself, but a non-200 answer (the
  // backend restarting behind a proxy) closes it for good; reopen it then, or
  // the page reads "Disconnected" until a reload.
  let retryTimer: ReturnType<typeof setTimeout> | undefined;
  let retryAttempt = 0;

  function connect(): void {
    clearTimeout(retryTimer);
    unsubscribe?.();
    streamState.value = "connecting";
    unsubscribe = subscribeEvents(handleFrame, (state) => {
      streamState.value = state;
      if (state === "live") {
        retryAttempt = 0;
        connectionRevision.value += 1;
      } else if (state === "unavailable") {
        retryTimer = setTimeout(connect, reconnectDelay(retryAttempt++));
      }
    }, cursor, logsReaders > 0);
  }

  function initialize(): void {
    if (unsubscribe) return;
    setAuthenticationStateHandler((required) => {
      authenticationRequired.value = required;
    });
    connect();
  }

  /** Subscriptions are local callbacks, not browser connections. Only Logs
   * changes the upstream topic set and intentionally reconnects the stream. */
  function subscribe(topic: string, listener: (data: unknown) => void): () => void {
    const callbacks = listeners.get(topic) ?? new Set<(data: unknown) => void>();
    callbacks.add(listener);
    listeners.set(topic, callbacks);
    if (topic === "logs" && ++logsReaders === 1) connect();
    return () => {
      callbacks.delete(listener);
      if (callbacks.size === 0) listeners.delete(topic);
      if (topic === "logs" && --logsReaders === 0) connect();
    };
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
    subscribe,
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
