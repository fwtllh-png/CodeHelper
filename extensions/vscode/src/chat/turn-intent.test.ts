import assert from "node:assert/strict";
import test from "node:test";

import { turnIntentForMode } from "./turn-intent.js";

void test("Act mode keeps ordinary Chat intent read-compatible", () => {
  assert.equal(turnIntentForMode("act"), "answer");
});

void test("specialized modes retain their explicit intent", () => {
  assert.equal(turnIntentForMode("plan"), "plan");
  assert.equal(turnIntentForMode("operate"), "operation");
  assert.equal(turnIntentForMode(undefined), "answer");
});
