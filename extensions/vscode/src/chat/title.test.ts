import assert from "node:assert/strict";
import test from "node:test";

import { chatTitleFromPrompt } from "./title.js";

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
