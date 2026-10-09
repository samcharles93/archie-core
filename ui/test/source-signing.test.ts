import assert from "node:assert/strict";
import test from "node:test";

import { captureSignature } from "../src/captures/capture-signature.ts";
import {
  signingKind,
  signingLabel,
  sourceURL,
} from "../src/sources/source-signing.ts";
import { describeTimelineEvent } from "../src/tasks/timeline-event.ts";

test("a capture shows how it may dispatch", () => {
  const cases: Array<
    [{ authenticated?: boolean; unsigned?: boolean }, string, string]
  > = [
    [{ authenticated: true }, "Signed", "ok"],
    [{ unsigned: true }, "Unsigned", "warn"],
    [{}, "Unverified", "idle"],
    [{ authenticated: false, unsigned: false }, "Unverified", "idle"],
  ];
  for (const [capture, label, kind] of cases) {
    assert.deepEqual(
      captureSignature(capture),
      { label, kind },
      JSON.stringify(capture),
    );
  }
});

test("a source's signing state names itself and its tone", () => {
  const cases: Array<[string | undefined, string, string]> = [
    ["signed", "signed", "ok"],
    ["unsigned_pending_approval", "unsigned pending approval", "info"],
    ["unsigned", "unsigned", "warn"],
    ["something_new", "something_new", "idle"],
    [undefined, "unknown", "idle"],
  ];
  for (const [signing, label, kind] of cases) {
    assert.equal(signingLabel(signing), label, String(signing));
    assert.equal(signingKind(signing), kind, String(signing));
  }
});

test("a source's URL escapes nothing a valid path can hold", () => {
  assert.equal(
    sourceURL("https://archie.example", "0199a0b2-0000-7000-8000-000000000000"),
    "https://archie.example/webhooks/capture/0199a0b2-0000-7000-8000-000000000000",
  );
  assert.equal(sourceURL("http://h", "a b"), "http://h/webhooks/capture/a%20b");
});

test("a task started by an unsigned event says so on its timeline", () => {
  const line = describeTimelineEvent({
    kind: "binding_started",
    data: { source: "firewall", binding_name: "fw", capture_id: "c1", unsigned: true },
  });
  assert.deepEqual(line, {
    title: "Started by an unsigned event",
    detail: "source firewall",
    tone: "warn",
    links: [
      { label: "binding fw", to: "/events?tab=bindings" },
      { label: "captured event", to: "/events?tab=inspector&capture=c1" },
    ],
  });
  assert.equal(
    describeTimelineEvent({ kind: "stage_start", stage: "plan" }).tone,
    undefined,
  );
});
