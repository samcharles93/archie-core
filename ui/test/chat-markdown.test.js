import { test } from "node:test";
import assert from "node:assert/strict";
import "./shim.js";
import { renderMarkdown } from "../src/chat/markdown.js";
import { updateStreamingReply } from "../src/chat/chat-stream.js";

test("chat Markdown renders Telegram-style rich blocks as semantic HTML", () => {
  const rendered = renderMarkdown(
    "# Summary\n\n**bold**, *italic*, ~~removed~~, and [docs](https://example.com)\n\n- first\n- second\n\n> quoted\n\n```go\nfmt.Println(\"hi\")\n```\n\n| Name | State |\n| --- | --- |\n| Archie | **live** |",
  );

  assert.equal(rendered.querySelector("h1").textContent, "Summary");
  assert.equal(rendered.querySelector("strong").textContent, "bold");
  assert.equal(rendered.querySelector("em").textContent, "italic");
  assert.equal(rendered.querySelector("del").textContent, "removed");
  assert.equal(rendered.querySelector("a").getAttribute("href"), "https://example.com");
  assert.equal(rendered.querySelector("ul").children.length, 2);
  assert.equal(rendered.querySelector("blockquote").textContent, "quoted");
  assert.match(rendered.querySelector("pre").textContent, /fmt\.Println/);
  assert.equal(rendered.querySelector("table").querySelector("th").textContent, "Name");
  assert.equal(rendered.querySelector("table").querySelector("td").textContent, "Archie");
});

test("streaming assistant replies keep rich Markdown in the live bubble", () => {
  const bubble = renderMarkdown("draft");
  const updated = updateStreamingReply(bubble, "# Live\n\n**bold**");

  assert.equal(updated.querySelector("h1").textContent, "Live");
  assert.equal(updated.querySelector("strong").textContent, "bold");
  assert.equal(updated.className, "chat-bubble-text");
});
