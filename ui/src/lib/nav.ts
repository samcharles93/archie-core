import { routes } from "@/router";

export interface NavEntry {
  path: string;
  label: string;
  description: string;
  section?: string;
  dividerBefore?: string;
  soon?: boolean;
}

export interface NavLink {
  kind: "link";
  entry: NavEntry;
}

export interface NavGroup {
  kind: "group";
  label: string;
  items: NavEntry[];
}

export type NavNode = NavLink | NavGroup;

interface RouteMeta {
  label?: string;
  description?: string;
  section?: string;
  nav?: boolean;
  navPath?: string;
  soon?: boolean;
}

const table = routes as Array<{ path: string; meta?: RouteMeta }>;

// The bar reads left to right. A destination that is its own top-level thing is
// a link rather than a group of one: Events carries the three surfaces that used
// to sit in a dropdown, and a group whose only child shares its name is a menu
// that says nothing.
type NavSpec =
  | { kind: "link"; path: string }
  | {
      kind: "group";
      label: string;
      paths: string[];
      dividerBefore?: { path: string; label: string };
    };

const specs: NavSpec[] = [
  { kind: "link", path: "/" },
  {
    kind: "group",
    label: "Work",
    paths: ["/tasks", "/workflows", "/channels", "/logs"],
  },
  { kind: "group", label: "Agent", paths: ["/skills", "/curators"] },
  { kind: "link", path: "/events" },
  {
    kind: "group",
    label: "System",
    paths: [
      "/system/status",
      "/system/appearance",
      "/system/tasks",
      "/system/models",
      "/system/repos",
      "/system/identities",
      "/system/advanced",
    ],
    dividerBefore: { path: "/system/tasks", label: "Settings" },
  },
];

function entryFor(path: string): NavEntry {
  const route = table.find((r) => r.path === path);
  if (!route) throw new Error(`nav references ${path}, which is not a route`);
  return {
    path,
    label: route.meta?.label ?? path,
    description: route.meta?.description ?? "",
    section: route.meta?.section,
    soon: route.meta?.soon,
  };
}

/**
 * navTree returns the bar, with routes the serving composition cannot back
 * removed. A group whose every child is hidden is dropped; a group left with
 * one child stays a group, so the bar has the same shape on every deployment.
 */
export function navTree(hidden: string[] = []): NavNode[] {
  const visible = (e: NavEntry) => !hidden.includes(e.path);
  const nodes: NavNode[] = [];
  for (const spec of specs) {
    if (spec.kind === "link") {
      const entry = entryFor(spec.path);
      if (visible(entry)) nodes.push({ kind: "link", entry });
      continue;
    }
    const items = spec.paths.map(entryFor).filter(visible);
    const divided = items.map((item) => ({
      ...item,
      dividerBefore:
        item.path === spec.dividerBefore?.path
          ? spec.dividerBefore.label
          : undefined,
    }));
    if (divided.length > 0)
      nodes.push({ kind: "group", label: spec.label, items: divided });
  }
  return nodes;
}

/** navEntries flattens the tree to the destinations it can reach. */
export function navEntries(hidden: string[] = []): NavEntry[] {
  return navTree(hidden).flatMap((node) =>
    node.kind === "link" ? [node.entry] : node.items,
  );
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
