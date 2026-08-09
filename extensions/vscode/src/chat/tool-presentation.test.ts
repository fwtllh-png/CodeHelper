import assert from "node:assert/strict";
import test from "node:test";

import type { ToolCard } from "./projector.js";
import { presentTool, toolGroupLabel } from "./tool-presentation.js";

void test("tool presentation turns internal reads into user-facing actions", () => {
  const tool = card("file_read", {
    arguments: JSON.stringify({ path: "extensions/vscode/src/chat/view.ts" }),
  });
  assert.deepEqual(presentTool(tool), {
    label: "View",
    target: "extensions/vscode/src/chat/view.ts",
  });
  assert.equal(toolGroupLabel([tool, card("fs_read")]), "Viewed 2 resources");
});

void test("tool presentation prefers a declared command purpose", () => {
  assert.deepEqual(presentTool(card("shell_run", {
    status: "running",
    arguments: JSON.stringify({
      command: "npm test",
      description: "运行 VS Code 测试",
    }),
  })), {
    label: "Running command",
    target: "npm test",
    command: "npm test",
  });
  assert.equal(
    toolGroupLabel([card("shell_run", { status: "running" })]),
    "Running 1 operation",
  );
});

void test("command presentation keeps a compact title and full detail", () => {
  const command = "rg -n \"timeline\" extensions/vscode/src " +
    "&& npm test -- --verbose";
  const presentation = presentTool(card("shell_run", {
    status: "completed",
    arguments: JSON.stringify({
      command,
      cwd: "extensions/vscode",
      timeout_ms: 30_000,
    }),
  }));
  assert.equal(presentation.label, "Command completed");
  assert.equal(presentation.command, command);
  assert.equal(presentation.target, command);
  assert.equal(
    presentation.detail,
    "cwd: extensions/vscode\ntimeout_ms: 30000",
  );
});

void test("long command titles are bounded without truncating full detail", () => {
  const command = `printf '${"x".repeat(180)}'`;
  const presentation = presentTool(card("shell_run", {
    status: "failed",
    arguments: JSON.stringify({ command }),
  }));
  assert.equal(presentation.label, "Command failed");
  assert.equal(presentation.command, command);
  assert.ok((presentation.target?.length ?? 0) <= 120);
  assert.match(presentation.target ?? "", /…$/u);
});

void test("file edit presentation exposes structured file statistics", () => {
  const changes = [{
    path: "extensions/vscode/src/chat/resources.test.ts",
    kind: "modified" as const,
    added: 1,
    removed: 0,
    resourceId: "a".repeat(64),
  }];
  assert.deepEqual(presentTool(card("file_edit", { changes })), {
    label: "Edited 1 file",
    files: changes,
    fileOperation: true,
  });
});

void test("failed file apply hides the raw transaction behind a compact summary", () => {
  const output = "read-before-edit \"docs/book/en/chapter.md\": required";
  assert.deepEqual(presentTool(card("file_apply", {
    status: "failed",
    output,
    arguments: JSON.stringify({
      changes: [
        {
          op: "edit",
          path: "scripts/check-book.sh",
          old: "large old payload",
          new: "large new payload",
        },
        {
          op: "move",
          path: "docs/book/en/chapter.md",
          to: "docs/book/en/renamed.md",
        },
      ],
    }),
  })), {
    label: "Edit failed · 3 files",
    target: "scripts/check-book.sh + 2 more",
    detail: output,
    fileOperation: true,
  });
});

void test("tool presentation exposes failures without raw payload noise", () => {
  assert.equal(
    toolGroupLabel([card("shell_run", { status: "failed" })]),
    "1 operation failed",
  );
  assert.equal(presentTool(card("custom_action")).label, "Custom action");
});

function card(
  tool: string,
  overrides: Partial<ToolCard> = {},
): ToolCard {
  return {
    callId: `${tool}-1`,
    tool,
    status: "completed",
    output: "",
    changes: [],
    ...overrides,
  };
}
