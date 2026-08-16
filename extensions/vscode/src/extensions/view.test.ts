import assert from "node:assert/strict";
import test from "node:test";

import { decodeExtensionResult } from "./model.js";

void test("extension projection decodes runtime-owned state", () => {
  const result = decodeExtensionResult({
    operation_id: "operation-1",
    revision: 2,
    extensions: [{
      kind: "plugin",
      name: "review",
      version: "1.0.0",
      trust: "signed-registry",
      enabled: true,
      health: "active",
    }],
  });
  assert.deepEqual(result, [{
    kind: "plugin",
    name: "review",
    version: "1.0.0",
    trust: "signed-registry",
    enabled: true,
    health: "active",
  }]);
});

void test("extension projection rejects malformed runtime state", () => {
  assert.throws(
    () => decodeExtensionResult({
      extensions: [{ kind: "plugin", name: "../bad", enabled: "yes" }],
    }),
    /projection is invalid/,
  );
});
