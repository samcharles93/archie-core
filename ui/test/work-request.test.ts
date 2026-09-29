import assert from "node:assert/strict";
import test from "node:test";

import {
  declaredInputs,
  inputValues,
  missingRequiredInputs,
  type DeclaredInput,
} from "../src/workflows/work-request.ts";

// The shape GET /api/workflows reports for a definition, as far as the
// work-request form reads it.
const prReview = {
  id: "pr-review",
  name: "pr-review",
  enabled: true,
  inputs: {
    pr_number: { type: "number", required: true },
    depth: { type: "string" },
  },
};

test("the form renders one field per declared input, in a stable order", () => {
  assert.deepEqual(declaredInputs(prReview), [
    { name: "depth", type: "string", required: false },
    { name: "pr_number", type: "number", required: true },
  ]);
});

test("a workflow that declares no inputs renders no fields", () => {
  assert.deepEqual(declaredInputs({ id: "implement" }), []);
  assert.deepEqual(declaredInputs(undefined), []);
});

test("a declared number is sent as a number, not as the typed text", () => {
  assert.deepEqual(
    inputValues(declaredInputs(prReview), { pr_number: "77", depth: "deep" }),
    { pr_number: 77, depth: "deep" },
  );
});

test("an input left empty is absent rather than an empty string", () => {
  assert.deepEqual(inputValues(declaredInputs(prReview), { pr_number: "77" }), {
    pr_number: 77,
  });
});

test("text that is not the declared type is sent as typed, for the server to name", () => {
  assert.deepEqual(inputValues(declaredInputs(prReview), { pr_number: "seventy-seven" }), {
    pr_number: "seventy-seven",
  });
});

test("required inputs that are still empty are named", () => {
  const declared = declaredInputs(prReview);
  assert.deepEqual(missingRequiredInputs(declared, {}), ["pr_number"]);
  assert.deepEqual(missingRequiredInputs(declared, { pr_number: "  " }), ["pr_number"]);
  assert.deepEqual(missingRequiredInputs(declared, { pr_number: "77" }), []);
});

test("an optional input does not hold the form back", () => {
  const declared: DeclaredInput[] = declaredInputs(prReview);
  assert.deepEqual(missingRequiredInputs(declared, { pr_number: "1" }), []);
});
