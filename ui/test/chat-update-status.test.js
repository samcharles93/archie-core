import { test } from "node:test";
import assert from "node:assert/strict";
import { ApiError } from "../src/base/api.js";
import { updateUnavailableMessage } from "../src/chat/chat-update-status.js";

test("a 501 (feature not configured) is a quiet non-error, not a banner", () => {
  assert.equal(updateUnavailableMessage(new ApiError("501: Not Implemented", 501)), null);
});

test("a genuine failure gets a clear human message, not the raw fetch error text", () => {
  const err = new ApiError("500 Internal Server Error", 500);
  const message = updateUnavailableMessage(err);
  assert.ok(message);
  assert.notEqual(message, err.message);
});

test("an error with no status (e.g. a network failure) also gets a human message", () => {
  const err = new TypeError("Failed to fetch");
  const message = updateUnavailableMessage(err);
  assert.ok(message);
  assert.notEqual(message, err.message);
});
