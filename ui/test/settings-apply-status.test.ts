import assert from "node:assert/strict";
import test from "node:test";

import {
  applyStateLabel,
  applyStateTone,
  restartPendingTitles,
} from "../src/settings/apply-status.ts";
import type { ApplyStatusRow } from "../src/stores/control-plane.ts";

const resource = (
  kind: string,
  title: string,
  apply_mode: "live" | "restart-required" | "domain-managed",
) => ({ kind, title, apply_mode });

const behind: ApplyStatusRow[] = [
  { process: "archie-gateway", state: "pending-restart", version: 6, error: "" },
];
const current: ApplyStatusRow[] = [
  { process: "archied", state: "running", version: 7, error: "" },
];

test("the restart banner names a process-binding change but never a live one", () => {
  const rowsFor = (kind: string) =>
    kind === "plugin-settings" ? behind : kind === "channel-settings" ? behind : current;

  // Both resources are behind; only the restart-required one is a restart.
  assert.deepEqual(
    restartPendingTitles(
      [
        resource("plugin-settings", "Plugin settings", "live"),
        resource("channel-settings", "Channel settings", "restart-required"),
      ],
      rowsFor,
    ),
    ["Channel settings"],
  );

  // A live-apply kind behind on a process never earns the banner at all.
  assert.deepEqual(
    restartPendingTitles(
      [resource("plugin-settings", "Plugin settings", "live")],
      () => behind,
    ),
    [],
  );
});

test("a live kind reads as applied, and a process-binding change as a restart", () => {
  assert.equal(applyStateLabel("running", "live"), "Applied");
  assert.equal(applyStateLabel("pending-restart", "restart-required"), "Restart pending");
});

test("a live kind's lag never reads as a restart and is not a warning", () => {
  const liveLag = applyStateLabel("pending-restart", "live");
  assert.ok(
    !liveLag.toLowerCase().includes("restart"),
    `a live kind must not be told to restart, got "${liveLag}"`,
  );

  // A live kind that is behind is not a warning: nothing is broken and no
  // restart is owed, so the row stays neutral.
  assert.equal(applyStateTone("pending-restart", "live"), "idle");
  assert.equal(applyStateTone("pending-restart", "restart-required"), "warn");
});
