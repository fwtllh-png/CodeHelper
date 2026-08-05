import assert from "node:assert/strict";
import test from "node:test";

import { ChatProjector } from "./projector.js";
import { decodeEvent } from "../protocol/decode.js";

void test("ChatProjector separates streamed reasoning from Markdown output", () => {
  const projector = new ChatProjector();
  projector.apply(event(1, "turn.started", {
    provider: "fixture",
    model: "fixture-model",
    prompt: "expanded secret context",
    display_prompt: "review this",
  }));
  projector.apply(event(2, "reasoning.delta", { text: "inspect " }));
  projector.apply(event(3, "reasoning.delta", { text: "the code" }));
  projector.apply(event(4, "reasoning.delta", { text: "inspect the code" }));
  assert.equal(projector.snapshot().turns[0]?.reasoningActive, true);
  projector.apply(event(5, "output.delta", { text: "## Result\n\n" }));
  projector.apply(event(6, "output.delta", { text: "**done**" }));
  projector.apply(event(7, "tool.start", {
    tool: "file_read",
    call_id: "call_1",
    arguments: { path: "value.go" },
  }));
  projector.apply(event(8, "tool.result", {
    tool: "file_read",
    call_id: "call_1",
    output: "package value",
    is_error: false,
  }));
  projector.apply(event(9, "turn.completed", { text: "## Result\n\n**done**" }));

  const snapshot = projector.snapshot();
  assert.equal(snapshot.turns.length, 1);
  const turn = snapshot.turns[0];
  assert.ok(turn);
  assert.equal(turn.user, "review this");
  assert.equal(turn.output, "## Result\n\n**done**");
  assert.equal(turn.reasoning, "inspect the code");
  assert.equal(turn.reasoningActive, false);
  assert.equal(turn.outputMarkdown[0]?.kind, "element");
  assert.equal(turn.reasoningMarkdown[0]?.kind, "element");
  assert.equal(turn.tools[0]?.status, "completed");
  assert.equal(turn.status, "completed");
  assert.equal(snapshot.activeTurnId, undefined);
});

void test("ChatProjector tracks approval identity and resolution", () => {
  const projector = new ChatProjector();
  projector.apply(event(1, "approval.required", {
    request_id: "approval_1",
    call_id: "call_1",
    tool: "file_write",
    arguments: { path: "result.txt" },
    arguments_digest: "a".repeat(64),
    resources: [{ kind: "file", path: "result.txt", access: "write" }],
    allowed_scopes: ["once", "session"],
    expires_at: "2026-08-04T12:00:00Z",
    replacement_allowed: false,
    modifiable_arguments: [],
    edit_plan: {
      id: "b".repeat(64),
      diff: "--- /dev/null\n+++ b/result.txt\n",
      files: [{
        path: "result.txt",
        kind: "created",
        before_digest: "missing",
        after_digest: "c".repeat(64),
        before_exists: false,
        after_exists: true,
        after: "created\n",
      }],
    },
  }));
  const pending = projector.pendingApprovals()[0];
  assert.equal(pending?.turnId, "turn_1");
  assert.equal(pending.editPlan?.files[0]?.after, "created\n");
  projector.apply(event(2, "approval.resolved", {
    request_id: "approval_1",
    decision: "approve",
  }));
  assert.equal(projector.pendingApprovals().length, 0);
  assert.equal(projector.snapshot().turns[0]?.approvals[0]?.resolved, "approve");
});

void test("ChatProjector exposes only Runtime-confirmed context receipts", () => {
  const projector = new ChatProjector();
  projector.apply(event(1, "turn.started", {
    provider: "fixture",
    model: "fixture-model",
    display_prompt: "inspect symbol",
    editor_context: [{
      kind: "symbol",
      source: "composer",
      path: "src/value.ts",
      digest: "a".repeat(64),
      range: {
        start: { line: 2, character: 4 },
        end: { line: 5, character: 1 },
      },
      symbol: { name: "calculate", kind: "function" },
      original_bytes: 120,
      retained_bytes: 80,
      truncated: true,
    }],
  }));
  const started = projector.snapshot().turns[0]?.contextReceipts[0];
  assert.ok(started);
  assert.equal(started.path, "src/value.ts");
  assert.equal(started.range, "3:5-6:2");
  assert.equal(started.symbol, "function calculate");
  assert.equal(started.truncated, true);

  projector.apply(event(2, "turn.receipt", {
    goal: "inspect symbol",
    verification: { status: "not_run", checks: [] },
    diagnostic_count: 0,
    approvals_requested: 0,
    input_tokens: 10,
    output_tokens: 4,
    cost_microunits: 0,
    cost_known: false,
    latency_ms: 20,
    editor_context: [{
      kind: "diagnostics",
      source: "composer",
      path: "src/value.ts",
      digest: "a".repeat(64),
      diagnostic_count: 32,
      omitted_diagnostics: 8,
      original_bytes: 120,
      retained_bytes: 120,
    }],
  }));
  const terminal = projector.snapshot().turns[0]?.contextReceipts[0];
  assert.ok(terminal);
  assert.equal(terminal.kind, "diagnostics");
  assert.equal(terminal.diagnosticCount, 32);
  assert.equal(terminal.omittedDiagnostics, 8);
});

void test("ChatProjector preserves unknown events as read-only cards and deduplicates", () => {
  const projector = new ChatProjector();
  const future = event(1, "future.event", { safe: true });
  assert.equal(projector.apply(future), true);
  assert.equal(projector.apply(future), false);
  const unknown = projector.snapshot().turns[0]?.unknownEvents;
  assert.ok(unknown);
  assert.equal(unknown.length, 1);
  assert.match(unknown[0] ?? "", /future\.event/);
});

function event(sequence: number, kind: string, data: unknown) {
  return decodeEvent({
    version: 1,
    id: `event_${String(sequence)}`,
    sequence,
    operation_id: "operation_1",
    thread_id: "thread_1",
    turn_id: "turn_1",
    item_id: "item_1",
    kind,
    created_at: "2026-08-04T00:00:00Z",
    data,
  });
}
