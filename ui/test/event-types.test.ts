import assert from "node:assert/strict";
import test from "node:test";

const { captureIdentity, cleanRule, parseHeaderLines, ruleSummary } = await import("../src/captures/event-types.ts");

test("pasted header lines become a name-to-value map", () => {
  assert.deepEqual(parseHeaderLines("X-GitHub-Event: pull_request\n\nContent-Type: application/json; charset=utf-8\nnot a header"), {
    "X-GitHub-Event": "pull_request",
    "Content-Type": "application/json; charset=utf-8",
  });
  assert.deepEqual(parseHeaderLines(""), {});
});

test("a capture reads as its type's name, as unidentified, or as a type since deleted", () => {
  const types = [{ id: "et1", source: "gh", name: "push", rule: { headers: [], payload: [] } }];
  assert.deepEqual(captureIdentity({ id: 1, event_type: "et1" }, types), { label: "push", identified: true });
  assert.deepEqual(captureIdentity({ id: 2, event_type: "" }, types), { label: "Unidentified", identified: false });
  assert.deepEqual(captureIdentity({ id: 3 }, types), { label: "Unidentified", identified: false });
  assert.deepEqual(captureIdentity({ id: 4, event_type: "gone" }, types), { label: "Deleted type", identified: true });
});

test("a rule reads as its conditions joined", () => {
  assert.equal(
    ruleSummary({
      headers: [{ name: "x-github-event", value: "push" }],
      payload: [
        { path: "action", op: "equals", value: "opened" },
        { path: "sender", op: "present" },
      ],
    }),
    "x-github-event = push · action = opened · sender present",
  );
  assert.equal(ruleSummary({ headers: [], payload: [] }), "any event");
  assert.equal(ruleSummary({ headers: null, payload: null }), "any event");
});

test("blank editor rows are dropped before a rule is saved", () => {
  assert.deepEqual(
    cleanRule({
      headers: [{ name: " ", value: "x" }, { name: "X-A", value: "1" }],
      payload: [
        { path: "", op: "present" },
        { path: "a", op: "present", value: "stale" },
        { path: "b", op: "equals", value: "2" },
      ],
    }),
    {
      headers: [{ name: "X-A", value: "1" }],
      payload: [
        { path: "a", op: "present" },
        { path: "b", op: "equals", value: "2" },
      ],
    },
  );
});
