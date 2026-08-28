import { test } from "node:test";
import assert from "node:assert/strict";
import "./shim.js";
import {
  actionFor,
  attentionStatusIds,
  statusIds,
  statusKind,
  statusLabel,
} from "../src/base/task-meta.js";

test("statusLabel returns the human label for known statuses", () => {
  assert.equal(statusLabel("pr_open"), "In review");
  assert.equal(statusLabel("waiting_human"), "Waiting for you");
});

test("statusLabel falls back to the raw id for unknown statuses", () => {
  assert.equal(statusLabel("quantum_superposition"), "quantum_superposition");
});

test("statusKind returns the pill severity for a status", () => {
  assert.equal(statusKind("running"), "info");
  assert.equal(statusKind("pr_open"), "ok");
  assert.equal(statusKind("parked"), "warn");
  assert.equal(statusKind("dead"), "danger");
});

test("statusKind defaults to idle for unknown statuses", () => {
  assert.equal(statusKind("quantum_superposition"), "idle");
});

test("statusIds includes every lifecycle status", () => {
  assert.deepEqual(new Set(statusIds()), new Set([
    "queued", "running", "waiting_human", "pr_open", "merged",
    "parked", "dead", "rejected", "closed_wont_do",
  ]));
});

test("attentionStatusIds groups the statuses that need a human", () => {
  const ids = attentionStatusIds();
  assert.ok(ids.has("waiting_human"));
  assert.ok(ids.has("parked"));
  assert.ok(!ids.has("queued"));
});

test("actionFor returns label, kind and confirm from the catalog", () => {
  const reject = actionFor("reject");
  assert.ok(reject);
  assert.equal(reject.label, "Reject");
  assert.equal(reject.kind, "quiet");
  assert.ok(reject.confirm.includes("{title}"));
});

test("actionFor marks navigation links as link kind", () => {
  assert.equal(actionFor("open_pr").kind, "link");
  assert.equal(actionFor("open_issue").kind, "link");
});

test("actionFor returns null for an unknown action", () => {
  assert.equal(actionFor("teleport"), null);
});
