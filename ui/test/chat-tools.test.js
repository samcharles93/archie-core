import { test } from "node:test";
import assert from "node:assert/strict";
import "./shim.js";
import { assistantBubble } from "../src/chat/chat-render.js";
import { appendToolCall } from "../src/chat/chat-tools.js";

// childIndex reports where a node sits among its parent's children, which is
// how the tests assert that the work is listed above the answer.
function childIndex(parent, selector) {
  const node = parent.querySelector(selector);
  return parent.childNodes.indexOf(node);
}

test("a tool call is listed above the answer it produced", () => {
  const row = assistantBubble();
  const host = row.querySelector(".chat-bubble");

  appendToolCall(host, { tool: "shell", text: "exit 0" });

  const tools = host.querySelector(".chat-tools");
  assert.equal(tools.childNodes.length, 1);
  assert.equal(host.querySelector(".chat-tool-name").textContent, "shell");
  assert.equal(host.querySelector(".chat-tool-summary").textContent, "exit 0");
  assert.ok(
    childIndex(host, ".chat-tools") < childIndex(host, ".chat-bubble-text"),
    "tool activity must render above the reply text",
  );
});

test("further tool calls join the same list in order", () => {
  const row = assistantBubble();
  const host = row.querySelector(".chat-bubble");

  appendToolCall(host, { tool: "memory_edit", text: "added entry" });
  appendToolCall(host, { tool: "shell", text: "exit 0" });

  const list = host.querySelector(".chat-tools");
  const names = list.childNodes.map((line) => line.querySelector(".chat-tool-name").textContent);
  assert.deepEqual(names, ["memory_edit", "shell"]);
});

test("a failed tool call is marked so it does not read as success", () => {
  const row = assistantBubble();
  const host = row.querySelector(".chat-bubble");

  appendToolCall(host, { tool: "read", text: "failed: no such file" });

  const line = host.querySelector(".chat-tool");
  assert.match(line.className, /failed/);
});

test("a successful tool call is not marked as failed", () => {
  const row = assistantBubble();
  const host = row.querySelector(".chat-bubble");

  appendToolCall(host, { tool: "read", text: "42 lines" });

  assert.doesNotMatch(host.querySelector(".chat-tool").className, /failed/);
});

test("a tool event with no name is ignored rather than rendered blank", () => {
  const row = assistantBubble();
  const host = row.querySelector(".chat-bubble");

  appendToolCall(host, { tool: "", text: "exit 0" });

  assert.equal(host.querySelector(".chat-tools").childNodes.length, 0);
});

// A bubble that predates the tool list  --  a retried turn reusing an older
// one  --  must still show tool activity rather than silently dropping it.
test("a bubble without a tool list still records tool activity", () => {
  const row = assistantBubble();
  const host = row.querySelector(".chat-bubble");
  host.childNodes = host.childNodes.filter((node) => node.className !== "chat-tools");

  appendToolCall(host, { tool: "shell", text: "exit 0" });

  assert.equal(host.querySelector(".chat-tool-name").textContent, "shell");
});
