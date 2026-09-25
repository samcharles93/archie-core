/**
 * The delivered count: work that finished and left archied's hands.
 *
 * A workflow can end a run `completed` instead of `merged` -- a no-change
 * build, or a triage that needed no code -- so a surface that reads `merged`
 * alone renders zero for exactly the runs that had nothing to merge. Every
 * surface that reports delivered work counts both, through this one function.
 */
export interface DeliveredCounts {
  merged?: number;
  completed?: number;
}

/** delivered counts the runs that finished without needing a merge. */
export function delivered(counts: DeliveredCounts | null | undefined): number {
  return (counts?.merged || 0) + (counts?.completed || 0);
}
