import assert from "node:assert/strict";
import test from "node:test";

const { tokenizeJson, tokenClass } = await import("../src/lib/json-tokens.ts");

test("a valid JSON document tokenizes into typed spans", () => {
  const tokens = tokenizeJson('{"name":"archie","count":3}');
  assert.ok(tokens, "valid JSON must tokenize");
  const kinds = tokens.map((t) => t.kind);
  assert.ok(tokens.some((t) => t.kind === "key" && t.text === '"name"'));
  assert.ok(tokens.some((t) => t.kind === "string" && t.text === '"archie"'));
  assert.ok(tokens.some((t) => t.kind === "number" && t.text === "3" || t.text.endsWith("3")));
  assert.ok(tokens.some((t) => t.kind === "punct" && t.text.includes("{")));
});

test("every character of the source survives, verbatim", () => {
  const source = '{"a":1,"b":[true,null],"c":"x\\u0041"}';
  const tokens = tokenizeJson(source);
  assert.ok(tokens);
  assert.equal(tokens.map((t) => t.text).join(""), source);
});

test("nested structures keep their punctuation", () => {
  const tokens = tokenizeJson('{"k":{"n":[1,2]}}');
  assert.ok(tokens);
  const open = tokens.filter((t) => t.kind === "punct" && t.text.includes("{"));
  assert.equal(open.length >= 1, true);
});

test("a non-JSON payload yields null, and the caller renders it plain", () => {
  assert.equal(tokenizeJson("not json at all"), null);
  assert.equal(tokenizeJson(""), null);
  assert.equal(tokenizeJson("{broken"), null);
});

test("an empty string is not JSON", () => {
  assert.equal(tokenizeJson(""), null);
});