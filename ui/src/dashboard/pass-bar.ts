import type { StatusKind } from "@/lib/status";

/**
 * The bar under a quality-gate row.
 *
 * The rate above the rows says what the gates do overall; a row has to say
 * whether *this* gate is working. Two decisions live here, so the row and any
 * test read the same rule: the width is the share of that gate's runs which got
 * through, not a pass/fail flip, and a failing gate is red even when it
 * delivered nothing -- which is what carries the failure when the bar has no
 * width to show.
 */

export interface PassBar {
  /** The share of the gate's runs that got through, 0-100. */
  pct: number;
  /** The fill's tone: passing, failing, or a gate that has run nothing. */
  kind: StatusKind;
  /** The track's tone: a failing gate with an empty fill draws the failure on
   * the track instead, rather than reading as a blank row. */
  track: StatusKind;
}

export function passBar(delivered: number, runs: number): PassBar {
  const total = Math.max(0, Math.trunc(runs) || 0);
  if (!total) return { pct: 0, kind: "idle", track: "idle" };
  const through = Math.min(Math.max(Math.trunc(delivered) || 0, 0), total);
  if (through >= total) return { pct: 100, kind: "ok", track: "idle" };
  return {
    pct: Math.round((through / total) * 100),
    kind: "danger",
    track: "danger",
  };
}
