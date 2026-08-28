import { test } from "node:test";
import assert from "node:assert/strict";
import { register } from "node:module";
import "./shim.js";

// settings.js imports ./settings.css, which Node's ESM loader cannot parse.
// Install a runtime loader hook (inline via a data: URL, as tasks-actions.test.js
// does for tasks.css) that stubs .css imports to an empty module, then load
// settings.js so the page can be driven under plain node --test.
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
const { settingsPage } = await import("../src/settings/settings.js");

// renderSettings builds the settings page with config/version/taskMeta stubbed
// to canned payloads, then returns the root once the background loads settle.
// The stubs are restored in a finally so they never leak.
async function renderSettings(meta) {
  const original = { config: api.config, version: api.version, taskMeta: api.taskMeta };
  api.config = async () => ({});
  api.version = async () => ({ components: [] });
  api.taskMeta = async () => meta;
  try {
    const root = settingsPage();
    await new Promise((resolve) => setTimeout(resolve, 0));
    return root;
  } finally {
    api.config = original.config;
    api.version = original.version;
    api.taskMeta = original.taskMeta;
  }
}

test("work lifecycle card renders a status pill and an action button from /api/task-meta", async () => {
  const root = await renderSettings({
    statuses: [
      { id: "waiting_human", label: "Waiting for you", kind: "warn", needs_you: true },
      { id: "queued", label: "Queued", kind: "idle" },
    ],
    actions: [
      { id: "cancel", label: "Cancel", kind: "quiet" },
      { id: "approve", label: "Approve", kind: "primary" },
    ],
  });

  const text = root.textContent;
  assert.ok(text.includes("Work lifecycle"), "the lifecycle card should be present");

  // Statuses render as pills carrying their severity token.
  const warnPill = root.querySelector(".pill-warn");
  assert.ok(warnPill, "a warn-kind status should render a warn pill");
  assert.equal(warnPill.textContent, "Waiting for you");
  assert.ok(text.includes("Queued"), "a second status should render its label");

  // Actions render as buttons carrying their variant token.
  const cancelBtn = root.querySelector(".btn-quiet");
  assert.ok(cancelBtn, "a quiet action should render a quiet-variant button");
  assert.equal(cancelBtn.textContent, "Cancel");
  assert.ok(text.includes("Approve"), "a primary action should render its label");
});

test("work lifecycle card shows a quiet empty state when the catalog fetch fails", async () => {
  const original = { config: api.config, version: api.version, taskMeta: api.taskMeta };
  api.config = async () => ({});
  api.version = async () => ({ components: [] });
  api.taskMeta = async () => {
    throw new Error("boom");
  };
  try {
    const root = settingsPage();
    await new Promise((resolve) => setTimeout(resolve, 0));
    assert.ok(root.textContent.includes("Work lifecycle"), "the card should still render");
    assert.ok(root.textContent.includes("Lifecycle unavailable"), "a failure should show the empty/error state");
  } finally {
    api.config = original.config;
    api.version = original.version;
    api.taskMeta = original.taskMeta;
  }
});
