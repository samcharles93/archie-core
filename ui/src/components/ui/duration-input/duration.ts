export type DurationUnit = "ms" | "s" | "m" | "h";

const UNIT_MS: Record<string, number> = {
  ns: 1e-6,
  us: 1e-3,
  "µs": 1e-3,
  ms: 1,
  s: 1000,
  m: 60_000,
  h: 3_600_000,
};

export const DURATION_UNITS: DurationUnit[] = ["ms", "s", "m", "h"];

/** Parses a Go duration string ("1h0m0s", "1m30s", "500ms", "0") to milliseconds. */
export function parseGoDuration(input: string): number | null {
  const text = input.trim();
  if (text === "0") return 0;
  const sign = text.startsWith("-") ? -1 : 1;
  const body = text.replace(/^[-+]/, "");
  if (body === "") return null;
  const re = /(\d+(?:\.\d*)?|\.\d+)(ns|us|µs|ms|s|m|h)/gy;
  let total = 0;
  let consumed = 0;
  let match: RegExpExecArray | null;
  while ((match = re.exec(body)) !== null) {
    total += Number(match[1]) * UNIT_MS[match[2]];
    consumed = re.lastIndex;
  }
  if (consumed !== body.length) return null;
  return sign * total;
}

function trimFraction(n: number): string {
  return Number(n.toFixed(9)).toString();
}

/** Formats milliseconds the way Go's time.Duration.String does. */
export function formatGoDuration(ms: number): string {
  if (ms === 0) return "0s";
  const sign = ms < 0 ? "-" : "";
  let rest = Math.abs(ms);
  if (rest < 1000) return `${sign}${trimFraction(rest)}ms`;
  const h = Math.floor(rest / 3_600_000);
  rest -= h * 3_600_000;
  const m = Math.floor(rest / 60_000);
  rest -= m * 60_000;
  const s = `${trimFraction(rest / 1000)}s`;
  if (h > 0) return `${sign}${h}h${m}m${s}`;
  if (m > 0) return `${sign}${m}m${s}`;
  return `${sign}${s}`;
}

/** Picks the largest unit that expresses ms as a whole number. */
export function splitDuration(ms: number): { value: number; unit: DurationUnit } {
  for (const unit of ["h", "m", "s"] as const) {
    const size = UNIT_MS[unit];
    if (ms !== 0 && ms % size === 0) return { value: ms / size, unit };
  }
  if (ms === 0) return { value: 0, unit: "s" };
  return { value: ms, unit: "ms" };
}

export function joinDuration(value: number, unit: DurationUnit): number {
  return value * UNIT_MS[unit];
}
