import assert from "node:assert/strict";
import test from "node:test";

import { buildNav, type NavRoute, type NavSpec } from "../src/lib/nav-tree.ts";

const table: NavRoute[] = [
  { path: "/", meta: { label: "Dashboard" } },
  { path: "/tasks", meta: { label: "Tasks" } },
  { path: "/settings/models", meta: { label: "Models" } },
  { path: "/settings/skills", meta: { label: "Skills" } },
  { path: "/settings/channels", meta: { label: "Channels" } },
  { path: "/logs", meta: { label: "Logs" } },
];

const specs: NavSpec[] = [
  { kind: "link", path: "/" },
  { kind: "link", path: "/tasks" },
  {
    kind: "group",
    label: "Settings",
    sections: [
      { label: "Agents", paths: ["/settings/models", "/settings/skills"] },
      { label: "Integrations", paths: ["/settings/channels"] },
    ],
  },
];

test("each settings section labels its first visible item", () => {
  const nav = buildNav(table, specs, ["/settings/models"]);
  const group = nav.tree.find((n) => n.kind === "group");
  assert.ok(group && group.kind === "group");
  assert.deepEqual(
    group.items.map((i) => [i.path, i.dividerBefore]),
    [
      ["/settings/skills", "Agents"],
      ["/settings/channels", "Integrations"],
    ],
  );
});

test("a section with every item hidden leaves no label behind", () => {
  const nav = buildNav(table, specs, ["/settings/models", "/settings/skills"]);
  const group = nav.tree.find((n) => n.kind === "group");
  assert.ok(group && group.kind === "group");
  assert.deepEqual(
    group.items.map((i) => [i.path, i.dividerBefore]),
    [["/settings/channels", "Integrations"]],
  );
});

test("a group with every item hidden is dropped", () => {
  const nav = buildNav(table, specs, [
    "/settings/models",
    "/settings/skills",
    "/settings/channels",
  ]);
  assert.deepEqual(
    nav.tree.map((n) => (n.kind === "link" ? n.entry.path : n.label)),
    ["/", "/tasks"],
  );
});

test("jump targets add unlisted pages to what the bar reaches", () => {
  const nav = buildNav(table, specs, [], ["/logs"]);
  assert.deepEqual(
    nav.jumpTargets.map((e) => e.path),
    ["/", "/tasks", "/settings/models", "/settings/skills", "/settings/channels", "/logs"],
  );
  assert.deepEqual(buildNav(table, specs, ["/logs"], ["/logs"]).jumpTargets.map((e) => e.path).includes("/logs"), false);
});

test("a spec naming a missing route fails loudly", () => {
  assert.throws(() => buildNav(table, [{ kind: "link", path: "/nope" }]), /\/nope/);
});

test("an unlabelled section marks its first item with an empty divider", () => {
  const nav = buildNav(table, [
    {
      kind: "group",
      label: "Settings",
      sections: [
        { label: "Agents", paths: ["/settings/models"] },
        { paths: ["/logs"] },
      ],
    },
  ]);
  const group = nav.tree[0];
  assert.ok(group && group.kind === "group");
  assert.deepEqual(
    group.items.map((i) => [i.path, i.dividerBefore]),
    [
      ["/settings/models", "Agents"],
      ["/logs", ""],
    ],
  );
});
