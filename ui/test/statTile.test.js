// KPI tile regression coverage, moved here when base/statTile.jsx stopped
// building DOM by hand.
//
// The regression this pins: `el("div.stat-value", 0, ...)` treated a falsy
// first child (0, "", false) as "no attributes" and dropped it, so stat tiles
// rendered an empty headline whenever the value was zero. The dashboards of
// fresh installs showed exactly that -- labels and comparison text, no number.
// JSX does not have that bug, which is precisely why the case is worth keeping:
// it is what a future `value && <div>` refactor would break.

import { test } from "node:test";
import assert from "node:assert/strict";
import { h } from "preact";
import { render, cleanup } from "@testing-library/preact";
import { StatTile, Sparkline } from "../src/base/statTile.jsx";

test("StatTile renders a zero headline value", () => {
  const { container } = render(<StatTile label="Working now" value={0} compare="Nothing running" />);
  const tile = container.querySelector(".card.stat");
  assert.equal(tile.querySelector(".stat-label").textContent, "Working now");
  assert.equal(tile.querySelector(".stat-value").textContent, "0");
  assert.equal(tile.querySelector(".stat-compare").textContent, "Nothing running");
  cleanup();
});

test("StatTile renders an empty-string headline value without dropping it", () => {
  const { container } = render(<StatTile label="Tokens used" value="" compare="Across 14 days" />);
  assert.equal(container.querySelector(".stat-value").textContent, "");
  assert.equal(container.querySelector(".stat-compare").textContent, "Across 14 days");
  cleanup();
});

test("StatTile renders a string headline value", () => {
  const { container } = render(<StatTile label="Tokens used" value="2.4M" compare="Across 14 days" />);
  assert.equal(container.querySelector(".stat-value").textContent, "2.4M");
  cleanup();
});

test("StatTile shows a trend direction only when a trend is supplied", () => {
  const plain = render(<StatTile label="Delivered" value={3} compare="3% of all tasks" />);
  assert.equal(plain.container.querySelector(".stat-trend"), null);
  cleanup();
  const trended = render(<StatTile label="Tokens used" value={5} trend={12.5} compare="x" goodDirection="down" />);
  assert.match(trended.container.querySelector(".stat-trend").textContent, /12\.5/);
  cleanup();
});

test("StatTile draws a sparkline only when a series has a shape to show", () => {
  const flat = render(<StatTile label="Delivered" value={3} compare="x" series={[1]} />);
  assert.equal(flat.container.querySelector("svg.spark"), null);
  cleanup();
  const shaped = render(<StatTile label="Delivered" value={3} compare="x" series={[1, 4, 2]} />);
  assert.ok(shaped.container.querySelector("svg.spark polyline"));
  cleanup();
});
