import assert from "node:assert/strict";
import test from "node:test";

import { decodeWebviewMessage } from "./messages.js";

void test("decodeWebviewMessage accepts the finite command surface", () => {
  assert.deepEqual(decodeWebviewMessage({ type: "ready" }), {
    type: "ready",
  });
  assert.deepEqual(decodeWebviewMessage({ type: "submit", text: "hello" }), {
    type: "submit",
    text: "hello",
  });
  assert.deepEqual(decodeWebviewMessage({
    type: "select-root",
    rootId: "a".repeat(64),
  }), {
    type: "select-root",
    rootId: "a".repeat(64),
  });
  assert.deepEqual(decodeWebviewMessage({
    type: "select-chat",
    sessionId: "session_2",
  }), {
    type: "select-chat",
    sessionId: "session_2",
  });
  assert.deepEqual(decodeWebviewMessage({ type: "new-chat" }), {
    type: "new-chat",
  });
  assert.deepEqual(decodeWebviewMessage({ type: "repair-runtime" }), {
    type: "repair-runtime",
  });
  assert.deepEqual(decodeWebviewMessage({ type: "run-setup" }), {
    type: "run-setup",
  });
  assert.deepEqual(decodeWebviewMessage({
    type: "merge-chat",
    planId: "b".repeat(64),
  }), {
    type: "merge-chat",
    planId: "b".repeat(64),
  });
  assert.deepEqual(decodeWebviewMessage({
    type: "approval",
    requestId: "approval_1",
    decision: "approve",
    scope: "once",
    planId: "a".repeat(64),
  }), {
    type: "approval",
    requestId: "approval_1",
    decision: "approve",
    scope: "once",
    planId: "a".repeat(64),
  });
  assert.deepEqual(decodeWebviewMessage({ type: "stop" }), {
    type: "stop",
  });
  assert.deepEqual(decodeWebviewMessage({
    type: "preview",
    requestId: "approval_1",
  }), {
    type: "preview",
    requestId: "approval_1",
  });
  assert.deepEqual(decodeWebviewMessage({
    type: "input",
    requestId: "input_1",
    answer: "",
  }), {
    type: "input",
    requestId: "input_1",
    answer: "",
  });
});

void test("decodeWebviewMessage rejects forged fields and commands", () => {
  assert.throws(
    () => decodeWebviewMessage({ type: "submit", text: "hello", path: "/tmp/escape" }),
    /unexpected fields/,
  );
  assert.throws(
    () => decodeWebviewMessage({ type: "execute", command: "rm -rf" }),
    /unknown Webview message/,
  );
  assert.throws(
    () => decodeWebviewMessage({
      type: "approval",
      requestId: "approval_1",
      decision: "bypass",
      scope: "always",
    }),
    /decision is invalid/,
  );
  assert.throws(
    () => decodeWebviewMessage({
      type: "select-root",
      rootId: "../../outside",
    }),
    /workspace root id is invalid/,
  );
  assert.throws(
    () => decodeWebviewMessage({
      type: "merge-chat",
      planId: "not-a-plan",
    }),
    /edit plan id is invalid/,
  );
});

void test("decodeWebviewMessage bounds attacker-controlled strings", () => {
  assert.throws(
    () => decodeWebviewMessage({ type: "submit", text: "x".repeat((64 << 10) + 1) }),
    /text is invalid/,
  );
});
