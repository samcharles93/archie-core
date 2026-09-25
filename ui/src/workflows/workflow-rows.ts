// Merging workflow definitions with their run statistics.
//
// The rows used to come from the definition list alone, so a workflow that had
// run but was no longer in that list (or a definitions fetch that came back
// empty while the stats fetch succeeded) produced no rows at all. The page then
// showed "No workflow runs yet" directly above the stage tables listing those
// very runs (archie-core-rcbf).
//
// A run is evidence the workflow exists. Anything with either a definition or
// statistics gets a row.

/** A workflow's run statistics, as the stats endpoint reports them. */
export interface WorkflowStats {
  workflow: string;
  runs?: number;
  merged?: number;
  completed?: number;
  avg_tokens?: number;
  avg_steps?: number;
  origin?: string;
}

/** A workflow definition, as the definitions endpoint reports it. */
export interface WorkflowDefinition {
  id: string;
  name?: string;
  origin?: string;
  enabled?: boolean;
}

/** One table row: a definition, its statistics, or both. */
export interface WorkflowRow {
  id: string;
  name?: string;
  workflow: string;
  runs?: number;
  merged?: number;
  completed?: number;
  avg_tokens?: number;
  avg_steps?: number;
  origin?: string;
}

/**
 * workflowRows returns one row per known workflow, definitions first and in
 * their declared order, then any workflow seen only in the statistics.
 */
export function workflowRows(
  workflows: WorkflowStats[] | null | undefined,
  definitions: WorkflowDefinition[] | null | undefined,
): WorkflowRow[] {
  const stats = new Map((workflows || []).map((w) => [w.workflow, w]));
  const rows: WorkflowRow[] = [];
  const claimed = new Set<string>();

  for (const definition of definitions || []) {
    claimed.add(definition.id);
    rows.push({
      ...definition,
      ...(stats.get(definition.id) || {
        workflow: definition.id,
        runs: 0,
        merged: 0,
        completed: 0,
      }),
    });
  }

  for (const workflow of workflows || []) {
    if (claimed.has(workflow.workflow)) continue;
    // No definition to describe it, so the id is the only name it has.
    rows.push({ id: workflow.workflow, name: workflow.workflow, ...workflow });
  }

  return rows;
}
