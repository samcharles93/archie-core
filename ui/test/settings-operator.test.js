import { test } from "node:test";
import assert from "node:assert/strict";
import { render } from "@testing-library/preact";
import { UpdateActionsCard, DangerousActionsCard } from "../src/settings/operator-cards.jsx";

// Update install/defer and the dangerous-action approvals moved out of the
// chat panel and onto Configuration, where the operator controls live
// (archie-core-tf20). They keep the behaviour they had in chat.

test("an up-to-date install says so rather than showing empty controls", () => {
  const { container, unmount } = render(<UpdateActionsCard data={{ available: [] }} />);
  assert.match(container.textContent, /up to date/i);
  assert.equal(container.querySelector("button"), null, "nothing to install, nothing to press");
  unmount();
});

test("available updates are listed with install and defer", () => {
  const data = { available: [{ label: "archied", available: "v1.31.0" }], can_install: true, snapshot: {} };
  const { container, unmount } = render(<UpdateActionsCard data={data} />);
  const labels = [...container.querySelectorAll("button")].map((b) => b.textContent.trim());
  assert.deepEqual(labels, ["Defer", "Install update"]);
  assert.match(container.textContent, /archied: v1\.31\.0/);
  unmount();
});

test("an update that cannot be installed here offers only defer", () => {
  const data = { available: [{ label: "agent", available: "v2" }], can_install: false, snapshot: {} };
  const { container, unmount } = render(<UpdateActionsCard data={data} />);
  const labels = [...container.querySelectorAll("button")].map((b) => b.textContent.trim());
  assert.deepEqual(labels, ["Defer"]);
  unmount();
});

test("a deferred update reports that rather than pretending to be current", () => {
  const { container, unmount } = render(
    <UpdateActionsCard data={{ available: [], snapshot: { deferred: true } }} />,
  );
  assert.match(container.textContent, /deferred/i);
  unmount();
});

test("the update card surfaces its own error", () => {
  const { container, unmount } = render(<UpdateActionsCard data={{ error: "check command failed" }} />);
  assert.match(container.textContent, /check command failed/);
  unmount();
});

test("pending dangerous actions offer approve, 24h and deny", () => {
  const data = { pending: [{ id: "a1", description: "rm -rf /srv/old" }], checkpoints: [] };
  const { container, unmount } = render(<DangerousActionsCard data={data} />);
  assert.match(container.textContent, /rm -rf \/srv\/old/);
  const labels = [...container.querySelectorAll(".cfg-pending button")].map((b) => b.textContent.trim());
  assert.deepEqual(labels, ["Approve", "Approve for 24h", "Deny"]);
  unmount();
});

test("no pending actions says so", () => {
  const { container, unmount } = render(<DangerousActionsCard data={{ pending: [], checkpoints: [] }} />);
  assert.match(container.textContent, /No pending dangerous actions/i);
  unmount();
});

test("checkpoints are selectable by number and label", () => {
  const data = { pending: [], checkpoints: [{ number: 7, label: "before migration" }] };
  const { container, unmount } = render(<DangerousActionsCard data={data} />);
  const option = [...container.querySelectorAll("option")].find((o) => o.value === "7");
  assert.ok(option, "checkpoint 7 should be selectable");
  assert.match(option.textContent, /before migration/);
  unmount();
});

test("the server's capitalised checkpoint fields are accepted too", () => {
  // The API has returned both shapes; the chat panel read either.
  const data = { pending: [], checkpoints: [{ Number: 9, Label: "pre-deploy" }] };
  const { container, unmount } = render(<DangerousActionsCard data={data} />);
  const option = [...container.querySelectorAll("option")].find((o) => o.value === "9");
  assert.ok(option, "checkpoint 9 should be selectable");
  assert.match(option.textContent, /pre-deploy/);
  unmount();
});
