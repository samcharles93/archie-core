import { test } from "node:test";
import assert from "node:assert/strict";
import { render, cleanup } from "@testing-library/preact";
import { ChangedFiles, fileStatusLabel } from "../src/tasks/changed-files.jsx";
import { applyTaskMeta } from "../src/base/task-meta.jsx";

const CAPTURE = {
  captured_at: "2026-09-18T07:03:00.000Z",
  captured_after: "commit",
  stage: "commit",
  owner: "o",
  repo: "r",
  base: "main",
  branch: "feat/42-x",
  head_sha: "abcdef1234567890",
  base_sha: "0987654321fedcba",
  pr_number: 7,
  totals: { files: 3, additions: 40, deletions: 5 },
  truncated: false,
  files: [
    { path: "internal/x.go", old_path: "", status: "modified", additions: 12, deletions: 3, binary: false },
    { path: "bin/app", old_path: "", status: "added", additions: 0, deletions: 0, binary: true },
    { path: "internal/new.go", old_path: "internal/old.go", status: "renamed", additions: 1, deletions: 1, binary: false },
  ],
};

const TASK = {
  id: 42,
  repo_url: "https://forge.example.internal/o/r",
  pr_number: 7,
  pr_url: "https://forge.example.internal/o/r/pull/7",
};

function mount(props) {
  const { container, unmount } = render(<ChangedFiles {...props} />);
  return { root: container, unmount };
}

function rowFor(root, path) {
  return [...root.querySelectorAll(".run-changes-table tbody tr")].find((row) =>
    row.querySelector(".run-changes-path")?.textContent.includes(path),
  );
}

// F5: nothing predating the capture has one, and the forge cannot supply one, so
// an empty view must not read as "this run changed nothing".
test("no capture recorded is not reported as no files changed", () => {
  const { root, unmount } = mount({ state: { task_id: 42, attempt: 2, found: false, captures: [] } });
  assert.equal(root.querySelector(".empty-title").textContent, "No change capture was recorded for this attempt");
  assert.match(root.textContent, /not the same as "no files changed"/);
  assert.doesNotMatch(root.textContent, /Nothing changed/);
  unmount();
});

test("a capture renders its per-file counts and statuses", () => {
  const { root, unmount } = mount({ state: { task_id: 42, attempt: 2, found: true, captures: [CAPTURE] }, task: TASK });
  const modified = rowFor(root, "internal/x.go");
  assert.match(modified.textContent, /Modified/);
  assert.doesNotMatch(modified.textContent, /modified/, "the raw status code must not be shown as a word");
  assert.match(modified.textContent, /\+12/);
  assert.equal(root.querySelector(".run-capture-totals").textContent, "3 files · +40 · −5");
  unmount();
});

// found:true with no readable capture is a READ failure, not "nothing was
// captured": the server reports found from the capture event's existence, so
// conflating the two tells the operator no capture was recorded when one
// demonstrably was.
test("a capture that was recorded but could not be read is not reported as missing", () => {
  const { root, unmount } = mount({ state: { task_id: 42, attempt: 2, found: true, captures: [] } });
  assert.match(root.textContent, /could not be read/i);
  assert.doesNotMatch(root.textContent, /No change capture was recorded/);
  unmount();
});

test("a renamed file shows the path it came from", () => {
  const { root, unmount } = mount({ state: { task_id: 42, attempt: 2, found: true, captures: [CAPTURE] } });
  const renamed = rowFor(root, "internal/new.go");
  assert.match(renamed.textContent, /Renamed/);
  assert.match(renamed.textContent, /internal\/old\.go → internal\/new\.go/);
  unmount();
});

// A zero-hunk file is "no textual hunks", which is what the capture actually
// recorded -- not a claim about the file's type on disk.
test("a file with no textual hunks is described that way, not as a binary", () => {
  const { root, unmount } = mount({ state: { task_id: 42, attempt: 2, found: true, captures: [CAPTURE] } });
  const binary = rowFor(root, "bin/app");
  assert.match(binary.textContent, /no textual hunks/);
  assert.match(binary.textContent, /Added/);
  assert.doesNotMatch(binary.textContent, /binary/i);
  unmount();
});

