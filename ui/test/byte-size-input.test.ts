import assert from "node:assert/strict";
import test from "node:test";

import {
  BYTES_PER_MB,
  bytesToMB,
  mbToBytes,
} from "../src/components/ui/byte-size-input/bytes.ts";

// The megabyte here is decimal (10^6), matching what the API boundary stores
// as whole bytes. A binary interpretation (2^20) would inflate every stored
// value by ~4.9%: ten entered megabytes would become 10_485_760 bytes.
test("entered megabytes store as whole decimal bytes", () => {
  assert.equal(BYTES_PER_MB, 1_000_000);
  assert.equal(mbToBytes(10), 10_000_000);
  assert.equal(mbToBytes(0.1), 100_000);
  assert.equal(mbToBytes(1.5), 1_500_000);
});

test("stored bytes read back as megabytes", () => {
  const cases: Array<[number, number]> = [
    [0, 0],
    [1_500_000, 1.5],
    [2_500_000, 2.5],
    [1_234_567, 1.234567],
    [10_000_000, 10],
  ];
  for (const [bytes, mb] of cases) {
    assert.equal(bytesToMB(bytes), mb, `bytesToMB(${bytes})`);
  }
});

// The control's display value is what it writes back on change, so storage
// must be a fixed point of the round trip -- no byte may drift.
test("the byte count survives a display round trip unchanged", () => {
  const stored: number[] = [
    0, 1, 1_500, 1_500_000, 2_500_000, 9_876_543, 10_000_000, 1_048_576,
  ];
  for (const bytes of stored) {
    assert.equal(mbToBytes(bytesToMB(bytes)), bytes, `round trip of ${bytes}`);
  }
});