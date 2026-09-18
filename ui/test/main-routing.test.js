import { test } from "node:test";
import assert from "node:assert/strict";
import { waitFor } from "@testing-library/preact";

// The router as the browser actually uses it: main.jsx is imported once (it
// starts on import), and every step below navigates by changing the hash and
// waiting for the app to follow. This is the only place the wiring between the
// route table, the matcher and the navigation highlight is exercised -- the
// matcher's own unit tests cannot see whether `show` hands the nav the section
// path or the detail path.
//
// archied is absent on purpose: every request fails, which is itself a state
// these pages must render honestly rather than blankly.
globalThis.fetch = async () => {
  throw new Error("no daemon in this test");
};

// jsdom has no EventSource, and the dashboard subscribes to the daemon's event
// stream the moment it mounts. An inert stand-in keeps this test about routing
// rather than about the stream.
class InertEventSource {
  close() {}
}
globalThis.EventSource = InertEventSource;

function navCurrent(path) {
  return document.querySelector(`a.nav-item[href="#${path}"]`)?.getAttribute("aria-current");
}

function title() {
  return document.querySelector(".page-title")?.textContent;
}

async function navigate(hash) {
  location.hash = hash;
  // jsdom queues its own hashchange for the assignment; waitFor lets it land.
  await new Promise((resolve) => setTimeout(resolve, 0));
}

test("the app routes deep links and keeps the navigation highlight honest", async (t) => {
  document.body.innerHTML = '<div id="app"></div>';
  location.hash = "#/tasks/42";
  await import("../src/main.jsx");
  await waitFor(() => assert.equal(title(), "Task #42"));

  await t.test("a per-task route renders the run page and keeps Tasks current", () => {
    assert.equal(title(), "Task #42");
    assert.ok(document.querySelector(".run-restart"), "the run controls should render");
    assert.equal(
      navCurrent("/tasks"),
      "page",
      "W9: a detail route must hand the nav its section, or Tasks silently loses aria-current",
    );
  });

  await t.test("the tasks list route still works", async () => {
    await navigate("#/tasks");
    await waitFor(() => assert.equal(title(), "Tasks"));
    assert.equal(navCurrent("/tasks"), "page");
    assert.equal(document.querySelector(".tab-bar"), null, "the run page is no longer mounted");
  });

  // W10: #/tasks?task=N and #/tasks?status=... are pinned deep links. The steps
  // below enter this route from the run page on purpose: the query string is an
  // entry state, so a navigation that only changes the query of the page already
  // mounted keeps the operator's own filter selection instead.
  await t.test("a tab and attempt in the query string open that panel", async () => {
    await navigate("#/tasks/7?tab=log&attempt=1");
    await waitFor(() => assert.equal(title(), "Task #7"));
    const selected = [...document.querySelectorAll('[role="tab"]')].find(
      (tab) => tab.getAttribute("aria-selected") === "true",
    );
    assert.equal(selected.textContent, "Log");
    // The panels read one attempt, and this deployment answered no reads at
    // all -- so the panel says that rather than claiming there is no attempt.
    await waitFor(() => assert.match(document.body.textContent, /Could not load this task's attempts/));
    assert.equal(navCurrent("/tasks"), "page");
  });

  await t.test("the existing task deep links keep working", async () => {
    await navigate("#/tasks?task=7&status=needs_you");
    await waitFor(() => assert.equal(title(), "Tasks"));
    assert.equal(document.querySelector(".task-filter").value, "needs_you");
    assert.equal(navCurrent("/tasks"), "page");
  });

  await t.test("an unmatched path falls back to the dashboard", async () => {
    await navigate("#/nope/deep");
    await waitFor(() => assert.ok(document.querySelector(".hero-title"), "the dashboard should render"));
    assert.equal(document.querySelector(".tab-bar"), null);
    assert.equal(navCurrent("/"), "page");
  });
});
