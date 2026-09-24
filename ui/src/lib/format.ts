/**
 * Display formatting shared across the dashboard pages. Pure functions: no
 * DOM, no framework.
 */

/** Humanised relative time: "2 min ago". Raw timestamps make people do maths. */
export function ago(value: string | number | Date | null | undefined): string {
  if (!value) return "—";
  const then = value instanceof Date ? value : new Date(value);
  const secs = Math.floor((Date.now() - then.getTime()) / 1000);
  if (!Number.isFinite(secs)) return "—";
  if (secs < 45) return "just now";
  const units: Array<[string, number]> = [
    ["min", 60],
    ["hr", 3600],
    ["day", 86400],
    ["wk", 604800],
  ];
  let label = "min";
  let size = 60;
  for (const [l, s] of units) {
    if (secs >= s) [label, size] = [l, s];
  }
  const n = Math.floor(secs / size);
  return `${n} ${label}${n === 1 ? "" : "s"} ago`;
}

/** Compact number: 2_400_000 -> "2.4M". Long digit strings do not scan. */
export function compact(n: number | null | undefined): string {
  if (n == null || Number.isNaN(n)) return "—";
  return new Intl.NumberFormat(undefined, {
    notation: "compact",
    maximumFractionDigits: 1,
  }).format(n);
}
