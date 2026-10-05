import assert from "node:assert/strict";
import test from "node:test";

const { restartAt, workflowGraph, withRuns } = await import(
  "../src/workflows/workflow-graph.ts"
);
import type { StageRun } from "../src/workflows/workflow-graph.ts";

const PARALLEL = `id: w
steps:
  - parallel:
      build:
        - {id: compile, type: command.run, settings: {run: make}}
        - {id: test, type: command.run, settings: {run: go test}}
      docs:
        - {id: write, type: agent.run, settings: {mission: m, read_only: true}}
      lint:
        - {id: vet, type: command.run, when: "task.title", settings: {run: go vet}}
`;

test("withRuns maps every branch node to its own recorded row", () => {
  // The engine records each branch step as its own row, named for its branch.
  // One branch failing must not light its siblings.
  const graph = workflowGraph(PARALLEL);
  const stages: StageRun[] = [
    { name: "build/compile", status: "ok" },
    { name: "build/test", status: "failed", error: "boom" },
    { name: "docs/write", status: "ok" },
    { name: "lint/vet", status: "skipped" },
  ];
  const run = withRuns(graph, stages);
  const cases: Array<[(string | number)[], string, string]> = [
    [["steps", 0, "parallel", "build", 0], "build/compile", "ok"],
    [["steps", 0, "parallel", "build", 1], "build/test", "failed"],
    [["steps", 0, "parallel", "docs", 0], "docs/write", "ok"],
    [["steps", 0, "parallel", "lint", 0], "lint/vet", "skipped"],
  ];
  for (const [path, name, status] of cases) {
    const node = run.nodes.find(
      (candidate) => JSON.stringify(candidate.data.path) === JSON.stringify(path),
    );
    assert.ok(node, `no node at ${JSON.stringify(path)}`);
    assert.equal(node.data.run?.name, name, JSON.stringify(path));
    assert.equal(node.data.run?.status, status, JSON.stringify(path));
  }
});

test("a branch node still resumes the run at its parallel step", () => {
  const graph = withRuns(workflowGraph(PARALLEL), [
    { name: "build/test", status: "failed" },
  ]);
  assert.deepEqual(restartAt(graph, ["steps", 0, "parallel", "build", 1]), {
    label: "Resume from here",
    from: "parallel",
  });
});

test("a step the server cannot resume by name asks for an id", () => {
  const graph = withRuns(
    workflowGraph(`id: w
repository: none
steps:
  - {id: plan, type: agent.run, settings: {mission: p}}
  - {type: agent.run, settings: {mission: a}}
  - {type: agent.run, settings: {mission: b}}
  - {id: last, type: agent.run, settings: {mission: c}}
`),
    [
      { name: "plan", status: "succeeded" },
      { name: "agent.run", status: "succeeded" },
      { name: "agent.run", status: "succeeded" },
      { name: "last", status: "failed" },
    ],
  );
  assert.deepEqual(restartAt(graph, ["steps", 0]), { label: "Re-run from here", from: "plan" });
  // A repeated name, and a step whose predecessor's name repeats, are refused.
  for (const at of [1, 3]) {
    assert.equal(restartAt(graph, ["steps", at])?.blocked, true, `step ${at}`);
  }
});
