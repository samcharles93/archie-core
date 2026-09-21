// The status vocabulary the board filters by, as pure functions of the served
// catalog. The catalog is an argument rather than a module-level import: it
// arrives after the app boots, so anything that captured the freeze-dried
// defaults at import or at setup kept them for the life of the page -- which
// is how a shared link to a served-only status filtered nothing -- and there
// is no way to test that capture without being able to replace the catalog.
// A caller that passes the catalog from inside a `computed` re-derives when
// `/api/task-meta` lands.
import type { StatusMeta } from "@/lib/task-meta";

// The vocabulary is the server catalog, not a hand-synced copy: the set of
// known statuses comes straight from what the server serves, so a status added
// on the backend shows up here without a frontend change. "needs_you" is a UI
// pseudo-status (work waiting on a human), so it is prepended rather than
// stored in the catalog.
export function taskStatuses(catalog: StatusMeta[]): Set<string> {
  return new Set(["needs_you", ...catalog.map((status) => status.id)]);
}

/**
 * A ?status= value, kept only when the catalog knows it: the query string is
 * operator input, and an unknown status would filter the whole board away with
 * no control showing why.
 */
export function initialTaskFilter(requested: string | null | undefined, catalog: StatusMeta[]): string {
  return requested && taskStatuses(catalog).has(requested) ? requested : "";
}

/** taskMatchesStatus reports whether a task survives the status filter. */
export function taskMatchesStatus(
  task: { status?: string },
  status: string,
  catalog: StatusMeta[],
): boolean {
  if (!status) return true;
  if (status === "needs_you") return catalog.some((entry) => entry.needs_you && entry.id === (task.status ?? ""));
  return task.status === status;
}
