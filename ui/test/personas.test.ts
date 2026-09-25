import assert from "node:assert/strict";
import test from "node:test";

import {
  duplicatePersona,
  removePersona,
  renamePersona,
} from "../src/settings/personas.ts";

const collection = () => ({
  default: "archie",
  personas: [
    { name: "archie", prompt: "a" },
    { name: "concise", prompt: "c" },
  ],
});

test("renaming the default persona keeps it the default", () => {
  const c = collection();
  renamePersona(c, 0, "archer");
  assert.equal(c.personas[0]!.name, "archer");
  assert.equal(c.default, "archer");
  renamePersona(c, 1, "brief");
  assert.equal(c.default, "archer");
});

test("removing the default hands it to the first persona left", () => {
  const c = collection();
  assert.equal(removePersona(c, 0), true);
  assert.deepEqual(c, { default: "concise", personas: [{ name: "concise", prompt: "c" }] });
});

test("the last persona cannot be removed", () => {
  const c = { default: "a", personas: [{ name: "a", prompt: "p" }] };
  assert.equal(removePersona(c, 0), false);
  assert.equal(c.personas.length, 1);
});

test("a duplicate gets a free name and lands after the original", () => {
  const c = collection();
  assert.equal(duplicatePersona(c, 0), 1);
  assert.equal(duplicatePersona(c, 0), 1);
  assert.deepEqual(
    c.personas.map((p) => p.name),
    ["archie", "archie-copy-2", "archie-copy", "concise"],
  );
  assert.equal(c.default, "archie");
});
