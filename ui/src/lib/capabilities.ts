// Which dashboard sections the serving process can actually back.
//
// From the UI cutover the dashboard is served by more than one composition,
// and a section with nothing behind it still answers -- an empty list, a
// "disabled" marker, a 501 -- which the browser cannot tell from a quiet
// deployment. GET /api/capabilities says which sections are backed here, and
// the nav drops the rest.
import { ref } from "vue";

import { api } from "@/lib/api";
import { routes } from "@/router";

/** The `sections` map GET /api/capabilities returns. */
export type CapabilitySections = Record<string, boolean>;

/**
 * The route fields hiddenRoutes reads. The section lives under `meta` because
 * the route table is an untyped literal: internal/gateway's registry test
 * parses `const routes = [` and one route per line out of the source, so it
 * cannot carry a type annotation.
 */
export interface SectionedRoute {
  path: string;
  meta?: { section?: string };
}

/** hidden lists the route paths this composition cannot serve. */
export const hidden = ref<string[]>([]);

/**
 * sections is the server's raw map, where `hidden` is that map already applied
 * to this route table. A surface that owns several capabilities rather than one
 * -- the Events page, whose tabs are separately backable -- reads this to decide
 * which parts of itself it can serve.
 *
 * Null means nothing has been reported (an older server, a failed read), and
 * every surface stays visible: a page that says "unavailable" is recoverable,
 * one that silently vanished is not.
 */
export const sections = ref<CapabilitySections | null>(null);

// hiddenRoutes returns the paths whose section the server reported it cannot
// serve. An unreported section stays visible: a page that says "unavailable"
// is recoverable, a navigation entry that silently vanished is not, so an
// older server or a failed capabilities read shows everything.
export function hiddenRoutes(
  sections: CapabilitySections | null | undefined,
  table: SectionedRoute[],
): string[] {
  if (!sections) return [];
  return table
    .filter(
      (r) =>
        typeof r.meta?.section === "string" &&
        sections[r.meta.section] === false,
    )
    .map((r) => r.path);
}

/**
 * loadCapabilities asks once, after the shell is up: the nav paints immediately
 * and loses what this process cannot back a moment later, rather than holding
 * the page behind a request. A failed read hides nothing.
 */
export async function loadCapabilities(): Promise<void> {
  try {
    const body = await api.capabilities<{
      sections?: CapabilitySections;
    } | null>();
    sections.value = body?.sections ?? null;
    hidden.value = hiddenRoutes(body?.sections, routes as SectionedRoute[]);
  } catch {
    // Offline or an older server: every section stays visible.
  }
}
