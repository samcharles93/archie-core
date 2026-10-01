import assert from "node:assert/strict";
import test from "node:test";

import {
  bindingStatus,
  setupTerminalURL,
  EXPIRING_WINDOW_MS,
} from "../src/harness/harness.ts";

import type { HarnessBinding } from "../src/harness/harness.ts";

const now = Date.parse("2026-10-02T12:00:00Z");

function binding(overrides: Partial<HarnessBinding> = {}): HarnessBinding {
  return { service: "github", captured: false, ...overrides };
}

test("an uncaptured binding reads as unconfigured", () => {
  assert.equal(bindingStatus(binding(), now), "unconfigured");
});

test("a captured binding with a future expiry reads as ready", () => {
  assert.equal(
    bindingStatus(
      binding({ captured: true, expires_at: "2026-10-02T13:00:00Z" }),
      now,
    ),
    "ready",
  );
});

test("a captured binding inside the expiring window reads as expiring", () => {
  const almost = new Date(now + EXPIRING_WINDOW_MS - 1000).toISOString();
  assert.equal(
    bindingStatus(binding({ captured: true, expires_at: almost }), now),
    "expiring",
  );
});

test("a captured binding past its expiry reads as expired", () => {
  assert.equal(
    bindingStatus(
      binding({ captured: true, expires_at: "2026-10-02T11:00:00Z" }),
      now,
    ),
    "expired",
  );
});

test("a capture with no parseable expiry counts as ready, not expired", () => {
  // The provider did not tell archie when the token lapses; treating that as
  // expired would be a guess, and the wrong one.
  assert.equal(
    bindingStatus(binding({ captured: true }), now),
    "ready",
  );
  assert.equal(
    bindingStatus(binding({ captured: true, expires_at: "not-a-date" }), now),
    "ready",
  );
});

test("the terminal URL upgrades the scheme and carries the profile", () => {
  assert.equal(
    setupTerminalURL("claude-kit", { protocol: "http:", host: "127.0.0.1:8484" }),
    "ws://127.0.0.1:8484/api/harness/terminal?profile=claude-kit",
  );
});

test("an https dashboard opens awss terminal", () => {
  assert.equal(
    setupTerminalURL("claude kit", { protocol: "https:", host: "archie.example" }),
    "wss://archie.example/api/harness/terminal?profile=claude+kit",
  );
});
