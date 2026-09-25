import { computed, onMounted, onUnmounted, reactive, ref, watch } from "vue";

import { api } from "@/lib/api";
import { useLiveUpdatesStore } from "@/stores/live-updates";
import type { LogEntry } from "@/lib/log";
import type { StatusKind } from "@/lib/status";
import type { StreamState } from "@/lib/stream-state";
import {
  entryKey,
  matchesFilters,
  mergeEntries,
  type LogFilters,
} from "./log-entries";
import { logsEmptyDetail, logsEmptyTitle } from "./logs-empty";

/**
 * The log page's shared state.
 *
 * The filters, the stream's state and the list itself are read by different
 * components, so it lives in a module rather than being threaded down through a
 * chain of props.
 */

/** One page of the durable log, as GET /api/logs answers it. */
interface LogHistory {
  entries?: LogEntry[];
  components?: string[];
  file?: string;
  truncated?: boolean;
  /** The process serving the request keeps no durable history. */
  disabled?: boolean;
}

export const filters = reactive<LogFilters>({
  level: "",
  component: "",
  q: "",
});

/** The components the filter can name: those the server found, plus any the
 * live stream has shown since. */
export const componentOptions = ref<string[]>([]);
export const paused = ref(false);
export const loading = ref(true);
export const readError = ref<string | null>(null);
export const streamState = ref<StreamState | "connecting">("connecting");

const historyEntries = ref<LogEntry[]>([]);
const durableUnavailable = ref(false);
const truncated = ref(false);
const logFile = ref("");

// Live entries are held in a plain Map so a line the stream repeats is stored
// once, and the tick is what makes that mutation visible to `entries` below --
// a Map is not something a computed can depend on by being read.
const liveEntries = new Map<string, LogEntry>();
const liveTick = ref(0);

export const entries = computed(() => {
  void liveTick.value;
  return mergeEntries(historyEntries.value, liveEntries, filters);
});

/** What the list is showing. Every string here is a statement about this
 * process: a failed read, a read in flight, a deployment with no durable
 * history, or the file the lines came from. */
export const meta = computed(() => {
  if (readError.value) return "Cannot read logs";
  if (loading.value) return "Refreshing…";
  if (durableUnavailable.value) return "Live only";
  if (truncated.value)
    return `showing the most recent matches from ${logFile.value}`;
  return logFile.value;
});

/** The stream's state as a badge kind. Never `ok` for a stream that is still
 * connecting, and never `warn` for one that has given up: the two are
 * different answers, and stream-state.ts is what tells them apart. */
export const streamKind = computed<StatusKind>(() => {
  if (streamState.value === "live") return "ok";
  return streamState.value === "unavailable" ? "danger" : "warn";
});

/** Which of the nothing-to-show situations the list is in. */
export const emptyTitle = computed(() =>
  logsEmptyTitle(durableUnavailable.value, streamState.value),
);
export const emptyDetail = computed(() =>
  logsEmptyDetail(durableUnavailable.value, streamState.value),
);

export async function loadLogs(): Promise<void> {
  loading.value = true;
  readError.value = null;
  try {
    const res = await api.logs<LogHistory>({
      level: filters.level,
      component: filters.component,
      q: filters.q,
      limit: 500,
    });
    componentOptions.value = res.components ?? [];
    durableUnavailable.value = !!res.disabled;
    truncated.value = !!res.truncated;
    logFile.value = res.file ?? "";
    historyEntries.value = res.entries ?? [];
  } catch (err) {
    // The server's own message is the only part of a failure an operator can
    // act on, so it is what the list says instead of the lines.
    readError.value = (err as Error).message || String(err);
  } finally {
    loading.value = false;
  }
}

function handleEntry(raw: unknown): void {
  // Paused drops what arrives rather than buffering it: the list is a view of
  // the stream, and resuming continues from where the stream is now instead of
  // replaying the pause.
  if (paused.value) return;

  const entry = raw as LogEntry;
  if (!matchesFilters(entry, filters)) return;

  liveEntries.set(entryKey(entry), entry);
  const component = entry.fields?.component;
  if (
    typeof component === "string" &&
    component &&
    !componentOptions.value.includes(component)
  ) {
    componentOptions.value = [...componentOptions.value, component];
  }
  liveTick.value += 1;
}

let unsubscribe: (() => void) | undefined;

/** Owns the initial read, the re-reads a filter change causes, and the live
 * stream, for the page's lifetime. */
export function useLogs(): void {
  const live = useLiveUpdatesStore();
  onMounted(() => {
    // A mount opens a new stream against a fresh read, so the tail starts
    // unpaused, connecting, and without the lines the previous visit's stream
    // left behind. The filters are not reset: they are what the operator last
    // asked to see, and the read below answers them again.
    paused.value = false;
    streamState.value = "connecting";
    liveEntries.clear();
    liveTick.value += 1;

    void loadLogs();
    unsubscribe = live.subscribe("logs", handleEntry);
    streamState.value = live.streamState;
  });
  onUnmounted(() => {
    unsubscribe?.();
    unsubscribe = undefined;
  });
  watch(() => live.streamState, (state) => {
    streamState.value = state;
  });
  // A process without a LogFeed cannot deliver live entries.
  const stopStatus = live.subscribe("logs-status", (data) => {
    if (!(data as { available: boolean }).available) streamState.value = "unavailable";
  });
  onUnmounted(stopStatus);

  // Every filter is a server-side one -- the request carries it -- so a change
  // to any of them is a new read, not a re-filter of what is already here.
  watch(filters, () => void loadLogs());
}
