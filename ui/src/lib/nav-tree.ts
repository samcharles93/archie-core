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

export interface NavRoute {
  path: string;
  meta?: {
    label?: string;
    description?: string;
    section?: string;
    soon?: boolean;
  };
}

export type NavSpec =
  | { kind: "link"; path: string }
  | {
      kind: "group";
      label: string;
      sections: { label?: string; paths: string[] }[];
    };

/**
 * buildNav turns the specs into the bar, with the routes the serving
 * composition cannot back removed. A group whose every child is hidden is
 * dropped; each section's label moves to its first visible item. jumpTargets
 * is everything the bar reaches plus the unlisted pages, for the Jump to box.
 */
export function buildNav(
  table: NavRoute[],
  specs: NavSpec[],
  hidden: string[] = [],
  unlisted: string[] = [],
): { tree: NavNode[]; jumpTargets: NavEntry[] } {
  const entryFor = (path: string): NavEntry => {
    const route = table.find((r) => r.path === path);
    if (!route) throw new Error(`nav references ${path}, which is not a route`);
    return {
      path,
      label: route.meta?.label ?? path,
      description: route.meta?.description ?? "",
      section: route.meta?.section,
      soon: route.meta?.soon,
    };
  };
  const visible = (path: string) => !hidden.includes(path);

  const tree: NavNode[] = [];
  for (const spec of specs) {
    if (spec.kind === "link") {
      if (visible(spec.path))
        tree.push({ kind: "link", entry: entryFor(spec.path) });
      continue;
    }
    const items = spec.sections.flatMap((section) =>
      section.paths
        .filter(visible)
        .map(entryFor)
        .map((entry, i) =>
          i === 0 ? { ...entry, dividerBefore: section.label ?? "" } : entry,
        ),
    );
    if (items.length > 0) tree.push({ kind: "group", label: spec.label, items });
  }

  const reached = tree.flatMap((node) =>
    node.kind === "link" ? [node.entry] : node.items,
  );
  const extra = unlisted.filter(visible).map(entryFor);
  return { tree, jumpTargets: [...reached, ...extra] };
}
