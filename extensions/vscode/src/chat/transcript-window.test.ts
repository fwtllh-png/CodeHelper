import assert from "node:assert/strict";
import test from "node:test";

import {
  computeTranscriptWindow,
  restoredAnchorScrollTop,
} from "./transcript-window.js";

void test("200 Turn Transcript DOM is bounded by viewport and overscan", () => {
  const first = computeTranscriptWindow(200, 0, 720);
  assert.deepEqual(first, {
    start: 0,
    end: 20,
    paddingBefore: 0,
    paddingAfter: 32_400,
  });
  const middle = computeTranscriptWindow(200, 18_000, 720);
  assert.equal(middle.end - middle.start, 20);
  assert.ok(middle.start > 0);
  assert.ok(middle.end < 200);
  const stale = computeTranscriptWindow(2, 36_000, 720);
  assert.deepEqual(stale, {
    start: 0,
    end: 2,
    paddingBefore: 0,
    paddingAfter: 0,
  });
});

void test("Transcript Window rejects forged dimensions", () => {
  assert.throws(() => computeTranscriptWindow(200, -1, 720));
  assert.throws(() => computeTranscriptWindow(200, 0, 720, 0));
  assert.throws(() => computeTranscriptWindow(200, 0, 720, 180, -1));
});

void test("Transcript anchor restoration preserves its viewport offset", () => {
  const restored = restoredAnchorScrollTop(9_000, 45, 15);
  assert.equal(restored, 9_030);
  assert.equal(45 - (restored - 9_000), 15);
  assert.equal(restoredAnchorScrollTop(5, 0, 20), 0);
  assert.throws(() => restoredAnchorScrollTop(Number.NaN, 0, 0));
});
