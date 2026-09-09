import { test } from "node:test";
import assert from "node:assert/strict";
import { render, cleanup } from "@testing-library/preact";
import { RepositoriesCard } from "../src/settings/settings.jsx";

const repos = [
  { owner: "acme", name: "widget", base: "main", allow_concurrent: true, max_retries: 3, review_enabled: false },
];

// A process that renders a published configuration snapshot has no write
// path: its PATCH routes answer 503. Offering a checkbox that fails on click
// is worse than not offering one (archie-core-ymut).
test("read-only repositories render values, not controls", () => {
  const { container, unmount } = render(<RepositoriesCard repos={repos} editable={false} />);
  assert.equal(container.querySelectorAll("input").length, 0, "a read-only page must render no repository inputs");
  const row = container.querySelector("tbody tr");
  assert.match(row.textContent, /acme\/widget/);
  assert.match(row.textContent, /3/, "the value itself is still shown");
  unmount();
});

test("editable repositories keep their controls", () => {
  const { container, unmount } = render(<RepositoriesCard repos={repos} editable={true} />);
  assert.ok(container.querySelectorAll("input").length > 0, "the owning process still edits repository fields");
  unmount();
});

test("cleanup", () => cleanup());
