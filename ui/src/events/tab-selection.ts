/**
 * Which Events tabs a composition offers, and which one is showing.
 *
 * Held apart from EventsPage so it can be tested without a browser: the page
 * owns the components and the query string, this owns the decision. It imports
 * nothing at all, which is what the test harness needs -- no Vue, no alias, no
 * component load.
 */

export interface EventsTabSpec {
  id: string;
  label: string;
  /** The capability this tab needs, as GET /api/capabilities names it. */
  section: string;
}

/** The tabs, in reading order: what arrived, where its fields go wrong, what
 * runs. */
export const EVENTS_TABS: EventsTabSpec[] = [
  { id: "inspector", label: "Inspector", section: "captures" },
  { id: "mappings", label: "Mappings", section: "mappings" },
  { id: "bindings", label: "Bindings", section: "bindings" },
];

/**
 * availableTabs keeps the tabs the serving composition can back. A section the
 * server did not mention -- an older server, a failed read, a section added
 * after this build -- stays visible, because a tab that says "unavailable" is
 * recoverable and one that silently vanished is not.
 */
export function availableTabs(
  sections: Record<string, boolean> | null | undefined,
): EventsTabSpec[] {
  if (!sections) return [...EVENTS_TABS];
  return EVENTS_TABS.filter((tab) => sections[tab.section] !== false);
}

/**
 * activeTab is the tab the URL asks for when that tab exists here, else the
 * first one that does. An empty result means this composition backs none of
 * them, which the page renders as an empty state rather than a broken tab.
 */
export function activeTab(
  requested: string,
  available: EventsTabSpec[],
): string {
  if (available.some((tab) => tab.id === requested)) return requested;
  return available[0]?.id ?? "";
}
