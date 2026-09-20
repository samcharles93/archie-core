import { test } from "node:test";
import assert from "node:assert/strict";
import { render } from "@testing-library/preact";
import { ChatApp } from "../src/chat/chat.jsx";
import { panelTitle } from "../src/chat/chat-render.jsx";

// Remediation from testing the launcher panel (archie-core-ifay): the panel
// should read as a chat window, not as a toolbar with a transcript attached.

test("the header names the open conversation", () => {
  const sessions = [
    { session_id: "abc12345", title: "Gate failures" },
    { session_id: "def67890", branch_name: "spike/retry" },
  ];
  assert.equal(panelTitle(sessions, "abc12345"), "Gate failures");
  assert.equal(panelTitle(sessions, "def67890"), "spike/retry");
});

test("with no conversation open the header falls back to the product name", () => {
  assert.equal(panelTitle([], ""), "Archie");
  assert.equal(panelTitle(undefined, undefined), "Archie");
  // A session id with no matching record must not render "undefined".
  assert.equal(panelTitle([], "missing"), "Archie");
});

test("the commands accordion is gone; the slash palette is the only command surface", () => {
  const { container, unmount } = render(<ChatApp />);
  assert.equal(container.querySelector(".chat-command-help"), null);
  unmount();
});

test("the composer carries no keyboard hint text", () => {
  const { container, unmount } = render(<ChatApp />);
  assert.ok(
    !/Enter to send/i.test(container.textContent),
    "the Enter/Shift+Enter hint should be gone",
  );
  unmount();
});

test("Stop is absent while nothing is generating", () => {
  const { container, unmount } = render(<ChatApp />);
  const labels = [...container.querySelectorAll(".chat-compose button")].map((b) => b.textContent.trim());
  assert.ok(!labels.includes("Stop"), `Stop should not render when idle: ${labels.join(", ")}`);
  assert.ok(labels.includes("Send"), "Send should still render");
  unmount();
});

test("new chat lives in the header, and the panel has no close button", () => {
  const { container, unmount } = render(<ChatApp />);
  const head = container.querySelector(".chat-drawer-head");
  assert.ok(head, "the panel owns its header");
  assert.ok(head.querySelector(".chat-new"), "new chat should sit in the header");
  assert.equal(container.querySelector(".chat-bar .chat-new"), null, "and not in the bar");

  // The launcher stays visible and is the only control that closes the panel.
  const closers = [...container.querySelectorAll("button")].filter(
    (b) => /close/i.test(b.getAttribute("aria-label") || ""),
  );
  assert.deepEqual(closers, [], "the panel should carry no close button");
  unmount();
});

test("message bubbles carry no speaker label", () => {
  const { container, unmount } = render(<ChatApp />);
  assert.equal(container.querySelector(".chat-bubble-meta"), null);
  unmount();
});
