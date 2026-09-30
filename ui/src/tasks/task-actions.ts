/**
 * Which lifecycle controls a surface renders.
 *
 * A task offers whatever the server says it offers -- the action list rides
 * with the task -- so a status the server will not archive is never offered an
 * Archive control here. A surface may narrow that list to the controls it has
 * room for (the task head has room for one), but narrowing can only ever
 * remove: a control the server did not send cannot be conjured by naming it.
 */
export function shownActionIds(
  actions: readonly string[] | null | undefined,
  only?: readonly string[],
): string[] {
  if (!Array.isArray(actions)) return [];
  if (!only || only.length === 0) return [...actions];
  return actions.filter((id) => only.includes(id));
}
