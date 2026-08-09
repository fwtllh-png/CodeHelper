import assert from "node:assert/strict";
import test from "node:test";

import {
  chatTitleFromPrompt,
  defaultChatTitle,
  isPlaceholderChatTitle,
} from "./title.js";

void test("placeholder Chat titles include the current and legacy defaults", () => {
  assert.equal(defaultChatTitle, "New Chat");
  assert.equal(isPlaceholderChatTitle("New Chat"), true);
  assert.equal(isPlaceholderChatTitle("新对话"), true);
  assert.equal(isPlaceholderChatTitle("Chat 2"), true);
  assert.equal(isPlaceholderChatTitle("修复登录问题"), false);
});

void test("chatTitleFromPrompt derives a concise Chinese title", () => {
  assert.equal(
    chatTitleFromPrompt("请帮我修复登录页面的空指针问题，并补充回归测试。后续再优化"),
    "修复登录页面的空指针问题，并补充回归测试",
  );
});

void test("chatTitleFromPrompt normalizes Markdown and English prompts", () => {
  assert.equal(
    chatTitleFromPrompt("### Please explain `WorkspaceTurnGate` concurrency"),
    "explain WorkspaceTurnGate concurrency",
  );
});

void test("chatTitleFromPrompt bounds long titles by Unicode characters", () => {
  const title = chatTitleFromPrompt("分析" + "并发恢复".repeat(20));
  assert.ok(title);
  assert.equal(Array.from(title).length, 48);
  assert.match(title, /…$/u);
});

void test("chatTitleFromPrompt ignores empty Markdown", () => {
  assert.equal(chatTitleFromPrompt("  ###  "), undefined);
});
