import { ref } from "vue";

import { routes } from "@/router";

export type CapabilitySections = Record<string, boolean>;

interface RouteMeta {
  section?: string;
}

/** hidden lists the route paths this composition cannot serve. */
export const hidden = ref<string[]>([]);

function hiddenFor(sections: CapabilitySections | undefined): string[] {
  if (!sections) return [];
  return (routes as Array<{ path: string; meta?: RouteMeta }>)
    .filter((route) => {
      const section = route.meta?.section;
      return typeof section === "string" && sections[section] === false;
    })
    .map((route) => route.path);
}

/**
 * loadCapabilities asks once, after the shell is up: the nav paints immediately
 * and loses what this process cannot back a moment later, rather than holding
 * the page behind a request. A failed read hides nothing.
 */
export async function loadCapabilities(): Promise<void> {
  try {
    const response = await fetch("/api/capabilities", { headers: { Accept: "application/json" } });
    if (!response.ok) return;
    const body = (await response.json()) as { sections?: CapabilitySections } | null;
    hidden.value = hiddenFor(body?.sections);
  } catch {
    // Offline or an older server: every section stays visible.
  }
}
