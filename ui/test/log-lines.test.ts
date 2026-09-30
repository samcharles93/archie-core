import assert from "node:assert/strict";
import test from "node:test";

const { attemptFooter, rowClass } = await import("../src/lib/log.ts");

test("an ERROR row is a block, not only red text", () => {
  const classes = rowClass("ERROR");
  assert.match(classes, /bg-danger-soft/);
  assert.match(classes, /border-l-danger/);
});

test("every other level keeps the plain row, still carrying its left edge", () => {
  for (const level of ["WARN", "INFO", "DEBUG", "info", "", null, undefined]) {
    const classes = rowClass(level);
    assert.doesNotMatch(classes, /danger/, `${String(level)} wore the error row`);
    assert.match(
      classes,
      /border-l-transparent/,
      `${String(level)} lost the left border every row carries, so its columns shift`,
    );
  }
});

test("the footer names the attempt and counts its lines", () => {
  assert.equal(attemptFooter(1, 1), "End of attempt 1 · 1 line");
  assert.equal(attemptFooter(2, 14), "End of attempt 2 · 14 lines");
});

test("an attempt with no lines still reports itself", () => {
  assert.equal(attemptFooter(1, 0), "End of attempt 1 · 0 lines");
});
