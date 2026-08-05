import assert from "node:assert/strict";
import test from "node:test";

import {
  normalizeInstruction,
  selectionCommandSpecs,
  selectionPrompt,
} from "./policy.js";

void test("selection command policy keeps four finite native actions", () => {
  assert.deepEqual(
    selectionCommandSpecs.map((spec) => spec.id),
    [
      "codehelper.explainSelection",
      "codehelper.editSelection",
      "codehelper.refactorSelection",
      "codehelper.generateTestsForSelection",
    ],
  );
  assert.equal(selectionCommandSpecs[0]?.requiresTrust, false);
  assert.equal(
    selectionCommandSpecs.filter((spec) => spec.requiresTrust).length,
    3,
  );
  assert.equal(
    selectionCommandSpecs.filter((spec) => spec.requiresInstruction).length,
    2,
  );
});

void test("selection prompts are bounded and preserve explicit instructions", () => {
  assert.match(
    selectionPrompt("codehelper.explainSelection"),
    /Explain the selected code/u,
  );
  assert.match(
    selectionPrompt("codehelper.generateTestsForSelection"),
    /Generate focused tests/u,
  );
  assert.match(
    selectionPrompt("codehelper.editSelection", "  replace the branch  "),
    /replace the branch$/u,
  );
  assert.match(
    selectionPrompt("codehelper.refactorSelection", "extract a helper"),
    /extract a helper$/u,
  );
  assert.throws(() => normalizeInstruction("  "), /instruction is required/);
  assert.throws(
    () => normalizeInstruction("x".repeat(4097)),
    /exceeds 4096 characters/,
  );
});
