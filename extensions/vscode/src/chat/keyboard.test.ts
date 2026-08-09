import assert from "node:assert/strict";
import test from "node:test";

import { routeChatKeyboard } from "./keyboard.js";

const base = {
  key: "",
  ctrlKey: false,
  metaKey: false,
  shiftKey: false,
  isComposing: false,
  sessionsOpen: false,
  turnActive: false,
};

void test("keyboard router protects IME composition and routes new Chat", () => {
  assert.equal(routeChatKeyboard({
    ...base, key: "Enter", metaKey: true, isComposing: true,
  }), "none");
  assert.equal(routeChatKeyboard({
    ...base, key: "n", metaKey: true,
  }), "new-chat");
  assert.equal(routeChatKeyboard({
    ...base, key: "N", ctrlKey: true,
  }), "new-chat");
});

void test("keyboard router sends with Enter and preserves Shift Enter", () => {
  assert.equal(routeChatKeyboard({
    ...base, key: "Enter",
  }), "send");
  assert.equal(routeChatKeyboard({
    ...base, key: "Enter", shiftKey: true,
  }), "none");
  assert.equal(routeChatKeyboard({
    ...base, key: "Enter", turnActive: true,
  }), "none");
});

void test("keyboard router closes the top overlay before stopping a Turn", () => {
  assert.equal(routeChatKeyboard({
    ...base, key: "Escape", sessionsOpen: true, turnActive: true,
  }), "close-sessions");
  assert.equal(routeChatKeyboard({
    ...base, key: "Escape", turnActive: true,
  }), "stop");
});
