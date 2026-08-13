import assert from "node:assert/strict";
import test from "node:test";

import type { ApprovalCard } from "./projector.js";
import { approvalCardContent } from "./approval-summary.js";

void test("approval card renders authoritative risk, effect, and command", () => {
  const content = approvalCardContent(approval({
    arguments: JSON.stringify({
      command: "cd docs/book && python3 - <<'PY'\nprint('ok')\nPY",
      description: "Verify all front matter code paths exist",
    }),
    resources: [
      "write:serial-tools",
      "write:workspace",
      "write:/workspace/.codehelper/chats/worktrees/chat-123",
      "write:none",
    ],
  }));

  assert.equal(content.risk, "High risk");
  assert.equal(content.title, "Run a command with side effects?");
  assert.equal(content.consequence, "This command can modify local state.");
  assert.match(content.target ?? "", /cd docs\/book[\s\S]+print\('ok'\)/u);
  assert.match(content.detail, /Effect: process\.mutating/u);
  assert.match(content.detail, /Reason code: approval_required/u);
  assert.match(content.detail, /Access: Write workspace/u);
  assert.doesNotMatch(content.detail, /serial-tools|write:none/u);
});

void test("approval card bounds malformed generic arguments", () => {
  const content = approvalCardContent(approval({
    arguments: "x".repeat(2000),
    resources: [],
  }));
  assert.equal(content.target, undefined);
  assert.ok(content.detail.length <= 360);
});

void test("approval card receives structured multi-file apply context", () => {
  const request = approval({
    tool: "file_apply",
    effect: "workspace.edit",
    arguments: JSON.stringify({
      changes: Array.from({ length: 14 }, (_, index) => ({
        op: "edit",
        path: `docs/book/chapter-${String(index % 6 + 1)}.md`,
        old: "old",
        new: "new",
      })),
    }),
    resources: Array.from(
      { length: 7 },
      (_, index) => `write:/workspace/docs/book/chapter-${String(index + 1)}.md`,
    ),
    editPlan: {
      id: "plan-1",
      diff: "--- a/docs/book/chapter-1.md\n+++ b/docs/book/chapter-1.md\n",
      files: Array.from({ length: 6 }, (_, index) => ({
        path: `docs/book/chapter-${String(index + 1)}.md`,
        kind: "modified" as const,
        before: "old",
        after: "new",
        beforeExists: true,
        afterExists: true,
        beforeDigest: "before",
        afterDigest: "after",
      })),
    },
  });
  const card = approvalCardContent(request);

  assert.equal(card.title, "Apply workspace changes?");
  assert.equal(card.target, "14 edits across 6 files");
  assert.match(card.detail, /Changes: 14 edits across 6 files/u);
  assert.doesNotMatch(card.detail, /changes:|old|new|\/workspace/u);
});

void test("approval card summarizes file content instead of rendering it", () => {
  const body = "# Heading\n\n" + "long content\n".repeat(200);
  const content = approvalCardContent(approval({
    tool: "file_write",
    effect: "workspace.edit",
    arguments: JSON.stringify({
      path: "docs/book/en/chapter.md",
      content: body,
    }),
    resources: ["write:docs/book/en/chapter.md"],
  }));

  assert.equal(content.title, "Apply workspace changes?");
  assert.equal(content.target, "docs/book/en/chapter.md");
  assert.match(content.detail, /File: docs\/book\/en\/chapter\.md/u);
  assert.match(content.detail, /Content: 202 lines · \d+ characters/u);
  assert.doesNotMatch(content.detail, /Heading|long content/u);
  assert.ok(content.detail.length <= 360);
});

void test("approval presentation identifies the requesting child agent", () => {
  const request = approval({
    tool: "shell_run",
    arguments: JSON.stringify({ command: "go test ./..." }),
    source: {
      kind: "agent",
      agentId: "agent-2",
      agentPath: "/root/verify_runtime",
      parentPath: "/root",
      role: "verifier",
    },
  });
  const card = approvalCardContent(request);
  assert.match(card.source ?? "", /Agent \/root\/verify_runtime \(verifier\)/u);
  assert.match(card.detail, /Requested by: Agent \/root\/verify_runtime/u);
  assert.match(card.detail, /Parent: \/root/u);
});

function approval(
  overrides: Partial<ApprovalCard>,
): ApprovalCard {
  return {
    requestId: "approval-1",
    turnId: "turn-1",
    itemId: "item-1",
    tool: "shell_run",
    arguments: "{}",
    resources: [],
    allowedScopes: ["once", "session"],
    expiresAt: new Date(Date.now() + 60_000).toISOString(),
    effect: "process.mutating",
    risk: "high",
    reasonCode: "approval_required",
    ...overrides,
  };
}
