import { routes } from "@/router";
import {
  buildNav,
  type NavNode,
  type NavRoute,
  type NavSpec,
} from "./nav-tree";

export type { NavEntry, NavGroup, NavLink, NavNode } from "./nav-tree";

const table = routes as Array<NavRoute & { meta?: { navPath?: string } }>;

// Five places. Configuration lives under Settings, grouped the way the
// settings sidebar groups it (docs/design/ui-redesign/README.md, section 2).
const specs: NavSpec[] = [
  { kind: "link", path: "/" },
  { kind: "link", path: "/tasks" },
  { kind: "link", path: "/workflows" },
  { kind: "link", path: "/events" },
  { kind: "link", path: "/settings" },
];

// The Settings sidebar, grouped by what is being configured.
const settings: NavSpec = {
  kind: "group",
  label: "Settings",
  sections: [
    {
      label: "Agents",
      paths: [
        "/settings/personas",
        "/settings/models",
        "/settings/skills",
        "/settings/curators",
        "/settings/schedules",
        "/settings/scheduling-policy",
        "/settings/advanced",
      ],
    },
    {
      label: "Integrations",
      paths: [
        "/settings/channels",
        "/settings/repositories",
        "/settings/identities",
        "/settings/tools",
      ],
    },
    {
      label: "Runtime",
      paths: ["/settings/task-execution", "/settings/status"],
    },
    { paths: ["/settings/history"] },
  ],
};

// Reached from the Dashboard's Logs button and the account menu, and by
// Jump to, but not from the bar.
const unlisted = ["/logs", "/settings/appearance"];

export function navTree(hidden: string[] = []): NavNode[] {
  return buildNav(table, specs, hidden).tree;
}

/** navEntries lists every destination Jump to can reach. */
export function navEntries(hidden: string[] = []) {
  return buildNav(table, [...specs, settings], hidden, unlisted).jumpTargets;
}

/** settingsNav is the Settings sidebar; each section labels its first item. */
export function settingsNav(hidden: string[] = []) {
  const [group] = buildNav(table, [settings], hidden).tree;
  return group?.kind === "group" ? group.items : [];
}

/**
 * activeNavPath maps the current route onto the nav entry that should read as
 * current, so /tasks/42 keeps Tasks lit rather than lighting nothing.
 */
export function activeNavPath(path: string): string {
  const route = table.find((r) => r.path === path);
  const navPath = route?.meta?.navPath;
  if (typeof navPath === "string") return navPath;
  return path;
}
