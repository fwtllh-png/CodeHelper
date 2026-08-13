import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { join } from "node:path";
import test from "node:test";

import { ChatProjector } from "../chat/projector.js";
import { decodeEvent } from "../protocol/decode.js";

void test("VS Code projects every shared Host Journey stage", async () => {
  const contract = JSON.parse(await readFile(
    join(process.cwd(), "..", "..", "docs", "host-journey-contract.json"),
    "utf8",
  )) as { readonly journey: readonly { readonly id: string }[] };
  assert.deepEqual(contract.journey.map((step) => step.id), [
    "start", "stream", "approve", "input", "cancel", "verify", "recover", "receipt",
  ]);

  const projector = new ChatProjector();
  const events = [
    event(1, "turn.started", {
      provider: "fixture", model: "fixture", display_prompt: "inspect",
    }),
    event(2, "reasoning.delta", { text: "checking" }),
    event(3, "output.delta", { text: "result" }),
    event(4, "tool.start", {
      tool: "file_read", call_id: "call_1", arguments: { path: "main.ts" },
    }),
    event(5, "tool.output", {
      tool: "file_read", call_id: "call_1", stream: "stdout",
      chunk: "reading", cursor: 1,
    }),
    event(6, "tool.result", {
      tool: "file_read", call_id: "call_1", output: "done", is_error: false,
    }),
    event(7, "approval.required", {
      request_id: "approval_1",
      call_id: "call_2",
      tool: "file_write",
      arguments: { path: "main.ts" },
      arguments_digest: "a".repeat(64),
      resources: [{ kind: "file", path: "main.ts", access: "write" }],
      allowed_scopes: ["once"],
      effect: "workspace.edit",
      risk: "high",
      reason_code: "approval_required",
      expires_at: "2099-01-01T00:00:00Z",
      replacement_allowed: false,
      modifiable_arguments: [],
    }),
    event(8, "approval.resolved", {
      request_id: "approval_1", decision: "approve",
    }),
    event(9, "input.required", {
      request_id: "input_1",
      prompt: "Choose verification",
      options: ["focused", "full"],
      expires_at: "2099-01-01T00:00:00Z",
    }),
    event(10, "input.resolved", {
      request_id: "input_1", answer: "full",
    }),
    event(11, "diagnostics.result", {
      receipts: [{
        path: "main.ts", status: "passed", diagnostics: [],
        message: "no diagnostics",
      }],
    }),
    event(12, "turn.verification", {
      scope: "affected", mode: "hard", status: "passed", action: "complete",
      repair_steps: 0,
      checks: [{ name: "npm test", command: "npm test", status: "passed" }],
    }),
    event(13, "turn.receipt", receipt()),
    event(14, "turn.completed", { text: "result" }),
  ];
  for (const item of events) {
    assert.equal(projector.apply(item), true);
  }
  const terminal = events.at(-1);
  assert.ok(terminal);
  assert.equal(projector.apply(terminal), false, "replay must deduplicate");

  const turn = projector.snapshot().turns[0];
  assert.ok(turn);
  assert.equal(turn.status, "completed");
  assert.equal(turn.output, "result");
  assert.equal(turn.reasoning, "checking");
  assert.equal(turn.tools[0]?.status, "completed");
  assert.equal(turn.approvals[0]?.resolved, "approve");
  assert.equal(turn.inputs[0]?.resolved, "full");
  assert.match(turn.diagnostics[0] ?? "", /main\.ts: passed/u);
  assert.match(turn.verification ?? "", /passed/u);
  assert.match(turn.receipt ?? "", /tokens 12 in/u);
  assert.equal(projector.snapshot().activeTurnId, undefined);
});

void test("VS Code keeps cancel reason and accepts recovered replay", () => {
  const projector = new ChatProjector();
  projector.apply(event(1, "turn.started", {
    provider: "fixture", model: "fixture", display_prompt: "long task",
  }));
  projector.apply(event(2, "turn.canceled", { reason: "user_interrupted" }));
  assert.equal(projector.snapshot().turns[0]?.status, "canceled");
  assert.match(projector.snapshot().turns[0]?.error ?? "", /user_interrupted/u);

  const recovered = new ChatProjector();
  assert.equal(recovered.apply(event(1, "turn.started", {
    provider: "fixture", model: "fixture", display_prompt: "long task",
  })), true);
  assert.equal(recovered.apply(event(2, "turn.canceled", {
    reason: "user_interrupted",
  })), true);
  assert.equal(recovered.snapshot().turns[0]?.status, "canceled");
});

function receipt(): Readonly<Record<string, unknown>> {
  return {
    goal: "inspect",
    changes: [{ path: "main.ts", tool: "file_write", kind: "modified" }],
    verification: { diagnostics: "passed", tests: "passed", verify: "passed" },
    diagnostic_count: 0,
    approvals_requested: 1,
    input_tokens: 12,
    output_tokens: 5,
    cost_microunits: 10,
    cost_known: true,
    latency_ms: 20,
    unresolved_issues: [],
  };
}

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
    created_at: "2026-08-06T00:00:00Z",
    data,
  });
}
