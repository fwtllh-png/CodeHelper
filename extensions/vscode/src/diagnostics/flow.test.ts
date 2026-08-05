import assert from "node:assert/strict";
import test from "node:test";

import {
  DiagnosticFlow,
  type DiagnosticFlowDependencies,
} from "./flow.js";
import type { DiagnosticSnapshot } from "./policy.js";
import type {
  EditorContextReference,
  SubmitReceipt,
} from "../runtime/session.js";

const snapshot: DiagnosticSnapshot = {
  uri: "file:///workspace/value.ts",
  documentVersion: 1,
  diagnostic: {
    range: {
      start: { line: 0, character: 0 },
      end: { line: 0, character: 5 },
    },
    severity: "error",
    message: "invalid value",
  },
};
const context: readonly EditorContextReference[] = [{
  kind: "diagnostics",
  source: "code_action",
  uri: snapshot.uri,
  path: "value.ts",
  document_version: 1,
  digest: "a".repeat(64),
  diagnostics: [snapshot.diagnostic],
  explicit: true,
}];
const receipt: SubmitReceipt = {
  operationId: "operation_1",
  turnId: "turn_1",
  itemId: "item_1",
};

void test("DiagnosticFlow submits fix and explain through one context path", async () => {
  const order: string[] = [];
  const prompts: string[] = [];
  const flow = new DiagnosticFlow(dependencies({
    captureDiagnostic: () => {
      order.push("capture");
      return Promise.resolve(context);
    },
    focusChat: () => {
      order.push("focus");
      return Promise.resolve();
    },
    submit: (prompt, submitted) => {
      order.push("submit");
      prompts.push(prompt);
      assert.equal(submitted, context);
      return Promise.resolve(receipt);
    },
  }));
  assert.equal(await flow.execute("explain", snapshot), receipt);
  assert.equal(await flow.execute("fix", snapshot), receipt);
  assert.deepEqual(order, [
    "capture", "focus", "submit",
    "capture", "focus", "submit",
  ]);
  assert.match(prompts[0] ?? "", /^Explain/u);
  assert.match(prompts[1] ?? "", /^Fix/u);
});

void test("DiagnosticFlow blocks untrusted fix before diagnostic capture", async () => {
  let captures = 0;
  const flow = new DiagnosticFlow(dependencies({
    isTrusted: () => false,
    captureDiagnostic: () => {
      captures++;
      return Promise.resolve(context);
    },
  }));
  await flow.execute("explain", snapshot);
  await assert.rejects(flow.execute("fix", snapshot), /untrusted workspace/);
  assert.equal(captures, 1);
});

void test("DiagnosticFlow propagates stale capture without submit or focus", async () => {
  let focuses = 0;
  let submits = 0;
  const flow = new DiagnosticFlow(dependencies({
    captureDiagnostic: () => Promise.reject(new Error("diagnostic action is stale")),
    focusChat: () => {
      focuses++;
      return Promise.resolve();
    },
    submit: () => {
      submits++;
      return Promise.resolve(receipt);
    },
  }));
  await assert.rejects(flow.execute("fix", snapshot), /stale/);
  assert.equal(focuses, 0);
  assert.equal(submits, 0);
});

function dependencies(
  overrides: Partial<DiagnosticFlowDependencies>,
): DiagnosticFlowDependencies {
  return {
    isTrusted: () => true,
    captureDiagnostic: () => Promise.resolve(context),
    focusChat: () => Promise.resolve(),
    submit: () => Promise.resolve(receipt),
    ...overrides,
  };
}
