import { test } from "node:test";
import assert from "node:assert/strict";
import { render, cleanup } from "@testing-library/preact";
import { AttemptConfig, CONFIG_SCHEMA, selectConfigEvent } from "../src/tasks/attempt-config.jsx";

const EVENTS = [
  {
    kind: "config_captured",
    attempt: 1,
    at: "2026-09-18T07:00:00.100Z",
    stage: "",
    data: { schema: CONFIG_SCHEMA, document: { BotUser: "archie-attempt-one", Models: { main: "openai/gpt" } } },
  },
  {
    kind: "config_captured",
    attempt: 2,
    at: "2026-09-18T07:05:00.100Z",
    stage: "",
    data: { schema: CONFIG_SCHEMA, document: { BotUser: "archie-attempt-two", Models: { main: "openai/gpt" } } },
  },
];

function mount(props) {
  const { container, unmount } = render(<AttemptConfig {...props} />);
  return { root: container, unmount };
}

// Selecting by kind alone would show the newest attempt's configuration on
// every attempt -- the merge-attempts bug the attempt column exists to prevent.
test("the configuration is selected by kind and attempt together", () => {
  const { root, unmount } = mount({ events: EVENTS, attempt: 1 });
  assert.match(root.textContent, /archie-attempt-one/);
  assert.doesNotMatch(root.textContent, /archie-attempt-two/);
  assert.match(root.textContent, /attempt 1's effective task-runtime configuration/);
  unmount();
});

test("an attempt with no config event of its own says so, never another attempt's", () => {
  const { root, unmount } = mount({ events: EVENTS, attempt: 3 });
  assert.equal(root.querySelector(".empty-title").textContent, "Not captured for this run");
  assert.doesNotMatch(root.textContent, /archie-attempt-one|archie-attempt-two/);
  assert.match(root.textContent, /Nothing here says the run used the defaults/);
  unmount();
});

test("the document is passed through verbatim, pretty-printed", () => {
  const { root, unmount } = mount({ events: EVENTS, attempt: 2 });
  const json = root.querySelector(".run-json").textContent;
  assert.match(json, /"BotUser": "archie-attempt-two"/);
  assert.match(json, /"Models"/);
  unmount();
});

// The page must not imply this document is the whole configuration: it is the
// non-secret task-runtime subset.
test("the panel states the scope of what was captured", () => {
  const { root, unmount } = mount({ events: EVENTS, attempt: 1 });
  assert.match(root.textContent, /non-secret subset/);
  assert.match(root.textContent, /not the dashboard configuration view/);
  assert.match(root.textContent, /providers, repositories, identities, credentials and lock state/);
  unmount();
});

test("an unrecognised schema is named and its payload is still shown", () => {
  const events = [
    { kind: "config_captured", attempt: 2, at: "2026-09-18T07:05:00.100Z", data: { schema: "archie/task-config@9", document: { future: true } } },
  ];
  const { root, unmount } = mount({ events, attempt: 2 });
  assert.match(root.textContent, /unknown schema \(archie\/task-config@9\)/);
  assert.match(root.querySelector(".run-json").textContent, /"future": true/);
  unmount();
});

test("a config event with no data at all renders the raw payload rather than guessing", () => {
  const events = [{ kind: "config_captured", attempt: 2, at: "2026-09-18T07:05:00.100Z" }];
  const { root, unmount } = mount({ events, attempt: 2 });
  assert.match(root.textContent, /unknown schema/);
  assert.ok(root.querySelector(".run-json"));
  unmount();
});

test("loading and failure states are distinct, and the failure offers a retry", () => {
  const loading = mount({ events: undefined, attempt: 1 });
  assert.match(loading.root.textContent, /Loading this task's events/);
  loading.unmount();

  const failed = mount({ events: null, attempt: 1, onRetry: () => {} });
  assert.match(failed.root.textContent, /Could not load this task's events/);
  assert.equal(failed.root.querySelector("button").textContent, "Retry");
  failed.unmount();
});

test("selectConfigEvent ignores other kinds and other attempts", () => {
  const events = [
    { kind: "stage_finish", attempt: 2, data: {} },
    ...EVENTS,
  ];
  assert.equal(selectConfigEvent(events, 2).attempt, 2);
  assert.equal(selectConfigEvent(events, 3), null);
  assert.equal(selectConfigEvent(undefined, 1), null);
});

// CONFIG_SCHEMA mirrors events.ConfigCapturedSchema (internal/events/events.go),
// which the Go suite pins by the same literal. Pinning both sides is what makes
// a one-sided rename a failing test rather than every attempt rendering as
// "unknown schema".
test("the config schema is the one the producer stamps", () => {
  assert.equal(CONFIG_SCHEMA, "archie/task-config@1");
});

test.after(() => cleanup());
