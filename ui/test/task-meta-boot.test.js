import { test } from "node:test";
import assert from "node:assert/strict";
import { waitFor } from "@testing-library/preact";

// The dashboard's lifecycle vocabulary is *served*: /api/task-meta is the
// source of truth, and base/task-meta.jsx keeps a freeze-dried default only so
// the first paint has labels. That default is meant to be replaced at boot.
//
// This pins the boot step. Without it the module documents a single source of
// truth that never loads, so a server-side catalogue change silently stops
// reaching the browser -- which is exactly what had happened: loadTaskMeta()
// was exported with no callers, and everything read the frozen snapshot.
import { api } from "../src/base/api.jsx";
import { actionFor, statusIds, statusKind, statusLabel } from "../src/base/task-meta.jsx";

globalThis.fetch = async () => {
  throw new Error("no daemon in this test");
};

// jsdom has no EventSource and the dashboard subscribes on mount.
class InertEventSource {
  close() {}
}
globalThis.EventSource = InertEventSource;

test("booting the dashboard replaces the freeze-dried vocabulary with the served catalog", async () => {
  api.taskMeta = async () => ({
    statuses: [{ id: "custom_status", label: "Custom status", kind: "danger", needs_you: false }],
    actions: [{ id: "custom_action", label: "Custom action", kind: "primary" }],
  });
  api.capabilities = async () => ({});

  document.body.innerHTML = '<div id="app"></div>';
  location.hash = "#/";
  await import("../src/main.jsx");

  await waitFor(() => assert.equal(statusLabel("custom_status"), "Custom status"));
  assert.equal(statusKind("custom_status"), "danger");
  assert.deepEqual(statusIds(), ["custom_status"]);
  assert.equal(actionFor("custom_action")?.label, "Custom action");
});
