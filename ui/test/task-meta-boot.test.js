import { test } from "node:test";
import assert from "node:assert/strict";
import { waitFor } from "@testing-library/preact";

// The end-to-end claim of the task-meta revival: a change made only on the
// backend (a status the dashboard has never hardcoded) reaches the rendered
// page. Everything else here fails like the daemon being absent, exactly as in
// main-routing.test.js, so the only successful read is /api/task-meta.
const SERVED = {
  statuses: [
    { id: "triaging", label: "Triaging", kind: "info", needs_you: false },
    { id: "waiting_human", label: "Waiting for you", kind: "warn", needs_you: true },
  ],
  actions: [],
  change_statuses: [{ id: "added", label: "Added" }],
  config_schema: "archie/task-config@1",
};

globalThis.fetch = async (path) => {
  if (String(path).includes("/api/task-meta")) {
    return { ok: true, status: 200, statusText: "OK", json: async () => SERVED };
  }
  throw new Error("no daemon in this test");
};

// jsdom has no EventSource and the shell subscribes on mount; an inert
// stand-in keeps this test about the vocabulary rather than the stream.
class InertEventSource {
  close() {}
}
globalThis.EventSource = InertEventSource;

test("a backend-only status reaches the rendered task filter", async () => {
  document.body.innerHTML = '<div id="app"></div>';
  location.hash = "#/tasks";
  await import("../src/main.jsx");

  // First paint is the snapshot; the served catalog arrives a tick later and
  // renderMountedRoute redraws the mounted tasks page in place.
  await waitFor(() => {
    const options = [...document.querySelectorAll(".task-filter option")].map((o) => o.textContent);
    assert.ok(
      options.includes("Triaging"),
      `the served status never reached the filter: ${JSON.stringify(options)}`,
    );
  });
});
