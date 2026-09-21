import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";

const { blankFromSchema, fieldHint, fieldPlaceholder, fieldTitle, isKeyedCollection, parseSchema, schemaAt } = await import(
  "../src/settings/resource-schema.ts"
);

// The shapes the control-plane descriptors actually advertise (derived in
// internal/app/controlplane/schema.go), plus the two degenerate ones.
const objectSchema = {
  type: "object",
  properties: {
    email: { type: "object", properties: { listen_addr: { type: "string", title: "Listen address" } } },
    rate_limit: {
      type: "object",
      properties: {
        window: { type: "string", format: "duration" },
        max_requests: { type: "integer", description: "0 disables rate limiting." },
      },
    },
  },
};
const arraySchema = {
  type: "array",
  items: { type: "object", properties: { owner: { type: "string" }, base: { type: "string" } } },
};
const keyedSchema = { type: "object", additionalProperties: { type: "object", properties: { class: { type: "string" } } } };
const bareSchema = { type: "object" };

test("parseSchema tolerates a missing or unparseable schema", () => {
  assert.equal(parseSchema(undefined), null);
  assert.equal(parseSchema(""), null);
  assert.equal(parseSchema("{"), null);
  assert.deepEqual(parseSchema(JSON.stringify(bareSchema)), bareSchema);
});

test("schemaAt walks nested fields, array items and keyed-collection values", () => {
  assert.deepEqual(schemaAt(objectSchema, "channel-settings.rate_limit.window", "channel-settings"), {
    type: "string",
    format: "duration",
  });
  assert.deepEqual(schemaAt(arraySchema, "repositories.0.owner", "repositories"), { type: "string" });
  assert.deepEqual(schemaAt(keyedSchema, "providers.anything.class", "providers"), { type: "string" });
  assert.deepEqual(schemaAt(objectSchema, "channel-settings", "channel-settings"), objectSchema);
});

test("schemaAt answers nothing for a path the schema does not describe", () => {
  assert.equal(schemaAt(objectSchema, "channel-settings.telegram.token", "channel-settings"), null);
  assert.equal(schemaAt(null, "channel-settings.email", "channel-settings"), null);
  assert.equal(schemaAt(bareSchema, "channel-settings.email.listen_addr", "channel-settings"), null);
});

test("fieldTitle prefers the schema title over the dashed-key fallback", () => {
  assert.equal(fieldTitle("listen_addr", { title: "Listen address" }), "Listen address");
  assert.equal(fieldTitle("listen_addr", { type: "string" }), "Listen addr");
  assert.equal(fieldTitle("listen_addr", null), "Listen addr");
});

test("fieldPlaceholder offers a duration only where the schema says duration", () => {
  assert.equal(fieldPlaceholder({ type: "string", format: "duration" }), "1m0s");
  assert.equal(fieldPlaceholder({ type: "string", format: "date-time" }), "2026-01-01T00:00:00Z");
  assert.equal(fieldPlaceholder({ type: "string" }), undefined);
  assert.equal(fieldPlaceholder({ type: "integer" }), undefined);
  assert.equal(fieldPlaceholder(null), undefined);
});

test("fieldHint carries the schema's own prose, and nothing when it has none", () => {
  assert.equal(fieldHint({ description: "0 disables rate limiting." }), "0 disables rate limiting.");
  assert.equal(fieldHint({ type: "integer" }), "");
  assert.equal(fieldHint(null), "");
});

test("isKeyedCollection is the schema signal that keys are the operator's", () => {
  assert.equal(isKeyedCollection(keyedSchema), true);
  assert.equal(isKeyedCollection(objectSchema), false);
  assert.equal(isKeyedCollection(bareSchema), false);
  assert.equal(isKeyedCollection(null), false);
});

test("blankFromSchema synthesises a new row from the schema, not from a path", () => {
  assert.deepEqual(blankFromSchema(arraySchema.items), { owner: "", base: "" });
  assert.deepEqual(blankFromSchema(keyedSchema), {});
  assert.deepEqual(blankFromSchema({ type: "array", items: { type: "string" } }), []);
  assert.equal(blankFromSchema({ type: "boolean" }), false);
  assert.equal(blankFromSchema({ type: "integer" }), 0);
  assert.equal(blankFromSchema({ type: "string", format: "duration" }), "");
  assert.equal(blankFromSchema(null), "");
});

test("the live editor reads the schema, and no longer hardcodes document shapes", async () => {
  const editor = await readFile(new URL("../src/settings/StructuredValueEditor.vue", import.meta.url), "utf8");
  assert.match(editor, /fieldPlaceholder/, "the editor offers the duration placeholder from the schema");
  assert.match(editor, /fieldTitle/, "labels come from the schema title");
  assert.match(editor, /blankFromSchema/, "a new row is synthesised from the schema");
  for (const stale of ["base: \"main\"", "ecosystem: \"go\"", "transport: \"stdio\"", "interval: 3600000000000"]) {
    assert.ok(!editor.includes(stale), `the editor still hardcodes a document shape: ${stale}`);
  }
});

test("the resource card hands the derived schema to the editor", async () => {
  const card = await readFile(new URL("../src/settings/StructuredResourceCard.vue", import.meta.url), "utf8");
  assert.match(card, /parseSchema/, "the card parses the descriptor's schema");
  assert.match(card, /:schema=/, "the card passes it down");
});
