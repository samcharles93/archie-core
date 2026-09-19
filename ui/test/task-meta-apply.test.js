import { test } from "node:test";
import assert from "node:assert/strict";
import "./shim.js";
import { api } from "../src/base/api.jsx";
import {
  actionFor,
  applyTaskMeta,
  changeStatusList,
  configSchema,
  fileStatusLabel,
  loadTaskMeta,
  statusLabel,
} from "../src/base/task-meta.js";

// This file mutates the catalog on purpose: its subject is the upgrade path,
// and node runs one file per process, so the mutations cannot leak into the
// sibling suite that pins the snapshot (task-meta-catalogue.test.js). The
// assertions below therefore build on each other in declaration order.

test("applyTaskMeta replaces the vocabulary from a served catalogue", () => {
  applyTaskMeta({
    statuses: [{ id: "queued", label: "Pending", kind: "idle", needs_you: false }],
    actions: [{ id: "cancel", label: "Halt", kind: "quiet", confirm: "" }],
    change_statuses: [{ id: "added", label: "Created" }],
    config_schema: "archie/task-config@2",
  });
  assert.equal(statusLabel("queued"), "Pending");
  assert.equal(actionFor("cancel").label, "Halt");
  assert.equal(fileStatusLabel("added"), "Created");
  assert.equal(configSchema(), "archie/task-config@2");
});

// A server that predates a key (the pre-change /api/task-meta served only
// statuses and actions) must not blank the snapshot for it: an upgrade is
// additive, so the dashboard keeps working on what the payload omitted.
test("a payload missing the new keys keeps the snapshot rather than emptying it", () => {
  applyTaskMeta({ statuses: [{ id: "queued", label: "Pending", kind: "idle", needs_you: false }] });
  assert.equal(statusLabel("queued"), "Pending");
  assert.equal(changeStatusList().length, 1);
  assert.equal(fileStatusLabel("added"), "Created");
  assert.equal(configSchema(), "archie/task-config@2");
});

test("a malformed payload is ignored, never thrown on", () => {
  applyTaskMeta(null);
  applyTaskMeta("not a catalog");
  assert.equal(configSchema(), "archie/task-config@2");
});

test("loadTaskMeta upgrades to the served catalogue", async () => {
  const original = api.taskMeta;
  api.taskMeta = async () => ({
    change_statuses: [{ id: "typechange", label: "Kind changed" }],
    config_schema: "archie/task-config@3",
  });
  try {
    await loadTaskMeta();
    assert.equal(fileStatusLabel("typechange"), "Kind changed");
    assert.equal(configSchema(), "archie/task-config@3");
  } finally {
    api.taskMeta = original;
  }
});

// The page must render when archied is unreachable: loadTaskMeta swallows the
// failure and the last good catalog (or the snapshot) stays in place.
test("a rejected fetch keeps the current vocabulary and never throws", async () => {
  const original = api.taskMeta;
  api.taskMeta = async () => {
    throw new Error("no daemon");
  };
  try {
    await loadTaskMeta();
    assert.equal(configSchema(), "archie/task-config@3");
    assert.equal(fileStatusLabel("typechange"), "Kind changed");
  } finally {
    api.taskMeta = original;
  }
});
