import { test } from "node:test";
import assert from "node:assert/strict";
import { h } from "preact";
import { render } from "@testing-library/preact";
import { StatusPill, BindingRow } from "../src/bindings/binding-editor.jsx";

test("statusPill labels draft as idle", () => {
  const { container } = render(h(StatusPill, { status: "draft" }));
  assert.match(container.textContent, /draft/);
  assert.ok(container.querySelector(".pill-idle"));
});

test("statusPill labels pending_approval as warn, with a human-readable label", () => {
  const { container } = render(h(StatusPill, { status: "pending_approval" }));
  assert.match(container.textContent, /pending approval/);
  assert.ok(container.querySelector(".pill-warn"));
});

test("statusPill labels armed as ok", () => {
  const { container } = render(h(StatusPill, { status: "armed" }));
  assert.match(container.textContent, /armed/);
  assert.ok(container.querySelector(".pill-ok"));
});

test("statusPill falls back to the raw status for an unrecognised value", () => {
  const { container } = render(h(StatusPill, { status: "weird" }));
  assert.match(container.textContent, /weird/);
});

test("bindingRow shows name, matcher source, workflow, and status", () => {
  const { container } = render(h(BindingRow, {
    binding: { id: 1, name: "sentry alerts", matcher: { source: "sentry" }, workflow: "implement", status: "draft" },
    onEdit: () => {},
    onApprove: () => {},
    onDelete: () => {},
  }));
  assert.match(container.textContent, /sentry alerts/);
  assert.match(container.textContent, /sentry/);
  assert.match(container.textContent, /implement/);
  assert.match(container.textContent, /draft/);
});

test("bindingRow shows the owner/repo pin when set, or a placeholder when not", () => {
  const pinned = render(h(BindingRow, {
    binding: { id: 1, name: "a", matcher: { source: "s" }, workflow: "implement", status: "draft", owner: "acme", repo: "widget" },
  }));
  assert.match(pinned.container.textContent, /acme\/widget/);

  const unpinned = render(h(BindingRow, {
    binding: { id: 2, name: "b", matcher: { source: "s" }, workflow: "implement", status: "draft" },
  }));
  assert.match(unpinned.container.textContent, /—/);
});

test("bindingRow shows an Approve action only when pending_approval", () => {
  const pending = render(h(BindingRow, {
    binding: { id: 1, name: "a", matcher: { source: "s" }, workflow: "implement", status: "pending_approval" },
  }));
  assert.match(pending.container.textContent, /Approve/);

  const draft = render(h(BindingRow, {
    binding: { id: 2, name: "b", matcher: { source: "s" }, workflow: "implement", status: "draft" },
  }));
  assert.doesNotMatch(draft.container.textContent, /Approve/);

  const armed = render(h(BindingRow, {
    binding: { id: 3, name: "c", matcher: { source: "s" }, workflow: "implement", status: "armed" },
  }));
  assert.doesNotMatch(armed.container.textContent, /Approve/);
});
