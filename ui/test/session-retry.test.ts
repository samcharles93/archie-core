import assert from "node:assert/strict";
import test from "node:test";

const { createSessionRetry } = await import("../src/lib/session-retry.ts");

test("session retry awaits every load before settling", async () => {
  const order: string[] = [];
  let release: (() => void) | undefined;
  const retry = createSessionRetry([
    () => {
      order.push("first");
    },
    () =>
      new Promise<void>((resolve) => {
        release = () => {
          order.push("second");
          resolve();
        };
      }),
  ]);

  const done = retry();
  await Promise.resolve();
  assert.deepEqual(
    order,
    ["first"],
    "the retry settled before the second read finished",
  );
  release?.();
  await done;
  assert.deepEqual(order, ["first", "second"]);
});

test("session retry ignores a second call while one is running", async () => {
  let calls = 0;
  let release: (() => void) | undefined;
  const retry = createSessionRetry([
    () =>
      new Promise<void>((resolve) => {
        calls += 1;
        release = resolve;
      }),
  ]);

  const first = retry();
  const second = retry();
  release?.();
  await Promise.all([first, second]);

  assert.equal(calls, 1, "a double click re-ran the reads");
});

test("session retry runs again after a failure", async () => {
  let calls = 0;
  const retry = createSessionRetry([
    () => {
      calls += 1;
      if (calls === 1) throw new Error("daemon unavailable");
    },
  ]);

  await assert.rejects(retry(), /daemon unavailable/);
  await retry();
  assert.equal(calls, 2, "a failed retry left the guard latched");
});

test("session retry runs again after it settles", async () => {
  let calls = 0;
  const retry = createSessionRetry([
    () => {
      calls += 1;
    },
  ]);

  await retry();
  await retry();
  assert.equal(calls, 2);
});
