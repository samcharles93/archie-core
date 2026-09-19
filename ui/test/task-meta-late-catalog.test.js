import { test } from "node:test";
import assert from "node:assert/strict";
import { waitFor } from "@testing-library/preact";

// Greptile's finding on the first cut of the served-vocabulary fix: the catalog
// resolves after a page has painted, and loadTaskMeta() only mutated module
// state. The route tree renders into its own root, so nothing re-rendered it
// and the painted page kept the freeze-dried labels.
//
// This lives in its own file because main.jsx boots on import and renders into
// #app: the module registry is per process, so a second test in the same file
// would reset the DOM of an app that never re-mounts and assert against a
// detached tree.
import { api } from "../src/base/api.jsx";

// A read that never settles. The page must not be able to pick up the catalog by
// re-rendering for its own reasons, or this test would pass without the
// re-render it exists to pin -- which is what the first version of it did: a
// failing fetch was enough to re-render the page and drag the served labels in.
// With the read pending, the catalog landing is the only thing that can re-render
// it. The status filter renders above the list's loading state, so it is on
// screen either way.
globalThis.fetch = () => new Promise(() => {});

class InertEventSource {
  close() {}
}
globalThis.EventSource = InertEventSource;

test("a page that painted before the catalog lands picks up the served vocabulary", async () => {
  api.taskMeta = async () => ({
    statuses: [{ id: "custom_status", label: "Custom status", kind: "danger", needs_you: true }],
    actions: [],
  });
  api.capabilities = async () => ({});

  document.body.innerHTML = '<div id="app"></div>';
  location.hash = "#/tasks";
  await import("../src/main.jsx");

  // The status filter renders one option per known status, so its option list
  // is the painted page's own view of the vocabulary. It renders above the
  // list's loading and failure states, which is why this assertion does not
  // depend on the tasks read (which cannot succeed here).
  await waitFor(() => {
    const options = [...document.querySelectorAll(".task-filter option")].map((option) => option.textContent);
    assert.ok(
      options.includes("Custom status"),
      `the painted filter must re-render with the served statuses, got: ${options.join(" | ")}`,
    );
  });
});
