import { computed, onMounted, onUnmounted, ref } from "vue";

import { api, subscribeEvents } from "@/lib/api";
import type { StatusKind } from "@/lib/status";
import type { StreamState } from "@/lib/stream-state";

/**
 * The event inspector's shared state.
 *
 * The list, the stream's state beside it and the sheet that shows one
 * capture's payload all read it, so it lives in a module rather than being
 * threaded down through a chain of props.
 */

/**
 * One captured inbound event as the daemon stored it. Headers and body are
 * already redacted (webhookguard.RedactPayload runs before InsertCapture), so
 * what the page prints is what the store holds.
 */
export interface Capture {
  id: number;
  received_at?: string;
  source?: string;
  remote_addr?: string;
  content_type?: string;
  headers?: string;
  body?: string;
  authenticated?: boolean;
}

interface CapturesResponse {
  captures?: Capture[];
  enabled?: boolean;
}

// How many captures the list asks for. This is a recent-events window, not a
// log: retention and the disk bound are the daemon's (see
// docs/prds/event-capture-storage.md), so an operator looking for something
// older is looking in the wrong place rather than scrolling further.
const LIST_LIMIT = 100;

export const captures = ref<Capture[]>([]);
export const enabled = ref(true);
export const error = ref<string | null>(null);
export const loading = ref(true);
export const streamState = ref<StreamState | "connecting">("connecting");

/**
 * The open row's capture, or null. Held by value rather than by id: the list
 * is re-read on every capture event, so resolving an id again would close the
 * sheet under an operator reading a capture that has just aged out of the
 * window.
 */
export const selected = ref<Capture | null>(null);

/** The stream's state as a badge kind. A stream still connecting and one that
 * has given up are different answers, so they do not share a tone. */
export const streamKind = computed<StatusKind>(() => {
  if (streamState.value === "live") return "ok";
  return streamState.value === "unavailable" ? "danger" : "warn";
});

/**
 * load re-reads the list. It runs once on mount and again on every capture
 * event, so it deliberately never re-enters the loading state: the skeletons
 * are for a page with nothing on it yet, and a live refresh that put them back
 * would replace the rows an operator is reading several times a minute.
 */
export async function load(): Promise<void> {
  try {
    const res = await api.captures<CapturesResponse>(LIST_LIMIT);
    enabled.value = res.enabled !== false;
    captures.value = res.captures || [];
    error.value = null;
  } catch (err) {
    // The server's own message is the only part of a failure an operator can
    // act on, so it is what the list says instead of the captures.
    error.value = (err as Error).message || String(err);
  } finally {
    loading.value = false;
  }
}

function handleEvent(raw: unknown): void {
  // The "capture" event on /events carries the id and source only, no payload
  // (see handleCapture), so it is an invalidation signal rather than data: it
  // says a capture exists, and the authoritative list -- the only place the
  // redacted payload lives -- is asked for again. Merging it into local state
  // instead would put a row in the list with nothing behind it.
  if ((raw as { kind?: string } | null)?.kind !== "capture") return;
  void load();
}

let unsubscribe: (() => void) | undefined;

/** Owns the initial read and the live stream that invalidates it, for the
 * page's lifetime. */
export function useCaptures(): void {
  onMounted(() => {
    void load();
    unsubscribe = subscribeEvents(handleEvent, (state) => {
      streamState.value = state;
    });
  });
  onUnmounted(() => unsubscribe?.());
}
