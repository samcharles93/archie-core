// Regression coverage for the DOM-building primitives.
//
// The KPI regression this pins: `el("div.stat-value", 0, ...)` used to treat a
// falsy first child (0, "", false) as "no attributes" and drop it, so stat
// tiles rendered an empty headline whenever the value was zero. The dashboards
// of fresh installs show exactly that -- labels and comparison text, no number.

import { test } from "node:test";
import assert from "node:assert/strict";
import "./shim.js";
import { el, mount } from "../src/base/dom.js";
import { statTile } from "../src/base/statTile.js";

test("el renders a falsy numeric value instead of dropping it", () => {
  const node = el("div.stat-value", 0);
  assert.equal(node.textContent, "0");
});

test("el renders an empty string first child without dropping later children", () => {
  const node = el("div", "", "tail");
  assert.equal(node.textContent, "tail");
});

test("el keeps falsy non-child values out of the output but keeps real children", () => {
  const node = el("div", false, "x");
  assert.equal(node.textContent, "x");
});

test("el still treats an attrs object as attributes", () => {
  const node = el("a.btn", { href: "#/tasks", "aria-label": "Tasks" }, "Tasks");
  assert.equal(node.getAttribute("href"), "#/tasks");
  assert.equal(node.getAttribute("aria-label"), "Tasks");
  assert.equal(node.textContent, "Tasks");
});

test("el still treats a Node first argument as a child", () => {
  const child = el("span", "inner");
  const node = el("div", child);
  assert.equal(node.textContent, "inner");
});

test("mount replaces prior children", () => {
  const root = el("div", "old");
  mount(root, el("p", "new"));
  assert.equal(root.textContent, "new");
});

test("statTile renders a zero headline value", () => {
  const tile = statTile({ label: "Working now", value: 0, compare: "Nothing running" });
  assert.equal(tile.querySelector(".stat-label").textContent, "Working now");
  assert.equal(tile.querySelector(".stat-value").textContent, "0");
  assert.equal(tile.querySelector(".stat-compare").textContent, "Nothing running");
});

test("statTile renders a string headline value", () => {
  const tile = statTile({ label: "Tokens used", value: "2.4M", compare: "Across 14 days" });
  assert.equal(tile.querySelector(".stat-value").textContent, "2.4M");
});

test("statTile shows a trend direction only when a trend is supplied", () => {
  const plain = statTile({ label: "Delivered", value: 3, compare: "3% of all tasks" });
  assert.equal(plain.querySelector(".stat-trend"), null);
  const trended = statTile({ label: "Tokens used", value: 5, trend: 12.5, compare: "x", goodDirection: "down" });
  assert.match(trended.querySelector(".stat-trend").textContent, /12\.5/);
});
