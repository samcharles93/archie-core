import assert from "node:assert/strict";
import test from "node:test";

import {
  declaredInputs,
  inputValues,
  missingRequiredInputs,
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

test("declared workflow inputs produce stable fields, including an empty form", () => {
  assert.deepEqual(declaredInputs(prReview), [
    { name: "depth", type: "string", required: false },
    { name: "pr_number", type: "number", required: true },
  ]);
  assert.deepEqual(declaredInputs({ id: "implement" }), []);
  assert.deepEqual(declaredInputs(undefined), []);
});

test("workflow input payloads convert declared types and omit empty inputs", () => {
  const cases: Array<[Record<string, string>, Record<string, unknown>]> = [
    [{ pr_number: "77", depth: "deep" }, { pr_number: 77, depth: "deep" }],
    [{ pr_number: "77" }, { pr_number: 77 }],
    [{ pr_number: "seventy-seven" }, { pr_number: "seventy-seven" }],
  ];
  for (const [values, expected] of cases) {
    assert.deepEqual(inputValues(declaredInputs(prReview), values), expected, JSON.stringify(values));
  }
});

test("only missing required workflow inputs block submission", () => {
  const cases: Array<[Record<string, string>, string[]]> = [
    [{}, ["pr_number"]],
    [{ pr_number: "  " }, ["pr_number"]],
    [{ pr_number: "77" }, []],
  ];
  for (const [values, expected] of cases) {
    assert.deepEqual(missingRequiredInputs(declaredInputs(prReview), values), expected, JSON.stringify(values));
  }
});
