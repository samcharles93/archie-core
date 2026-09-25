import assert from "node:assert/strict";
import test from "node:test";

import { streamURL } from "../src/lib/stream-url.ts";
import { logsEmptyDetail } from "../src/logs/logs-empty.ts";

test("deliberate topic reconnect carries the last cursor rather than starting over", () => {
  const token = "eyJ0YXNrcyI6IjIwMjYtMDEtMDEiLCJsb2dzIjoyfQ";
  assert.equal(streamURL("", false), "/api/stream");
  const onLogs = streamURL(token, true);
  assert.equal(new URL(onLogs, "http://archie-ui").searchParams.get("topics"), "logs");
  assert.equal(new URL(onLogs, "http://archie-ui").searchParams.get("since"), token);
  const offLogs = streamURL(token, false);
  assert.equal(new URL(offLogs, "http://archie-ui").searchParams.get("topics"), null);
  assert.equal(new URL(offLogs, "http://archie-ui").searchParams.get("since"), token);
});

test("unavailable logs do not promise an automatic reconnect when no feed exists", () => {
  assert.equal(logsEmptyDetail(true, "unavailable"), "This process is not delivering live daemon logs.");
});
