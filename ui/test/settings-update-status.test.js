import { test } from "node:test";
import assert from "node:assert/strict";
import { render, cleanup } from "@testing-library/preact";
import { UpdateStatusCard } from "../src/settings/update-status.jsx";

function renderCard(components) {
  const { container, unmount } = render(<UpdateStatusCard components={components} />);
  return { root: container, unmount };
}

test("update status card shows an empty state with no components", () => {
  const { root } = renderCard([]);
  assert.equal(root.querySelector("table"), null);
});

test("update status card renders one row per component with its status pill", () => {
  const { root } = renderCard([
    { id: "daemon", label: "Gateway", install_type: "binary", running_version: "1.0.0", latest_available: "1.1.0", status: "update_available" },
    { id: "agent", label: "Runtime", install_type: "container", running_version: "1.0.0", status: "ok" },
  ]);
  const rows = root.querySelector("tbody").children;
  assert.equal(rows.length, 2);
  assert.match(rows[0].textContent, /Gateway/);
  assert.match(rows[0].textContent, /Update available/);
  assert.match(rows[1].textContent, /OK/);
});

test("update status card's row title carries the comparison basis", () => {
  const { root } = renderCard([
    { id: "agent", label: "Runtime", installed_claim: "1.1.0", running_version: "1.0.0", reference: "img@sha256:abc", status: "drift" },
  ]);
  const row = root.querySelector("tbody").children[0];
  assert.match(row.getAttribute("title"), /Installed claim: 1\.1\.0/);
  assert.match(row.getAttribute("title"), /Running: 1\.0\.0/);
  assert.match(row.getAttribute("title"), /Reference: img@sha256:abc/);
});

test("update status card shows an unrecognised status as its own label rather than crashing", () => {
  const { root } = renderCard([{ id: "x", status: "something-new" }]);
  const row = root.querySelector("tbody").children[0];
  assert.match(row.textContent, /something-new/);
});
