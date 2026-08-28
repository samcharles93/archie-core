import { test } from "node:test";
import assert from "node:assert/strict";
import { register } from "node:module";
import "./shim.js";

// tasks.js imports "./tasks.css", which Node's ESM loader cannot parse. Install
// a runtime loader hook (inline, via a data: URL so nothing extra is written to
// disk) that stubs .css imports to an empty module, then load tasks.js.
const cssLoad = "data:text/javascript," + encodeURIComponent(`
  export async function load(url, context, nextLoad) {
    if (url.endsWith(".css")) {
      return { format: "module", shortCircuit: true, source: "export default {};" };
    }
    return nextLoad(url, context);
  }
`);
register(cssLoad, import.meta.url);

const { api } = await import("../src/base/api.js");
const { tasksPage } = await import("../src/tasks/tasks.js");

// renderTask builds a page with a single task by stubbing api.tasks to a fake
// task, then returns the rendered root once tasksPage's background load() has
// settled. api.tasks is restored in a finally so the stub never leaks.
async function renderTask(task) {
  const original = api.tasks;
  api.tasks = async () => [task];
  try {
    const root = tasksPage(new URLSearchParams());
    await new Promise((resolve) => setTimeout(resolve, 0));
    return root;
  } finally {
    api.tasks = original;
  }
}

test("an action present in the catalog renders a button, not null", async () => {
  const task = {
    id: 7,
    title: "Fix the flaky test",
    status: "waiting_human",
    repo: "sam/archie",
    workflow: "tdd",
    actions: ["cancel"],
  };
  const root = await renderTask(task);
  const container = root.querySelector(".task-actions");
  assert.ok(container, "a known action should render a .task-actions container");
  const btn = container.querySelector("button");
  assert.ok(btn, "a button should be rendered for a catalog action");
  assert.ok(btn.className.includes("btn-quiet"), "cancel maps to the quiet variant");
  assert.equal(btn.textContent, "Cancel");
});

test("an id NOT in the catalog renders nothing", async () => {
  const task = {
    id: 8,
    title: "Poltergeist task",
    status: "waiting_human",
    repo: "sam/archie",
    workflow: "boot",
    actions: ["teleport"],
  };
  const root = await renderTask(task);
  // Unknown id -> no control renders, so no .task-actions container appears.
  assert.equal(root.querySelector(".task-actions"), null, "unknown action renders nothing");
});

test("a link-kind action renders an anchor to the forge", async () => {
  const task = {
    id: 9,
    title: "Open the PR",
    status: "pr_open",
    repo: "sam/archie",
    workflow: "tdd",
    issue_number: 12,
    pr_number: 42,
    pr_url: "https://forge.example.internal/sam/archie/pull/42",
    actions: ["open_pr"],
  };
  const root = await renderTask(task);
  const link = root.querySelector(".task-actions")?.querySelector("a");
  assert.ok(link, "open_pr should render an <a> not a <button>");
  assert.equal(link.textContent, "Open PR");
  assert.equal(link.getAttribute("href"), "https://forge.example.internal/sam/archie/pull/42");
});
