import { test } from "node:test";
import assert from "node:assert/strict";
import { waitFor } from "@testing-library/preact";

// The chat is a FAB-opened panel, not a route. These tests hold that shape:
// there is exactly one way in (the floating button), the panel anchors beside
// it rather than filling the right edge, and `#/chat` is not a destination.
//
// main.jsx starts on import and cannot be imported twice, so this file drives
// the same live app the routing test does.
globalThis.fetch = async () => {
  throw new Error("no daemon in this test");
};

class InertEventSource {
  close() {}
}
globalThis.EventSource = InertEventSource;

const fab = () => document.querySelector(".chat-fab");
const drawer = () => document.querySelector(".chat-drawer");

async function settle() {
  await new Promise((resolve) => setTimeout(resolve, 0));
}

test("chat is reachable only from the floating button", async (t) => {
  document.body.innerHTML = '<div id="app"></div>';
  location.hash = "#/";
  await import("../src/main.jsx");
  await waitFor(() => assert.ok(document.querySelector(".topbar")));

  await t.test("the nav has no Chat entry", () => {
    const labels = [...document.querySelectorAll("a.nav-item .nav-label")].map((el) => el.textContent);
    assert.ok(!labels.includes("Chat"), `nav still offers Chat: ${labels.join(", ")}`);
    assert.equal(document.querySelector('a.nav-item[href="#/chat"]'), null);
  });

  await t.test("the topbar has no chat icon button", () => {
    assert.equal(document.querySelector(".topbar .icon-btn-chat"), null);
  });

  await t.test("the floating button is present and announces its state", () => {
    assert.ok(fab(), "no .chat-fab rendered");
    assert.equal(fab().getAttribute("aria-expanded"), "false");
    assert.ok(fab().getAttribute("aria-controls"), "FAB does not point at the panel it opens");
  });
});

test("the floating button opens and closes the panel", async () => {
  assert.ok(fab(), "no .chat-fab rendered");
  assert.ok(!drawer().classList.contains("is-open"));

  fab().click();
  await waitFor(() => assert.ok(drawer().classList.contains("is-open")));
  assert.equal(fab().getAttribute("aria-expanded"), "true");

  // The panel it controls is the one that actually opened.
  assert.equal(document.getElementById(fab().getAttribute("aria-controls")), drawer());

  fab().click();
  await waitFor(() => assert.ok(!drawer().classList.contains("is-open")));
  assert.equal(fab().getAttribute("aria-expanded"), "false");
});

test("Escape closes the panel and returns focus to the button", async () => {
  fab().click();
  await waitFor(() => assert.ok(drawer().classList.contains("is-open")));

  // The class lands with the render, but the effect that binds Escape runs
  // after paint. Pressing the key on each retry means the test waits for the
  // binding rather than for a guessed number of milliseconds.
  await waitFor(() => {
    document.dispatchEvent(new window.KeyboardEvent("keydown", { key: "Escape", bubbles: true }));
    assert.ok(!drawer().classList.contains("is-open"));
  });
  assert.equal(document.activeElement, fab(), "focus was not returned to the launcher");
});

test("#/chat is not a route and does not blank the page", async () => {
  location.hash = "#/chat";
  await settle();
  await waitFor(() => {
    const main = document.querySelector(".main");
    assert.ok(main && main.textContent.trim().length > 0, "#/chat rendered an empty page");
  });
  // It resolves to the dashboard rather than to a chat page.
  assert.equal(document.querySelector('a.nav-item[href="#/"]')?.getAttribute("aria-current"), "page");
});
