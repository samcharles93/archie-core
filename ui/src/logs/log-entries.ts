import type { LogEntry } from "@/lib/log";

/**
 * What the log list is made of: the filters an entry survives, the key that
 * makes one entry, and the merge of the durable history with the live tail.
 * Pure, so the page's rendering has nothing to decide about entries.
 */

/** The filters the list is read through. All three narrow the read
 * server-side as well as the live stream locally. */
export interface LogFilters {
  level: string;
  component: string;
  q: string;
}

/**
 * entryKey is the identity of a log line: its time, level, message and fields.
 * The live stream repeats entries the history also carries, so both sides key
 * on the same value and the duplicate is stored once.
 */
export function entryKey(entry: LogEntry): string {
  return `${entry.time || ""}|${entry.level || ""}|${entry.message || entry.msg || ""}|${JSON.stringify(entry.fields || {})}`;
}

/**
 * matchesFilters applies the filters to one entry. It runs over live entries as
 * they arrive and again over the merged list, so the level CSV is matched part
 * by part exactly as the server matches it (`splitCSV` in
 * internal/webui/api_tasks_logs.go).
 */
export function matchesFilters(entry: LogEntry, filters: LogFilters): boolean {
  if (filters.level && !filters.level.split(",").includes((entry.level || "").toUpperCase())) {
    return false;
  }
  if (filters.component && entry.fields?.component !== filters.component) return false;
  if (filters.q) {
    const needle = filters.q.toLowerCase();
    const hay = `${entry.message || entry.msg || ""} ${JSON.stringify(entry.fields || {})}`.toLowerCase();
    if (!hay.includes(needle)) return false;
  }
  return true;
}

/**
 * mergeEntries overlays the live tail on the durable history, keeps the last
 * 1000 that pass the filters, and returns them oldest first.
 *
 * A key the history already holds keeps its position when the live entry
 * replaces it, so a line both sources carry is not reordered; lines only ever
 * seen live land at the end, where the newest ones belong.
 */
export function mergeEntries(
  history: LogEntry[],
  live: Map<string, LogEntry>,
  filters: LogFilters,
): LogEntry[] {
  const byID = new Map<string, LogEntry>();
  for (const entry of history) byID.set(entryKey(entry), entry);
  for (const [id, entry] of live) byID.set(id, entry);
  return [...byID.values()].filter((entry) => matchesFilters(entry, filters)).slice(-1000);
}
