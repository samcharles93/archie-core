import { ref } from "vue";

import { api } from "@/lib/api";
import { useLiveResource } from "@/stores/live-updates";
import { loadEventTypes } from "./event-type-state";

/**
 * The event inspector's shared state.
 *
 * The list, the stream's state beside it and the detail pane that shows one
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
  /** The event type it was identified as on arrival; empty is unidentified. */
  event_type?: string;
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

/**
 * The selected capture, or null. Held by value rather than by id: the list is
 * re-read on every capture event, so resolving an id again would blank the
 * pane under an operator reading a capture that has just aged out of the
 * window.
 */
export const selected = ref<Capture | null>(null);

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

/** selectById restores the selection the URL names, if the window still holds
 * it. A capture that has aged out cannot be resolved: the list is the only
 * read of a capture's body, so an id it no longer carries has nothing to show.
 * Reports whether it found one. */
export function selectById(id: number): boolean {
  const found = captures.value.find((capture) => capture.id === id);
  if (found) selected.value = found;
  return Boolean(found);
}

/** selectNewest fills a page opened without a selection, so the pane is useful
 * on arrival rather than an empty box beside the list. */
export function selectNewest(): void {
  if (captures.value.length) selected.value = captures.value[0];
}

/** Owns the live stream that invalidates the list, for the page's lifetime.
 * The initial read belongs to the page, which has to await it before it can
 * restore a selection from the address bar. */
export function useCaptures(): void {
  useLiveResource("captures", () => {
    void load();
    void loadEventTypes();
  });
}
