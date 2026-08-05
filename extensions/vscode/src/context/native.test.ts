import assert from "node:assert/strict";
import test from "node:test";

import {
  prepareDiagnostics,
  selectInnermostSymbol,
  type NativeDiagnostic,
  type NativeSymbol,
} from "./native.js";

void test("selectInnermostSymbol chooses the nested containing symbol", () => {
  const symbols: NativeSymbol[] = [{
    name: "outer",
    kind: "function",
    range: range(0, 0, 8, 1),
    selectionRange: range(0, 9, 0, 14),
    children: [{
      name: "inner",
      kind: "function",
      range: range(2, 2, 4, 3),
      selectionRange: range(2, 11, 2, 16),
    }],
  }];

  assert.equal(
    selectInnermostSymbol(symbols, range(3, 4, 3, 4))?.name,
    "inner",
  );
  assert.equal(
    selectInnermostSymbol(symbols, range(7, 0, 7, 1))?.name,
    "outer",
  );
  assert.equal(selectInnermostSymbol(symbols, range(9, 0, 9, 0)), undefined);
});

void test("selectInnermostSymbol rejects inconsistent provider ranges", () => {
  const symbols: NativeSymbol[] = [{
    name: "broken",
    kind: "function",
    range: range(2, 0, 1, 0),
  }, {
    name: "selection-escape",
    kind: "function",
    range: range(2, 0, 3, 0),
    selectionRange: range(1, 0, 1, 3),
  }];
  assert.equal(selectInnermostSymbol(symbols, range(2, 1, 2, 1)), undefined);
  assert.equal(selectInnermostSymbol([], range(0, 0, 0, 0)), undefined);
});

void test("prepareDiagnostics sorts, bounds, and reports omissions", () => {
  const diagnostics: NativeDiagnostic[] = Array.from(
    { length: 40 },
    (_, index) => ({
      range: range(40 - index, 0, 40 - index, 1),
      severity: index % 2 === 0 ? "warning" : "error",
      message: `diagnostic ${String(index)}`,
      code: String(index),
      source: "fixture",
    }),
  );
  const result = prepareDiagnostics(diagnostics);
  assert.equal(result.diagnostics.length, 32);
  assert.equal(result.omitted, 8);
  const first = result.diagnostics[0];
  assert.ok(first);
  assert.equal(first.severity, "error");
  assert.equal(first.range.start.line, 1);
});

void test("prepareDiagnostics fails closed for empty or oversized metadata", () => {
  assert.throws(() => prepareDiagnostics([]), /requires diagnostics/);
  assert.throws(() => prepareDiagnostics([{
    range: range(0, 0, 0, 1),
    severity: "error",
    message: "",
  }]), /diagnostic message is empty/);
  assert.throws(() => prepareDiagnostics([{
    range: range(0, 0, 0, 1),
    severity: "error",
    message: "x".repeat(8193),
  }]), /diagnostic message exceeds 8192 UTF-8 bytes/);
  assert.throws(() => prepareDiagnostics([{
    range: range(0, 0, 0, 1),
    severity: "error",
    message: "valid",
    source: "界".repeat(86),
  }]), /diagnostic source exceeds 256 UTF-8 bytes/);
});

function range(
  startLine: number,
  startCharacter: number,
  endLine: number,
  endCharacter: number,
) {
  return {
    start: { line: startLine, character: startCharacter },
    end: { line: endLine, character: endCharacter },
  };
}
