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

// The group owns the context, so the items inside it are named for what they
// are rather than what they belong to. Order is the reading order of the menu.
interface GroupSpec {
  label: string;
  paths: string[];
  dividerBefore?: { path: string; label: string };
}

const groups: GroupSpec[] = [
  { label: "Work", paths: ["/tasks", "/workflows", "/channels", "/logs"] },
  { label: "Agent", paths: ["/skills", "/curators"] },
  { label: "Events", paths: ["/captures", "/mappings", "/bindings"] },
  {
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
  const nodes: NavNode[] = [{ kind: "link", entry: entryFor("/") }];
  for (const group of groups) {
    const items = group.paths.map(entryFor).filter(visible);
    const divided = items.map((item) => ({
      ...item,
      dividerBefore: item.path === group.dividerBefore?.path ? group.dividerBefore.label : undefined,
    }));
    if (divided.length > 0) nodes.push({ kind: "group", label: group.label, items: divided });
  }
  return nodes;
}

/** navEntries flattens the tree to the destinations it can reach. */
export function navEntries(hidden: string[] = []): NavEntry[] {
  return navTree(hidden).flatMap((node) => (node.kind === "link" ? [node.entry] : node.items));
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
