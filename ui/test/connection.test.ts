import assert from "node:assert/strict";
import test from "node:test";

import { connectionStatus } from "../src/lib/connection.ts";

test("the connection pill names the stream state", () => {
  for (const [state, label, tone] of [
    ["live", "Connected", "ok"],
    ["connecting", "Connecting", "warn"],
    ["reconnecting", "Connecting", "warn"],
    ["unavailable", "Disconnected", "danger"],
  ] as const) {
    assert.deepEqual(connectionStatus(state), { label, tone }, state);
  }
});
