import { test } from "node:test";
import assert from "node:assert/strict";
import { render, cleanup, fireEvent } from "@testing-library/preact";
import { TabBar, panelId, tabId } from "../src/tasks/tab-bar.jsx";

const TABS = [
  { id: "stages", label: "Stages" },
  { id: "log", label: "Log" },
  { id: "changes", label: "Changed files" },
];

import { useState } from "preact/hooks";

function Harness({ initial = "stages", onSelect }) {
  const [active, setActive] = useState(initial);
  return (
    <TabBar
      tabs={TABS}
      active={active}
      onSelect={(id) => {
        onSelect?.(id);
        setActive(id);
      }}
      prefix="run"
    />
  );
}

function mount(active = "stages", onSelect) {
  const { container, unmount } = render(<Harness initial={active} onSelect={onSelect} />);
  return { root: container, unmount };
}

test("the tab list is a tablist of tabs with the active one selected", () => {
  const { root, unmount } = mount("log");
  const list = root.querySelector('[role="tablist"]');
  assert.ok(list, "a tablist should render");
  const tabs = [...root.querySelectorAll('[role="tab"]')];
  assert.equal(tabs.length, 3);
  assert.equal(tabs[0].getAttribute("aria-selected"), "false");
  assert.equal(tabs[1].getAttribute("aria-selected"), "true");
  assert.equal(tabs[0].textContent, "Stages");
  unmount();
});

// The panel is a separate element in the page, so the relationship has to be
// declared explicitly or assistive technology cannot connect them.
test("each tab points at the panel it controls", () => {
  const { root, unmount } = mount("stages");
  const tabs = [...root.querySelectorAll('[role="tab"]')];
  const expected = ["stages", "log", "changes"].map((id) => panelId("run", id));
  assert.deepEqual(tabs.map((tab) => tab.getAttribute("aria-controls")), expected);
  assert.deepEqual(tabs.map((tab) => tab.id), ["stages", "log", "changes"].map((id) => tabId("run", id)));
  unmount();
});

// Roving tabindex: the tab list is one stop in the page's tab order, not three.
test("only the active tab is in the page tab order", () => {
  const { root, unmount } = mount("changes");
  const tabs = [...root.querySelectorAll('[role="tab"]')];
  assert.deepEqual(tabs.map((tab) => tab.getAttribute("tabindex")), ["-1", "-1", "0"]);
  unmount();
});

test("clicking a tab selects it", () => {
  const selected = [];
  const { root, unmount } = mount("stages", (id) => selected.push(id));
  fireEvent.click(root.querySelectorAll('[role="tab"]')[2]);
  assert.deepEqual(selected, ["changes"]);
  unmount();
});

test("arrow keys move between tabs, wrap around, and select as they go", () => {
  const selected = [];
  const { root, unmount } = mount("stages", (id) => selected.push(id));
  const first = root.querySelectorAll('[role="tab"]')[0];
  first.focus();
  fireEvent.keyDown(first, { key: "ArrowRight" });
  fireEvent.keyDown(root.querySelectorAll('[role="tab"]')[1], { key: "ArrowRight" });
  fireEvent.keyDown(root.querySelectorAll('[role="tab"]')[2], { key: "ArrowRight" });
  assert.deepEqual(selected, ["log", "changes", "stages"], "arrow-right wraps to the first tab");
  unmount();
});

test("Home and End jump to the ends of the tab list", () => {
  const selected = [];
  const { root, unmount } = mount("log", (id) => selected.push(id));
  const active = root.querySelectorAll('[role="tab"]')[1];
  fireEvent.keyDown(active, { key: "End" });
  fireEvent.keyDown(root.querySelectorAll('[role="tab"]')[2], { key: "Home" });
  assert.deepEqual(selected, ["changes", "stages"]);
  unmount();
});

// Keyboard focus must follow selection: a roving-tabindex widget that moves the
// selection but leaves focus behind loses the keyboard user.
test("an arrow key moves keyboard focus with the selection", () => {
  const { root, unmount } = mount("stages");
  const first = root.querySelectorAll('[role="tab"]')[0];
  first.focus();
  fireEvent.keyDown(first, { key: "ArrowRight" });
  const tabs = [...root.querySelectorAll('[role="tab"]')];
  assert.equal(document.activeElement, tabs[1]);
  unmount();
});

test("an unrelated key does nothing", () => {
  const selected = [];
  const { root, unmount } = mount("stages", (id) => selected.push(id));
  fireEvent.keyDown(root.querySelectorAll('[role="tab"]')[0], { key: "a" });
  assert.deepEqual(selected, []);
  unmount();
});

test.after(() => cleanup());
