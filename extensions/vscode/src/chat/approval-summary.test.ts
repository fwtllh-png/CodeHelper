import assert from "node:assert/strict";
import test from "node:test";

import type { ApprovalCard } from "./projector.js";
import {
  approvalCardContent,
  approvalDialogContent,
} from "./approval-summary.js";

void test("approval dialog renders shell requests as readable sections", () => {
  const content = approvalDialogContent(approval({
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

  assert.equal(
    content.title,
    "shell_run: Verify all front matter code paths exist",
  );
  assert.match(content.detail, /Command\ncd docs\/book && python3/u);
  assert.match(content.detail, /\nprint\('ok'\)\n/u);
  assert.match(content.detail, /• Write workspace/u);
  assert.match(content.detail, /• Write isolated chat worktree/u);
  assert.doesNotMatch(content.detail, /serial-tools|write:none|\\n/u);
});

void test("approval dialog bounds malformed generic arguments", () => {
  const content = approvalDialogContent(approval({
    arguments: "x".repeat(2000),
    resources: [],
  }));
  assert.equal(content.title, "shell_run needs approval");
  assert.ok(content.detail.length < 1000);
  assert.match(content.detail, /Full request details/u);
});

void test("approval dialog receives structured multi-file apply context", () => {
  const request = approval({
    tool: "file_apply",
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
  const content = approvalDialogContent(request);
  const card = approvalCardContent(request);

  assert.equal(content.title, "file_apply: 14 edits across 6 files");
  assert.match(content.detail, /Request\nApply 14 edits across 6 files/u);
  assert.match(content.detail, /chapter-1\.md \(3 edits\)/u);
  assert.match(content.detail, /Access\n• Write 6 files in workspace/u);
  assert.match(content.detail, /diff preview is open in Changes/u);
  assert.doesNotMatch(content.detail, /changes:|old|new|\/workspace/u);
  assert.equal(card.summary, "Approval: file_apply · 14 edits across 6 files");
  assert.match(card.detail, /Changes: 14 edits across 6 files/u);
  assert.doesNotMatch(card.detail, /changes:|old|new|\/workspace/u);
});

void test("approval card summarizes file content instead of rendering it", () => {
  const body = "# Heading\n\n" + "long content\n".repeat(200);
  const content = approvalCardContent(approval({
    tool: "file_write",
    arguments: JSON.stringify({
      path: "docs/book/en/chapter.md",
      content: body,
    }),
    resources: ["write:docs/book/en/chapter.md"],
  }));

  assert.equal(
    content.summary,
    "Approval: file_write · docs/book/en/chapter.md",
  );
  assert.match(content.detail, /File: docs\/book\/en\/chapter\.md/u);
  assert.match(content.detail, /Content: 202 lines · \d+ characters/u);
  assert.doesNotMatch(content.detail, /Heading|long content/u);
  assert.ok(content.detail.length <= 360);
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
    ...overrides,
  };
}
