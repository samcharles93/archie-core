import assert from "node:assert/strict";
import test from "node:test";

import {
  DURATION_UNITS,
  formatGoDuration,
  joinDuration,
  parseGoDuration,
  splitDuration,
  type DurationUnit,
} from "../src/components/ui/duration-input/duration.ts";

// The Go duration string is the API boundary: pages store one string exactly
// as the daemon's Go side writes it (time.Duration.String), and the control
// turns it into milliseconds for arithmetic. Both directions are tested, and
// round-tripping through the boundary must be exact.
test("Go duration strings parse to milliseconds at the API boundary", () => {
  const cases: Array<[string, number]> = [
    ["1h0m0s", 3_600_000],
    ["1m30s", 90_000],
    ["500ms", 500],
    ["0", 0],
    ["1.5s", 1_500],
    [".5s", 500],
    ["2h30.25s", 7_230_250],
    [" -90s ", -90_000],
    ["+90s", 90_000],
  ];
  for (const [input, ms] of cases) {
    assert.equal(parseGoDuration(input), ms, JSON.stringify(input));
  }
});

// A loose parser that coerced a bare number (or truncated trailing junk)
// would silently store seconds as milliseconds. Rejection is the contract.
test("input outside Go's duration syntax is rejected, not coerced", () => {
  const cases: string[] = ["1200", "90", "1x", "1s2", "", "1S", "-"];
  for (const input of cases) {
    assert.equal(parseGoDuration(input), null, JSON.stringify(input));
  }
});

// Go's own string form: hours always carry minutes, minutes always carry
// seconds, and the sign goes on the left.
test("formatting matches Go's time.Duration.String above one millisecond", () => {
  const cases: Array<[number, string]> = [
    [0, "0s"],
    [1_000, "1s"],
    [5_500, "5.5s"],
    [1_500, "1.5s"],
    [90_000, "1m30s"],
    [3_600_000, "1h0m0s"],
    [3_660_000, "1h1m0s"],
    [7_200_250, "2h0m0.25s"],
    [-90_000, "-1m30s"],
  ];
  for (const [ms, want] of cases) {
    assert.equal(formatGoDuration(ms), want, `formatGoDuration(${ms})`);
  }
});

const STORED_MS = [0, 500, 1_000, 1_500, 90_000, 150_000, 7_200_250, -90_000];

test("values survive a round trip through the boundary unchanged", () => {
  for (const ms of STORED_MS) {
    assert.equal(
      parseGoDuration(formatGoDuration(ms)),
      ms,
      `parse(format(${ms}))`,
    );
  }
});

// The control splits a stored duration into (value, unit) for display and
// rejoins it on entry; the pair must reproduce the stored milliseconds.
test("a split value rejoins to the milliseconds it came from", () => {
  const cases: number[] = [3_600_000, 7_200_000, 90_000, 150_000, 5_500, 0];
  for (const ms of cases) {
    const split = splitDuration(ms);
    assert.equal(joinDuration(split.value, split.unit), ms, `split(${ms}) rejoins`);
  }
  // Exact display pair for the largest whole unit: hours win over minutes,
  // minutes over seconds, and a value no larger unit divides falls to ms.
  assert.deepEqual(splitDuration(3_600_000), { value: 1, unit: "h" });
  assert.deepEqual(splitDuration(7_200_000), { value: 2, unit: "h" });
  assert.deepEqual(splitDuration(90_000), { value: 90, unit: "s" });
  assert.deepEqual(splitDuration(150_000), { value: 150, unit: "s" });
  assert.deepEqual(splitDuration(5_500), { value: 5_500, unit: "ms" });
  assert.deepEqual(splitDuration(0), { value: 0, unit: "s" });
});

test("unit arithmetic reassembles the stored milliseconds", () => {
  const cases: Array<[number, DurationUnit, number]> = [
    [1.5, "s", 1_500],
    [90, "m", 5_400_000],
    [2, "h", 7_200_000],
    [3, "ms", 3],
  ];
  for (const [value, unit, ms] of cases) {
    assert.equal(joinDuration(value, unit), ms, `${value}${unit}`);
  }
  assert.deepEqual(DURATION_UNITS, ["ms", "s", "m", "h"]);
});