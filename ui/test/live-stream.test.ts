import assert from "node:assert/strict";
import test from "node:test";

import { streamURL } from "../src/lib/stream-url.ts";

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
