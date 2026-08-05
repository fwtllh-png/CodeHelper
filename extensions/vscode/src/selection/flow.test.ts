import assert from "node:assert/strict";
import test from "node:test";

import {
  SelectionFlow,
  type SelectionFlowDependencies,
} from "./flow.js";
import { selectionCommandSpecs } from "./policy.js";
import type {
  EditorContextReference,
  SubmitReceipt,
} from "../runtime/session.js";

const context: readonly EditorContextReference[] = [{
  kind: "selection",
  source: "selection_command",
  uri: "file:///workspace/value.ts",
  path: "value.ts",
  document_version: 1,
  digest: "a".repeat(64),
  range: {
    start: { line: 0, character: 0 },
    end: { line: 0, character: 5 },
  },
  explicit: true,
}];
const receipt: SubmitReceipt = {
  operationId: "operation_1",
  turnId: "turn_1",
  itemId: "item_1",
};

void test("SelectionFlow submits every action through one selection turn", async () => {
  const prompts: string[] = [];
  const order: string[] = [];
  let captures = 0;
  let focuses = 0;
  const flow = new SelectionFlow(dependencies({
    captureSelection: () => {
      captures++;
      order.push("capture");
      return Promise.resolve(context);
    },
    submit: (prompt, submittedContext) => {
      order.push("submit");
      prompts.push(prompt);
      assert.equal(submittedContext, context);
      return Promise.resolve(receipt);
    },
    focusChat: () => {
      focuses++;
      order.push("focus");
      return Promise.resolve();
    },
  }));

  for (const spec of selectionCommandSpecs) {
    const result = await flow.execute(
      spec,
      spec.requiresInstruction ? `${spec.title} instruction` : undefined,
    );
    assert.equal(result, receipt);
  }
  assert.equal(captures, 4);
  assert.equal(focuses, 4);
  assert.equal(prompts.length, 4);
  assert.equal(prompts.every((prompt) => prompt.length > 0), true);
  assert.deepEqual(order, [
    "capture", "focus", "submit",
    "capture", "focus", "submit",
    "capture", "focus", "submit",
    "capture", "focus", "submit",
  ]);
});

void test("SelectionFlow rejects mutating actions before capture when untrusted", async () => {
  let captures = 0;
  const flow = new SelectionFlow(dependencies({
    isTrusted: () => false,
    captureSelection: () => {
      captures++;
      return Promise.resolve(context);
    },
  }));
  const explain = selectionCommandSpecs[0];
  const edit = selectionCommandSpecs[1];
  assert.ok(explain);
  assert.ok(edit);
  await flow.execute(explain);
  await assert.rejects(flow.execute(edit, "change it"), /untrusted workspace/);
  assert.equal(captures, 1);
});

void test("SelectionFlow treats a dismissed instruction box as cancellation", async () => {
  let captures = 0;
  const flow = new SelectionFlow(dependencies({
    requestInstruction: () => Promise.resolve(undefined),
    captureSelection: () => {
      captures++;
      return Promise.resolve(context);
    },
  }));
  const edit = selectionCommandSpecs[1];
  assert.ok(edit);
  assert.equal(await flow.execute(edit), undefined);
  assert.equal(captures, 0);
});

function dependencies(
  overrides: Partial<SelectionFlowDependencies>,
): SelectionFlowDependencies {
  return {
    isTrusted: () => true,
    requestInstruction: () => Promise.resolve("fixture instruction"),
    captureSelection: () => Promise.resolve(context),
    submit: () => Promise.resolve(receipt),
    focusChat: () => Promise.resolve(),
    ...overrides,
  };
}
