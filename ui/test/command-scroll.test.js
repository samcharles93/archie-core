import { test } from "node:test";
import assert from "node:assert/strict";
import { commandScrollTop } from "../src/chat/command-scroll.js";

const menu = { scrollTop: 0, viewportHeight: 100 };

test("an option already in view does not move the list", () => {
  assert.equal(commandScrollTop({ ...menu, optionTop: 20, optionHeight: 30 }), 0);
});

test("an option below the fold scrolls down by the minimum", () => {
  // Bottom at 150 against a viewport ending at 100: scroll the missing 50.
  assert.equal(commandScrollTop({ ...menu, optionTop: 120, optionHeight: 30 }), 50);
});

test("an option above the fold scrolls up to its top", () => {
  assert.equal(
    commandScrollTop({ scrollTop: 200, viewportHeight: 100, optionTop: 120, optionHeight: 30 }),
    120,
  );
});

test("wrapping from the last option back to the first returns to the top", () => {
  assert.equal(
    commandScrollTop({ scrollTop: 400, viewportHeight: 100, optionTop: 0, optionHeight: 30 }),
    0,
  );
});

test("wrapping from the first option to the last scrolls to the end", () => {
  assert.equal(
    commandScrollTop({ scrollTop: 0, viewportHeight: 100, optionTop: 470, optionHeight: 30 }),
    400,
  );
});

test("an option taller than the viewport aligns to its top edge", () => {
  // Scrolling to its bottom would hide the command name; the top is what matters.
  assert.equal(commandScrollTop({ ...menu, optionTop: 40, optionHeight: 300 }), 40);
});
