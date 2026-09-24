/** How many events a mapping has resolved, as the mappings list shows it. */
export function matchCountLabel(mapping: { match_count?: number }): string {
  const count = mapping.match_count ?? 0;
  if (count === 0) return "Never matched";
  return `${count} ${count === 1 ? "event" : "events"}`;
}
