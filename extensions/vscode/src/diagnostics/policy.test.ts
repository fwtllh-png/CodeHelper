import assert from "node:assert/strict";
import test from "node:test";

import {
  decodeDiagnosticSnapshot,
  diagnosticPrompt,
  sameDiagnostic,
} from "./policy.js";

const snapshot = {
  uri: "file:///workspace/value.ts",
  documentVersion: 7,
  diagnostic: {
    range: {
      start: { line: 2, character: 4 },
      end: { line: 2, character: 9 },
    },
    severity: "error",
    code: "E100",
    message: "invalid value",
    source: "fixture",
  },
} as const;

void test("decodeDiagnosticSnapshot accepts the finite diagnostic shape", () => {
  assert.deepEqual(decodeDiagnosticSnapshot(snapshot), snapshot);
  assert.match(diagnosticPrompt("fix"), /^Fix the attached/u);
  assert.match(diagnosticPrompt("explain"), /^Explain the attached/u);
});

void test("decodeDiagnosticSnapshot rejects forged and oversized values", () => {
  assert.throws(
    () => decodeDiagnosticSnapshot({ ...snapshot, path: "../../secret" }),
    /unknown fields/,
  );
  const inherited = { ...snapshot };
  Object.setPrototypeOf(inherited, {});
  assert.throws(() => decodeDiagnosticSnapshot(inherited), /plain object/);
  assert.throws(
    () => decodeDiagnosticSnapshot({ ...snapshot, documentVersion: -1 }),
    /version is invalid/,
  );
  assert.throws(
    () => decodeDiagnosticSnapshot({
      ...snapshot,
      diagnostic: { ...snapshot.diagnostic, severity: "fatal" },
    }),
    /severity is invalid/,
  );
  assert.throws(
    () => decodeDiagnosticSnapshot({
      ...snapshot,
      diagnostic: { ...snapshot.diagnostic, message: "x".repeat(8193) },
    }),
    /message exceeds 8192 UTF-8 bytes/,
  );
});

void test("sameDiagnostic binds range and every diagnostic identity field", () => {
  const decoded = decodeDiagnosticSnapshot(snapshot).diagnostic;
  assert.equal(sameDiagnostic(decoded, decoded), true);
  assert.equal(sameDiagnostic(decoded, { ...decoded, message: "changed" }), false);
  assert.equal(sameDiagnostic(decoded, {
    ...decoded,
    range: {
      start: { line: 3, character: 4 },
      end: { line: 3, character: 9 },
    },
  }), false);
});
