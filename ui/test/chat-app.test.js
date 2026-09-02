import { test } from "node:test";
import assert from "node:assert/strict";
import "./shim.js";
import { h, render } from "preact";
import { ChatApp, ChatPage, chatPage } from "../src/chat/chat.jsx";

test("ChatApp renders structure with topbar, sidebar, transcript and composer", () => {
  const container = document.createElement("div");
  render(h(ChatApp, null), container);

  assert.ok(container.querySelector(".chat-topbar"), "topbar should render");
  assert.ok(container.querySelector(".chat-sidebar"), "sidebar should render");
  assert.ok(container.querySelector(".chat-transcript"), "transcript should render");
  assert.ok(container.querySelector(".chat-composer"), "composer textarea should render");
  assert.ok(container.querySelector(".chat-sidebar button"), "+ New chat button should exist");
});

test("ChatApp renders empty state with prompt chips when messages are empty", () => {
  const container = document.createElement("div");
  render(h(ChatApp, null), container);

  const emptyState = container.querySelector(".chat-empty-state");
  assert.ok(emptyState, "empty state should be present");
  const prompts = container.querySelectorAll(".chat-prompt");
  assert.ok(prompts.length >= 3, "should have prompt buttons");
});

test("chatPage bridge function returns a DOM element containing the ChatApp", () => {
  const node = chatPage();
  assert.ok(node instanceof Element, "chatPage should return an Element");
  assert.equal(node.className, "chat-page");
  assert.ok(node.querySelector(".chat-page-content"), "chat-page-content should be inside node");
});
