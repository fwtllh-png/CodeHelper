import assert from "node:assert/strict";
import test from "node:test";

import type { ChatTurn } from "./projector.js";
import {
  projectChatResources,
  validateResourceReference,
  validateWorkspacePath,
} from "./resources.js";

const rootId = "a".repeat(64);

void test("Resource projection covers file, range, symbol, diagnostic, directory, and diff", () => {
  const snapshot = projectChatResources({
    turns: [turn({
      output: "`README.md` `calculate`",
      outputMarkdown: [{
        kind: "element",
        tag: "p",
        children: [
          { kind: "element", tag: "code", children: [{ kind: "text", text: "README.md" }] },
          { kind: "text", text: " " },
          { kind: "element", tag: "code", children: [{ kind: "text", text: "calculate" }] },
        ],
      }],
      contextReceipts: [
        receipt("file", "README.md"),
        receipt("selection", "src/value.ts", range()),
        receipt("symbol", "src/calculate.ts", range(), "calculate"),
        receipt("diagnostics", "src/problem.ts"),
      ],
      contextSelections: [
        selection("internal/runtime", "directory"),
      ],
      approvals: [{
        requestId: "approval_1",
        turnId: "turn_1",
        itemId: "item_1",
        tool: "file_write",
        arguments: "{}",
        resources: ["write:src/value.ts"],
        allowedScopes: ["once"],
        expiresAt: "2099-01-01T00:00:00Z",
        editPlan: {
          id: "b".repeat(64),
          diff: "diff",
          files: [{
            path: "src/value.ts",
            kind: "modified",
            before: "before",
            after: "after",
            beforeExists: true,
            afterExists: true,
            beforeDigest: "c".repeat(64),
            afterDigest: "d".repeat(64),
          }],
        },
      }],
    })],
  }, rootId, "session_1");

  assert.deepEqual(
    new Set(snapshot.references.map((reference) => reference.kind)),
    new Set(["file", "range", "symbol", "diagnostic", "directory", "diff"]),
  );
  assert.equal(snapshot.views.length, snapshot.references.length);
  const projected = snapshot.snapshot.turns[0];
  assert.ok(projected);
  assert.match(projected.contextReceipts[0]?.resourceId ?? "", /^[0-9a-f]{64}$/u);
  assert.match(
    projected.approvals[0]?.editPlan?.files[0]?.resourceId ?? "",
    /^[0-9a-f]{64}$/u,
  );
  const paragraph = projected.outputMarkdown[0];
  assert.ok(paragraph);
  assert.equal(paragraph.kind, "element");
  const pathCode = paragraph.children[0];
  const symbolCode = paragraph.children[2];
  assert.ok(pathCode);
  assert.ok(symbolCode);
  assert.equal(pathCode.kind, "element");
  assert.equal(symbolCode.kind, "element");
  assert.match(pathCode.resourceId ?? "", /^[0-9a-f]{64}$/u);
  assert.match(symbolCode.resourceId ?? "", /^[0-9a-f]{64}$/u);
});

void test("Resource projection refuses ambiguous basenames", () => {
  const projection = projectChatResources({
    turns: [turn({
      output: "`value.ts`",
      outputMarkdown: [{
        kind: "element",
        tag: "p",
        children: [{
          kind: "element",
          tag: "code",
          children: [{ kind: "text", text: "value.ts" }],
        }],
      }],
      contextSelections: [
        selection("src/value.ts", "source"),
        selection("test/value.ts", "test"),
      ],
    })],
  }, rootId, "session_1");
  const paragraph = projection.snapshot.turns[0]?.outputMarkdown[0];
  assert.ok(paragraph);
  assert.equal(paragraph.kind, "element");
  const code = paragraph.children[0];
  assert.ok(code);
  assert.equal(code.kind, "element");
  assert.equal(code.resourceId, undefined);
});

void test("Resource validation rejects absolute, traversal, command, and forged diff input", () => {
  for (const path of [
    "/tmp/secret",
    "../secret",
    "src/../secret",
    "C:/secret",
    String.raw`src\secret`,
    "command:workbench.action.terminal.new",
  ]) {
    assert.throws(() => validateWorkspacePath(path), /resource path/u);
  }
  assert.throws(() => validateResourceReference({
    id: "b".repeat(64),
    rootId,
    kind: "file",
    path: "https://example.com/file.ts",
  }), /resource path/u);
  assert.throws(() => validateResourceReference({
    id: "b".repeat(64),
    rootId,
    kind: "diff",
    path: "src/value.ts",
    fileIndex: 0,
  }), /diff identity/u);
});

function turn(overrides: Partial<ChatTurn>): ChatTurn {
  return {
    id: "turn_1",
    user: "inspect",
    status: "completed",
    output: "",
    outputMarkdown: [],
    reasoning: "",
    reasoningMarkdown: [],
    reasoningActive: false,
    tools: [],
    approvals: [],
    inputs: [],
    contextReceipts: [],
    contextSelections: [],
    diagnostics: [],
    unknownEvents: [],
    ...overrides,
  };
}

function receipt(
  kind: "file" | "selection" | "symbol" | "diagnostics",
  path: string,
  navigationRange?: ReturnType<typeof range>,
  symbolName?: string,
) {
  return {
    kind,
    path,
    digest: "e".repeat(64),
    ...(navigationRange === undefined
      ? {}
      : { range: "1:1-1:2", navigationRange }),
    ...(symbolName === undefined
      ? {}
      : { symbol: `function ${symbolName}`, symbolName }),
    diagnosticCount: kind === "diagnostics" ? 1 : 0,
    omittedDiagnostics: 0,
    originalBytes: 10,
    retainedBytes: 10,
    truncated: false,
  };
}

function selection(path: string, kind: string) {
  return {
    path,
    kind,
    reasons: ["search"],
    evidence: ["search"],
    score: 1,
    critical: false,
    included: true,
    truncated: false,
  };
}

function range() {
  return {
    start: { line: 0, character: 0 },
    end: { line: 0, character: 1 },
  };
}
