/** Per-stage timing and failure data, and the two readings the page shows. */

export interface StageStats {
  workflow: string;
  stage: string;
  runs: number;
  errors: number;
  avg_ms?: number;
}

/** Stages by average duration, slowest first. */
export function slowestStages(stages: StageStats[]): StageStats[] {
  return [...stages].sort((a, b) => (b.avg_ms || 0) - (a.avg_ms || 0));
}

/** Only the stages that have failed, and by how much. A stage that has never
 * errored is not a zero-length row here, it is absent. */
export function failingStages(stages: StageStats[]): StageStats[] {
  return [...stages].filter((s) => s.errors > 0).sort((a, b) => b.errors - a.errors);
}

/** The largest duration in the set, used to scale the bars. */
export function maxStageMs(stages: StageStats[]): number {
  return Math.max(...stages.map((s) => s.avg_ms || 0), 1);
}
