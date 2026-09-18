import { test } from "node:test";
import assert from "node:assert/strict";
import { register } from "node:module";
import { render, waitFor } from "@testing-library/preact";

// tasks.jsx imports its feature CSS, which node cannot load; the same inline
// loader the sibling tasks tests register lets the module be imported here.
const cssLoad = "data:text/javascript," + encodeURIComponent(`
  export async function load(url, context, nextLoad) {
    if (url.endsWith(".css")) {
      return { format: "module", shortCircuit: true, source: "export default {};" };
    }
    return nextLoad(url, context);
  }
`);
register(cssLoad, import.meta.url);

const { api } = await import("../src/base/api.jsx");
const { tasksPage } = await import("../src/tasks/tasks.jsx");

// jsdom implements neither matchMedia nor scrollIntoView, so both are provided
// here to observe the one decision under test: which scroll behaviour a
// deep-linked row is revealed with.
function stubMotion(reduced) {
  window.matchMedia = (query) => ({
    matches: Boolean(reduced) && String(query).includes("prefers-reduced-motion: reduce"),
    media: String(query),
    onchange: null,
    addEventListener() {},
    removeEventListener() {},
    addListener() {},
    removeListener() {},
    dispatchEvent() {
      return false;
    },
  });
}

async function revealWithMotion(reduced) {
  const seen = [];
  const originalScroll = Element.prototype.scrollIntoView;
  const originalMatchMedia = window.matchMedia;
  const originalTasks = api.tasks;
  Element.prototype.scrollIntoView = (opts) => seen.push(opts);
  stubMotion(reduced);
  api.tasks = async () => [
    { id: 7, title: "Deep link", status: "running", repo: "o/r", workflow: "tdd", actions: [], attempt: 1 },
  ];
  try {
    const { unmount } = render(tasksPage(new URLSearchParams("task=7")));
    await waitFor(() => assert.ok(seen.length, "the deep-linked row should be revealed"));
    unmount();
    return seen[0];
  } finally {
    Element.prototype.scrollIntoView = originalScroll;
    window.matchMedia = originalMatchMedia;
    api.tasks = originalTasks;
  }
}

// The global reduced-motion rule switches CSS transitions, NOT JS smooth
// scrolling: a deep-linked row is revealed by scrollIntoView, so the behaviour
// has to be chosen in JS or a reduced-motion user still gets an animated scroll.
test("a revealed row scrolls smoothly only when motion is not reduced", async () => {
  const opts = await revealWithMotion(false);
  assert.equal(opts.behavior, "smooth");
  assert.equal(opts.block, "center");
});

test("a revealed row scrolls instantly when motion is reduced", async () => {
  const opts = await revealWithMotion(true);
  assert.equal(opts.behavior, "auto");
});
