import { createHash } from "node:crypto";

import type { ChatSessionSummary } from "../runtime/controller.js";
import type { ChatSnapshot } from "./projector.js";

export interface StructuredSessionReceipt {
  readonly version: 1;
  readonly exportedAt: string;
  readonly session: ChatSessionSummary;
  readonly snapshot: ChatSnapshot;
  readonly integrity: {
    readonly algorithm: "sha256";
    readonly digest: string;
  };
}

export function createStructuredSessionReceipt(
  session: ChatSessionSummary,
  snapshot: ChatSnapshot,
  exportedAt = new Date().toISOString(),
): StructuredSessionReceipt {
  const payload = {
    version: 1 as const,
    exportedAt,
    session: { ...session },
    snapshot,
  };
  return {
    ...payload,
    integrity: {
      algorithm: "sha256",
      digest: createHash("sha256")
        .update(JSON.stringify(payload))
        .digest("hex"),
    },
  };
}

export function validateStructuredSessionReceipt(
  receipt: StructuredSessionReceipt,
): void {
  const { integrity, ...payload } = receipt;
  const expected = createHash("sha256")
    .update(JSON.stringify(payload))
    .digest("hex");
  if (integrity.digest !== expected) {
    throw new Error("Structured Session Receipt integrity check failed");
  }
}

export function renderSessionMarkdown(
  session: ChatSessionSummary,
  snapshot: ChatSnapshot,
): string {
  const lines = [
    `# ${session.title}`,
    "",
    `- Session: \`${session.sessionId}\``,
    `- Workspace: \`${session.workspaceLabel}\``,
    `- Provider / Model: \`${session.provider ?? "unknown"}/${session.model ?? "unknown"}\``,
    `- Mode: \`${session.mode ?? "unknown"}\``,
    `- Execution: \`${session.executionEnvironment}\``,
    `- Updated: ${session.updatedAt}`,
    "",
  ];
  for (const turn of snapshot.turns) {
    lines.push(
      `## User (${turn.id})`,
      "",
      turn.user,
      "",
      `## Agent (${turn.status})`,
      "",
      turn.output || turn.error || "_No output_",
      "",
    );
    if (turn.receipt !== undefined) {
      lines.push("### Receipt", "", "```json", turn.receipt, "```", "");
    }
  }
  return `${lines.join("\n")}\n`;
}
