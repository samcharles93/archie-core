import assert from "node:assert/strict";
import test from "node:test";
import { parse } from "yaml";

const { addBranch, deleteBranch, deleteStep, insertStep, renameBranch } = await import("../src/workflows/workflow-edit.ts");
const { workflowGraph } = await import("../src/workflows/workflow-graph.ts");
const source = `id: w
steps:
  - id: research
    parallel:
      # Keep the branch comment.
      code: [{id: read, type: agent.run, settings: {mission: Read code, read_only: true}}]
      docs: [{type: agent.run, settings: {mission: Read docs, read_only: true}}]
  - {id: finish, type: workflow.finish}
`;
const path = ["steps", 0];

test("parallel authoring preserves branches and their YAML while guarding destructive edits", () => {
  const cases: [string, () => void][] = [
    ["rename preserves order, comments and steps", () => {
      const renamed = renameBranch(source, path, "code", "review-code");
      assert.match(renamed, /Keep the branch comment/);
      const before = parse(source), after = parse(renamed);
      assert.deepEqual(Object.keys(after.steps[0].parallel), ["review-code", "docs"]);
      assert.deepEqual(after.steps[0].parallel["review-code"], before.steps[0].parallel.code);
      assert.deepEqual(after.steps[1], before.steps[1]);
    }],
    ["rename cannot overwrite a sibling", () => assert.throws(() => renameBranch(source, path, "code", "docs"), /already exists/)],
    ["branch names follow the engine grammar", () => {
      for (const name of ["Bad", "with space", "a..b", "", "__proto__"]) assert.throws(() => addBranch(source, path, name), /lowercase branch name/);
    }],
    ["add and remove a branch leaves its siblings unchanged", () => {
      const added = addBranch(source, path, "checks");
      assert.deepEqual(parse(added).steps[0].parallel.checks, []);
      assert.deepEqual(parse(deleteBranch(added, path, "checks")), parse(source));
      assert.throws(() => deleteBranch(source, path, "docs"), /at least two branches/);
    }],
    ["last step removal leaves a reachable empty branch", () => {
      const empty = deleteStep(source, ["steps", 0, "parallel", "code", 0]);
      assert.deepEqual(parse(empty).steps[0].parallel.code, []);
      assert.ok(workflowGraph(empty).nodes.some((node) => node.type === "add" && node.data.branch === "code"));
    }],
    ["branch insertion defaults agents to read-only and refuses unsafe steps", () => {
      const inserted = insertStep(source, ["steps", 0, "parallel", "code", -1], "agent.run");
      assert.equal(parse(inserted.source).steps[0].parallel.code[0].settings.read_only, true);
      assert.equal(parse(inserted.source).steps[0].parallel.code[1].id, "read");
      for (const type of ["parallel", "repo.commit", "command.run"]) assert.throws(() => insertStep(source, ["steps", 0, "parallel", "code", 0], type), /Branches allow only/);
    }],
    ["every branch and the workflow end has the correct insertion path", () => {
      const graph = workflowGraph(source);
      assert.deepEqual(graph.nodes.find((node) => node.type === "add" && !node.data.branch)?.data.insertAfter, ["steps", 1]);
      for (const branch of ["code", "docs"]) {
        assert.ok(graph.edges.some((edge) => JSON.stringify(edge.insertAfter) === JSON.stringify(["steps", 0, "parallel", branch, -1])));
        assert.deepEqual(graph.nodes.find((node) => node.type === "add" && node.data.branch === branch)?.data.insertAfter, ["steps", 0, "parallel", branch, 0]);
      }
      const inserted = insertStep(source, graph.nodes.find((node) => node.type === "add" && !node.data.branch)!.data.insertAfter!, "parallel");
      assert.deepEqual(parse(inserted.source).steps.slice(0, 2), parse(source).steps);
      const branches = Object.values(parse(inserted.source).steps[2].parallel);
      assert.equal(branches.length, 2);
      assert.ok(branches.every((steps) => Array.isArray(steps) && steps.length === 0));
    }],
  ];
  for (const [name, check] of cases) {
    try { check(); } catch (error) { throw new Error(name, { cause: error }); }
  }
});
