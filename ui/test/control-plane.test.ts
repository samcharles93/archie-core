import assert from "node:assert/strict";
import test from "node:test";

import {
  commandBody,
  upsertWorkflowDefinition,
  restoreShippedDefinitions,
  removeWorkflowDefinition,
  applyStatusForKind,
} from "../src/stores/control-plane.ts";

test("control-plane commands keep the edited version and typed values", () => {
  assert.deepEqual(
    JSON.parse(
      commandBody(
        {
          max_model_tool_steps: 12,
          max_runtime_seconds: 900,
          max_consecutive_gate_failures: 3,
        },
        7,
      ),
    ),
    {
      value: {
        max_model_tool_steps: 12,
        max_runtime_seconds: 900,
        max_consecutive_gate_failures: 3,
      },
      expected_version: 7,
    },
  );
});

test("workflow edits replace by id without dropping sibling definitions", () => {
  const original = {
    definitions: [
      { id: "implement", yaml: "id: implement\nsteps: []\n" },
      { id: "tdd", yaml: "id: tdd\nsteps: []\n" },
    ],
  };
  assert.deepEqual(
    upsertWorkflowDefinition(original, {
      id: "implement",
      yaml: "id: implement\nsteps:\n  - type: implement.code\n",
    }),
    {
      definitions: [
        {
          id: "implement",
          yaml: "id: implement\nsteps:\n  - type: implement.code\n",
        },
        { id: "tdd", yaml: "id: tdd\nsteps: []\n" },
      ],
    },
  );
  assert.deepEqual(removeWorkflowDefinition(original, "implement"), {
    definitions: [{ id: "tdd", yaml: "id: tdd\nsteps: []\n" }],
  });
  assert.deepEqual(
    restoreShippedDefinitions(
      { definitions: [...original.definitions, { id: "mine", yaml: "id: mine\n" }] },
      { definitions: [{ id: "implement", yaml: "shipped\n" }] },
    ),
    {
      definitions: [
        { id: "implement", yaml: "shipped\n" },
        { id: "tdd", yaml: "id: tdd\nsteps: []\n" },
        { id: "mine", yaml: "id: mine\n" },
      ],
    },
  );
});

test("apply status names a process behind the stored version and one that stopped reporting", () => {
  const records = [
    {
      process: "archied",
      kind: "tool-settings",
      applied_version: 7,
      reported_at: "",
      state: "current",
    },
    {
      process: "archie-gateway",
      kind: "tool-settings",
      applied_version: 6,
      reported_at: "",
      state: "current",
    },
    {
      process: "archie-messaging",
      kind: "tool-settings",
      applied_version: 7,
      reported_at: "",
      state: "unknown",
    },
    {
      process: "archied",
      kind: "plugin-settings",
      applied_version: 2,
      reported_at: "",
      state: "current",
    },
  ];
  const processes = ["archied", "archie-gateway", "archie-messaging"];

  assert.deepEqual(applyStatusForKind(records, processes, "tool-settings", 7), [
    { process: "archied", state: "running", version: 7, error: "", storedVersion: 7, reportedAt: "" },
    {
      process: "archie-gateway",
      state: "pending-restart",
      version: 6,
      error: "", storedVersion: 7, reportedAt: "",
    },
    { process: "archie-messaging", state: "unknown", version: 7, error: "", storedVersion: 7, reportedAt: "" },
  ]);
});

test("apply status shows a process that has never reported, and one that rejected the edit", () => {
  const processes = ["archied", "archie-gateway"];
  const records = [
    {
      process: "archied",
      kind: "tool-settings",
      applied_version: 6,
      reported_at: "",
      state: "failed",
      error: "validate database settings: bad policy", storedVersion: 7, reportedAt: "",
    },
  ];

  assert.deepEqual(applyStatusForKind(records, processes, "tool-settings", 7), [
    {
      process: "archied",
      state: "failed",
      version: 6,
      error: "validate database settings: bad policy", storedVersion: 7, reportedAt: "",
    },
    {
      process: "archie-gateway",
      state: "not-reporting",
      version: 0,
      error: "", storedVersion: 7,
    },
  ]);
});
