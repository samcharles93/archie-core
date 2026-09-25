// A deliberate EventSource replacement does not inherit Last-Event-ID from
// its predecessor. Carry the last multi-topic token in the new URL instead.
export function streamURL(cursor: string, logs: boolean): string {
  const params = new URLSearchParams();
  if (cursor) params.set("since", cursor);
  if (logs) params.set("topics", "logs");
  return `/api/stream${params.size ? `?${params}` : ""}`;
}