test("a truncated capture says so while still reporting complete totals", () => {
  const truncated = { ...CAPTURE, truncated: true, totals: { files: 210, additions: 900, deletions: 40 } };
  const { root, unmount } = mount({ state: { task_id: 42, attempt: 2, found: true, captures: [truncated] } });
  assert.match(root.textContent, /Showing the first 3 of 210 files/);
  assert.match(root.textContent, /210 files · \+900 · −40/);
  unmount();
});

test("the capture links to the repository and the pull request", () => {
  const { root, unmount } = mount({ state: { task_id: 42, attempt: 2, found: true, captures: [CAPTURE] }, task: TASK });
  const hrefs = [...root.querySelectorAll("a")].map((a) => a.getAttribute("href"));
  assert.ok(hrefs.includes("https://forge.example.internal/o/r"));
  assert.ok(hrefs.includes("https://forge.example.internal/o/r/pull/7"));
  assert.match(root.textContent, /feat\/42-x → main/);
  assert.match(root.textContent, /abcdef12/);
  unmount();
});

// A link is only rendered when there is a URL behind it: a capture with no forge
// coordinates shows its owner/repo and PR number as text rather than a dead link.
test("a capture with no forge URL shows plain text instead of a dead link", () => {
  const bare = { ...CAPTURE, pr_number: 9 };
  const { root, unmount } = mount({ state: { task_id: 42, attempt: 2, found: true, captures: [bare] } });
  assert.match(root.textContent, /PR #9/);
  assert.equal(root.querySelectorAll("a").length, 0);
  unmount();
});

test("multiple captures in one attempt all render, oldest first", () => {
  const second = {
    ...CAPTURE,
    captured_at: "2026-09-18T07:06:00.000Z",
    captured_after: "commit-push",
    stage: "push",
  };
  const { root, unmount } = mount({ state: { task_id: 42, attempt: 2, found: true, captures: [CAPTURE, second] } });
  const heads = [...root.querySelectorAll(".run-capture-when")].map((n) => n.textContent);
  assert.deepEqual(heads, ["Captured after commit in stage commit", "Captured after commit-push in stage push"]);
  unmount();
});

test("loading and failure states are distinct, and the failure offers a retry", () => {
  const loading = mount({ state: undefined });
  assert.match(loading.root.textContent, /Loading changed files/);
  loading.unmount();

  const failed = mount({ state: null, onRetry: () => {} });
  assert.match(failed.root.textContent, /Could not load this attempt's changed files/);
  assert.equal(failed.root.querySelector("button").textContent, "Retry");
  failed.unmount();
});

test("the status vocabulary is words, and an unknown status is passed through", () => {
  assert.equal(fileStatusLabel("added"), "Added");
  assert.equal(fileStatusLabel("typechange"), "Type changed");
  assert.equal(fileStatusLabel("submodule"), "submodule");
});

// The label rendered for a change status is the one the served catalog carries,
// not a UI literal: a backend rename reaches this table with no JS edit. The
// snapshot it starts from is pinned across the language boundary by
// ui/test/task-meta-catalogue.test.js against the Go fixture.
test("a served change-status label is what the table renders", () => {
  applyTaskMeta({ change_statuses: [{ id: "typechange", label: "Kind changed" }] });
  assert.equal(fileStatusLabel("typechange"), "Kind changed");
  const capture = {
    ...CAPTURE,
    files: [{ path: "internal/x.go", old_path: "", status: "typechange", additions: 1, deletions: 0, binary: false }],
  };
  const { root, unmount } = mount({ state: { task_id: 42, attempt: 2, found: true, captures: [capture] } });
  assert.match(rowFor(root, "internal/x.go").textContent, /Kind changed/);
  unmount();
});

test.after(() => cleanup());
