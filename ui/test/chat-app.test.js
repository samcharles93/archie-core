import { test } from "node:test";
import assert from "node:assert/strict";
import { render, cleanup } from "@testing-library/preact";
import { ChatApp } from "../src/chat/chat.jsx";

test("ChatApp renders structure with topbar, sidebar, transcript and composer", () => {
  const { container, unmount } = render(<ChatApp />);

  assert.ok(container.querySelector(".chat-topbar"), "topbar should render");
  assert.ok(container.querySelector(".chat-sidebar"), "sidebar should render");
  assert.ok(container.querySelector(".chat-transcript"), "transcript should render");
  assert.ok(container.querySelector(".chat-composer"), "composer textarea should render");
  assert.ok(container.querySelector(".chat-sidebar button"), "+ New chat button should exist");
  unmount();
});

test("ChatApp renders empty state with prompt chips when messages are empty", () => {
  const { container, unmount } = render(<ChatApp />);

  const emptyState = container.querySelector(".chat-empty-state");
  assert.ok(emptyState, "empty state should be present");
  const prompts = container.querySelectorAll(".chat-prompt");
  assert.ok(prompts.length >= 3, "should have prompt buttons");
  unmount();
});
