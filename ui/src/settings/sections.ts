import type { ConfigField, ConfigSection } from "./types";

/**
 * Which schema section belongs to which Route.
 *
 * The daemon serves one catalog (internal/webui/config_schema.go); the split
 * of that catalog across six routes is the UI's, so the mapping lives here
 * rather than in a per-page filter. Sections whose only fields are structured
 * (repositories, models) are absent deliberately: their dedicated editors own
 * those, not the generic row renderer.
 */

export const TASKS_SECTIONS = ["budgets"];
export const ADVANCED_SECTIONS = ["identity", "storage"];

/** sectionRows drops the structured fields: the generic renderer does not own
 * them, so a section left with none renders nothing at all. */
export function sectionRows(section: ConfigSection): ConfigField[] {
  return section.fields.filter((f) => f.type !== "structured");
}

/** sectionsFor returns the sections this page renders, in catalog order. */
export function sectionsFor(schema: ConfigSection[] | undefined, ids: string[]): ConfigSection[] {
  return (schema ?? []).filter((s) => ids.includes(s.id) && sectionRows(s).length > 0);
}
