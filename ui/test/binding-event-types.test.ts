import assert from "node:assert/strict";
import test from "node:test";

const { eventTypeLabel } = await import("../src/captures/event-types.ts");
const { bindingPayload, draftFromBinding, emptyDraft, mappingsForEventType } =
  await import("../src/bindings/binding-draft.ts");
const { matchCountLabel } = await import("../src/mappings/match-count.ts");

const types = [
  { id: "et1", source: "gh", name: "push", rule: { headers: [], payload: [] } },
  {
    id: "et2",
    source: "fw",
    name: "blocked",
    rule: { headers: [], payload: [] },
  },
];
const mappings = [
  { id: "m1", name: "push fields", event_type_id: "et1" },
  { id: "m2", name: "blocked fields", event_type_id: "et2" },
  { id: "m3", name: "more push", event_type_id: "et1" },
];

test("an event type reads as source and name, and a missing one says so", () => {
  assert.equal(eventTypeLabel("et1", types), "gh / push");
  assert.equal(eventTypeLabel("", types), "No event type");
  assert.equal(eventTypeLabel(undefined, types), "No event type");
  assert.equal(eventTypeLabel("gone", types), "Deleted type");
});

test("the mapping picker offers only the chosen event type's mappings", () => {
  assert.deepEqual(
    mappingsForEventType(mappings, "et1").map((m) => m.id),
    ["m1", "m3"],
  );
  assert.deepEqual(mappingsForEventType(mappings, ""), []);
});

test("an edited binding opens on its mapping's event type and keeps its filter", () => {
  const draft = draftFromBinding(
    {
      id: "b1",
      name: "n",
      matcher: { source: "fw" },
      mapping_id: "m2",
      filter: 'severity == "high"',
    },
    mappings,
  );
  assert.equal(draft.eventTypeId, "et2");
  assert.equal(draft.filter, 'severity == "high"');
});

test("a binding is sent with the source of its event type and its filter", () => {
  const draft = {
    ...emptyDraft(),
    name: "n",
    eventTypeId: "et2",
    mappingId: "m2",
    workflow: "w",
    filter: " severity == 'high' ",
  };
  const body = bindingPayload(draft, types);
  assert.deepEqual(body.matcher, { source: "fw" });
  assert.equal(body.mapping_id, "m2");
  assert.equal(body.filter, "severity == 'high'");
  assert.equal("eventTypeId" in body, false);
});

test("a mapping's match count reads as a number of events or as never matched", () => {
  assert.equal(matchCountLabel({ match_count: 0 }), "Never matched");
  assert.equal(matchCountLabel({}), "Never matched");
  assert.equal(matchCountLabel({ match_count: 1 }), "1 event");
  assert.equal(matchCountLabel({ match_count: 12 }), "12 events");
});

test("a binding's inputs go out typed, and a no-repository workflow names no repo", async () => {
  const { constantValue, paramsForType, takesRepository } =
    await import("../src/bindings/binding-draft.ts");
  const workflow = {
    id: "investigate",
    name: "investigate",
    repository: "none",
    inputs: {
      src_ip: { type: "string", required: true },
      severity: { type: "number" },
      note: { type: "string" },
    },
  };
  const draft = {
    ...emptyDraft(),
    eventTypeId: "et2",
    workflow: "investigate",
    owner: "acme",
    repo: "api",
    inputs: {
      src_ip: { param: "ip", value: "" },
      severity: { param: "", value: "3" },
      note: { param: "", value: "" },
    },
  };
  const payload = bindingPayload(draft, types, workflow);
  assert.deepEqual(payload.inputs, {
    src_ip: { param: "ip" },
    severity: { value: 3 },
  });
  assert.equal(payload.owner, "");
  assert.equal(payload.repo_param, "");
  assert.equal(takesRepository(workflow), false);
  assert.equal(takesRepository(undefined), true);

  assert.equal(constantValue("true", "bool"), true);
  assert.equal(constantValue("high", "number"), "high");
  assert.deepEqual(constantValue('{"a":1}', "object"), { a: 1 });
  assert.deepEqual(
    paramsForType(
      [
        { name: "ip", type: "string" },
        { name: "n", type: "number" },
        { name: "x", type: "any" },
      ],
      "string",
    ).map((f) => f.name),
    ["ip", "x"],
  );
});

test("an edited binding's assignments come back into the draft", () => {
  const draft = draftFromBinding(
    {
      id: "b",
      name: "b",
      mapping_id: "m2",
      repo_param: "full",
      inputs: { a: { param: "ip" }, b: { value: 3 }, c: { value: "x" } },
    },
    mappings,
  );
  assert.equal(draft.repoParam, "full");
  assert.deepEqual(draft.inputs, {
    a: { param: "ip", value: "" },
    b: { param: "", value: "3" },
    c: { param: "", value: "x" },
  });
});
