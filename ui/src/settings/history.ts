interface HistoryEntry {
  record_key: string;
  actor: string;
  at?: string;
}

export interface HistoryFilter {
  /** Resource kinds to keep; empty or absent keeps every kind. */
  kinds?: string[];
  actor?: string;
  /** Keep entries newer than this many milliseconds before now. */
  sinceMs?: number;
}

/** filterHistory keeps the audit entries that match every filter set. */
export function filterHistory<T extends HistoryEntry>(
  entries: T[],
  filter: HistoryFilter,
  now: number,
): T[] {
  return entries.filter((entry) => {
    if (filter.kinds?.length && !filter.kinds.includes(entry.record_key)) return false;
    if (filter.actor && entry.actor !== filter.actor) return false;
    if (filter.sinceMs !== undefined) {
      if (!entry.at) return false;
      if (now - Date.parse(entry.at) > filter.sinceMs) return false;
    }
    return true;
  });
}
