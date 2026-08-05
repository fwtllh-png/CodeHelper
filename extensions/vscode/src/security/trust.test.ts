import assert from "node:assert/strict";
import test from "node:test";

import { configuredBinaryPath, runtimePosture } from "./trust.js";

void test("untrusted workspaces are forced into read-only posture", () => {
  assert.equal(runtimePosture(false), "never");
  assert.equal(runtimePosture(true), "suggest");
});

void test("untrusted workspaces cannot select the Runtime executable", () => {
  const values = {
    globalValue: "/global/codehelper",
    workspaceValue: "/workspace/hostile",
    workspaceFolderValue: "/folder/hostile",
  };
  assert.equal(configuredBinaryPath(values, false), "/global/codehelper");
  assert.equal(configuredBinaryPath(values, true), "/folder/hostile");
});
