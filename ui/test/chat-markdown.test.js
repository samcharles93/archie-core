import { test } from "node:test";
import assert from "node:assert/strict";
import { h } from "preact";
import { render, cleanup } from "@testing-library/preact";
import { ChatMarkdown } from "../src/chat/markdown.jsx";

test("chat Markdown renders Telegram-style rich blocks as semantic HTML", () => {
  const { container } = render(
    <ChatMarkdown
      text={
        "# Summary\n\n**bold**, *italic*, ~~removed~~, and [docs](https://example.com)\n\n- first\n- second\n\n> quoted\n\n```go\nfmt.Println(\"hi\")\n```\n\n| Name | State |\n| --- | --- |\n| Archie | **live** |"
      }
    />,
  );
  const rendered = container.querySelector(".chat-bubble-text");

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
  cleanup();
});

test("chat Markdown renders images as img elements with src and alt", () => {
  const { container } = render(
    <ChatMarkdown text="Here is the generated image: ![generated preview](https://example.com/image.png)" />,
  );
  const img = container.querySelector("img");
  assert.ok(img, "img element should be present");
  assert.equal(img.getAttribute("src"), "https://example.com/image.png");
  assert.equal(img.getAttribute("alt"), "generated preview");
  cleanup();
});

// The blocks are keyed, so a streamed reply that grows by one line re-renders
// only what changed. Preact reports a duplicate-key mistake by rendering the
// wrong thing, so the shapes with sibling lists and tables are worth pinning.
test("chat Markdown keeps sibling blocks distinct when the text grows", () => {
  const { container, rerender } = render(<ChatMarkdown text={"- one\n- two"} />);
  assert.equal(container.querySelectorAll("ul").length, 1);
  rerender(<ChatMarkdown text={"- one\n- two\n- three"} />);
  assert.equal(container.querySelectorAll("ul").length, 1);
  assert.equal(container.querySelector("ul").children.length, 3);

  rerender(<ChatMarkdown text={"- one\n\n1. first\n2. second"} />);
  assert.equal(container.querySelectorAll("ul").length, 1);
  assert.equal(container.querySelectorAll("ol").length, 1);
  cleanup();
});
