import { test } from "node:test";
import assert from "node:assert/strict";
import { render } from "@testing-library/preact";
import { ChatApp } from "../src/chat/chat.jsx";

// ChatApp lives in a ~440px panel hung off the launcher, never on a page of its
// own. These tests hold that shape: one column, no page furniture, and the
// controls that are not conversation kept out of the reading area
// (archie-core-hesx).

test("the panel is a single column: no page title, no persistent sidebar", () => {
  const { container, unmount } = render(<ChatApp />);

  assert.equal(container.querySelector(".chat-topbar"), null, "page topbar should be gone");
  assert.equal(container.querySelector(".chat-kicker"), null, "ARCHIE WORKSPACE kicker should be gone");
  assert.equal(container.querySelector(".page-title"), null, "the panel header already names Archie");
  assert.equal(container.querySelector(".chat-sidebar"), null, "conversations should not hold a column");
  unmount();
});

test("the transcript and composer are the panel's own rows", () => {
  const { container, unmount } = render(<ChatApp />);

  assert.ok(container.querySelector(".chat-transcript"), "transcript should render");
  assert.ok(container.querySelector(".chat-composer"), "composer should render");
  unmount();
});

test("conversations are a switcher in the bar, not a column", () => {
  const { container, unmount } = render(<ChatApp />);

  const bar = container.querySelector(".chat-bar");
  assert.ok(bar, "compact bar should render");
  assert.ok(bar.querySelector(".chat-session-switch"), "session switcher should live in the bar");
  assert.ok(bar.querySelector(".chat-new"), "new chat should live in the bar");
  unmount();
});

test("model controls are behind one settings affordance, not laid out inline", () => {
  const { container, unmount } = render(<ChatApp />);

  const settings = container.querySelector(".chat-settings");
  assert.ok(settings, "settings popover should render");
  assert.ok(settings.querySelector('[aria-label="Personality"]'), "personality lives in the popover");
  assert.ok(settings.querySelector('[aria-label="Model"]'), "model lives in the popover");

  // Closed by default: a 440px panel cannot spend its width on three selects.
  assert.equal(settings.tagName, "DETAILS");
  assert.equal(settings.hasAttribute("open"), false);
  unmount();
});

test("operator panels are not rendered in the chat panel", () => {
  const { container, unmount } = render(<ChatApp />);

  assert.equal(container.querySelector(".chat-update-panel"), null, "update panel belongs to Configuration");
  assert.equal(container.querySelector(".chat-dangerous-panel"), null, "dangerous actions belong to Configuration");
  unmount();
});

test("the empty state still offers its prompts", () => {
  const { container, unmount } = render(<ChatApp />);

  assert.ok(container.querySelector(".chat-empty-state"), "empty state should be present");
  assert.ok(container.querySelectorAll(".chat-prompt").length >= 3, "should have prompt buttons");
  unmount();
});
