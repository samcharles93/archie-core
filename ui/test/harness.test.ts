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

test("credential status distinguishes uncaptured, ready, expiring and expired bindings", () => {
  const cases: Array<[Partial<HarnessBinding>, string]> = [
    [{}, "unconfigured"],
    [{ captured: true, expires_at: "2026-10-02T13:00:00Z" }, "ready"],
    [{ captured: true, expires_at: new Date(now + EXPIRING_WINDOW_MS - 1000).toISOString() }, "expiring"],
    [{ captured: true, expires_at: "2026-10-02T11:00:00Z" }, "expired"],
    [{ captured: true }, "ready"],
    [{ captured: true, expires_at: "not-a-date" }, "ready"],
  ];
  for (const [overrides, expected] of cases) {
    assert.equal(bindingStatus(binding(overrides), now), expected, JSON.stringify(overrides));
  }
});

test("setup terminals preserve secure transport and encode the profile", () => {
  for (const [profile, location, expected] of [
    ["claude-kit", { protocol: "http:", host: "127.0.0.1:8484" }, "ws://127.0.0.1:8484/api/harness/terminal?profile=claude-kit"],
    ["claude kit", { protocol: "https:", host: "archie.example" }, "wss://archie.example/api/harness/terminal?profile=claude+kit"],
  ] as const) {
    assert.equal(setupTerminalURL(profile, location), expected, profile);
  }
});
