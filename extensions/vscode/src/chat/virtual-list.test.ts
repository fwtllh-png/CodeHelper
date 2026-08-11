import assert from "node:assert/strict";
import test from "node:test";

import {
  computeVirtualWindow,
  virtualItemOffset,
} from "./virtual-list.js";

void test("virtual list renders only the viewport and overscan", () => {
  const items = Array.from({ length: 1_000 }, () => ({ height: 50 }));
  const window = computeVirtualWindow(items, 20_000, 500, 100);
  assert.equal(window.totalHeight, 50_000);
  assert.equal(window.paddingBefore, 19_900);
  assert.ok(window.end - window.start <= 15);
  assert.equal(
    window.paddingBefore +
      items.slice(window.start, window.end).length * 50 +
      window.paddingAfter,
    window.totalHeight,
  );
});

void test("virtual list supports variable headings and direct keyboard offsets", () => {
  const items = [
    { height: 24 },
    { height: 50 },
    { height: 50 },
    { height: 24 },
    { height: 50 },
  ];
  assert.equal(virtualItemOffset(items, 4), 148);
  const window = computeVirtualWindow(items, 140, 50, 0);
  assert.equal(window.start, 3);
  assert.equal(window.end, 5);
});

void test("virtual list clamps stale scroll after search results shrink", () => {
  const items = [{ height: 24 }, { height: 52 }];
  const window = computeVirtualWindow(items, 20_000, 500, 0);
  assert.equal(window.start, 0);
  assert.equal(window.end, 2);
  assert.equal(window.paddingBefore, 0);
  assert.equal(window.paddingAfter, 0);
});

void test("virtual list rejects forged viewport and item dimensions", () => {
  assert.throws(
    () => computeVirtualWindow([{ height: 0 }], 0, 100),
    /height is invalid/u,
  );
  assert.throws(
    () => computeVirtualWindow([{ height: 20 }], -1, 100),
    /viewport is invalid/u,
  );
  assert.throws(
    () => virtualItemOffset([{ height: 20 }], 1),
    /index is invalid/u,
  );
});
