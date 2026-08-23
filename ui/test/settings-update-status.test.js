import { test } from "node:test";
import assert from "node:assert/strict";
import "./shim.js";
import { updateStatusCard } from "../src/settings/update-status.js";

test("update status card shows an empty state with no components", () => {
  const card = updateStatusCard([]);
  assert.equal(card.querySelector("table"), null);
});

test("update status card renders one row per component with its status pill", () => {
  const card = updateStatusCard([
    { id: "daemon", label: "Gateway", install_type: "binary", running_version: "1.0.0", latest_available: "1.1.0", status: "update_available" },
    { id: "agent", label: "Runtime", install_type: "container", running_version: "1.0.0", status: "ok" },
  ]);
  const rows = card.querySelector("tbody").children;
  assert.equal(rows.length, 2);
  assert.match(rows[0].textContent, /Gateway/);
  assert.match(rows[0].textContent, /Update available/);
  assert.match(rows[1].textContent, /OK/);
});

test("update status card's row title carries the comparison basis", () => {
  const card = updateStatusCard([
    { id: "agent", label: "Runtime", installed_claim: "1.1.0", running_version: "1.0.0", reference: "img@sha256:abc", status: "drift" },
  ]);
  const row = card.querySelector("tbody").children[0];
  assert.match(row.attrs.title, /Installed claim: 1\.1\.0/);
  assert.match(row.attrs.title, /Running: 1\.0\.0/);
  assert.match(row.attrs.title, /Reference: img@sha256:abc/);
});

test("update status card shows an unrecognised status as its own label rather than crashing", () => {
  const card = updateStatusCard([{ id: "x", status: "something-new" }]);
  const row = card.querySelector("tbody").children[0];
  assert.match(row.textContent, /something-new/);
});
