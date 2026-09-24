// The status vocabulary the board filters by, as pure functions of the served
// catalog. The catalog is an argument rather than a module-level import: it
// arrives after the app boots, so anything that captured the freeze-dried
// defaults at import or at setup kept them for the life of the page -- which
// is how a shared link to a served-only status filtered nothing -- and there
// is no way to test that capture without being able to replace the catalog.
// A caller that passes the catalog from inside a `computed` re-derives when
// `/api/task-meta` lands.
//
// Only type imports, and only from "@/": `node --test` runs this file with
// --experimental-strip-types and resolves no tsconfig paths, so `import type`
// is erased before node ever sees the specifier. A value import from
// "@/lib/..." would kill the suite with ERR_MODULE_NOT_FOUND that names
// nothing in this package, so a value that has to cross this boundary is
// either an argument (the catalog, below) or a relative specifier with its
// .ts extension.
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
export function initialTaskFilter(
  requested: string | null | undefined,
  catalog: StatusMeta[],
): string {
  return requested && taskStatuses(catalog).has(requested) ? requested : "";
}

/**
 * boardStatus is the task board's status filter: what the control shows and
 * what the table filters by, for one query value and the catalog read at the
 * moment of the call. It exists as a named derivation so the page's only
 * remaining decision is where each argument comes from -- and the catalog has
 * to be read inside the `computed`, because a caller that evaluates
 * `statusList()` while the page sets up passes the freeze-dried defaults for
 * the life of the page, which is how a shared link to a served-only status
 * filtered nothing until a reload.
 */
export function boardStatus(query: string, catalog: StatusMeta[]): string {
  return initialTaskFilter(query, catalog);
}

/** taskMatchesStatus reports whether a task survives the status filter. */
export function taskMatchesStatus(
  task: { status?: string },
  status: string,
  catalog: StatusMeta[],
): boolean {
  if (!status) return true;
  if (status === "needs_you")
    return catalog.some(
      (entry) => entry.needs_you && entry.id === (task.status ?? ""),
    );
  return task.status === status;
}
