import { test } from "node:test";
import assert from "node:assert/strict";
import { randomUUID } from "../src/base/uuid.js";

const uuidPattern = /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/;

test("randomUUID uses the native implementation when available", () => {
  assert.equal(randomUUID({ randomUUID: () => "native-id" }), "native-id");
});

test("randomUUID builds a v4 UUID when native randomUUID is unavailable", () => {
  const bytes = new Uint8Array(16).fill(0xab);
  const cryptoAPI = { getRandomValues: (target) => target.set(bytes) };
  const value = randomUUID(cryptoAPI);

  assert.match(value, uuidPattern);
  assert.equal(value, "abababab-abab-4bab-abab-abababababab");
});
