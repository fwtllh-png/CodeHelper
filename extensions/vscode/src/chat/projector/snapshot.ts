import { projectMarkdown } from "../markdown.js";
import { projectTimeline, summarizeDiagnostics } from "./helpers.js";
import type { ChatSnapshot, ChatTurn, MutableTurn } from "./model.js";
export function projectSnapshot(
  turnsByID: ReadonlyMap<string, MutableTurn>,
  activeTurnId?: string,
): ChatSnapshot {
  const turns = [...turnsByID.values()].map((turn): ChatTurn => ({
    id: turn.id,
    user: turn.user,
    status: turn.status,
    output: turn.output,
    outputMarkdown: projectMarkdown(turn.output),
    reasoning: turn.reasoning,
    reasoningMarkdown: projectMarkdown(turn.reasoning),
    reasoningActive: turn.reasoningActive,
    timeline: projectTimeline(turn),
    tools: [...turn.tools.values()].map((tool) => ({
      ...tool,
      changes: tool.changes.map((change) => ({ ...change })),
    })),
    approvals: [...turn.approvals.values()].map((approval) => ({ ...approval })),
    inputs: [...turn.inputs.values()].map((input) => ({ ...input })),
    ...(turn.plan === undefined
      ? {}
      : {
          plan: {
            ...turn.plan,
            bodyMarkdown: projectMarkdown(turn.plan.body),
          },
        }),
    contextReceipts: turn.contextReceipts.map((receipt) => ({ ...receipt })),
    contextSelections: turn.contextSelections.map((selection) => ({
      ...selection,
      reasons: [...selection.reasons],
      evidence: [...selection.evidence],
    })),
    diagnostics: [
      ...turn.diagnosticNotices,
      ...summarizeDiagnostics(turn.diagnostics.values()),
    ],
    ...(turn.verification === undefined ? {} : { verification: turn.verification }),
    ...(turn.verificationBlocked === undefined
      ? {}
      : { verificationBlocked: turn.verificationBlocked }),
    verificationUncoveredPaths: [...turn.verificationUncoveredPaths],
    ...(turn.workspaceChange === undefined
      ? {}
      : { workspaceChange: { ...turn.workspaceChange } }),
    ...(turn.receipt === undefined ? {} : { receipt: turn.receipt }),
    ...(turn.error === undefined ? {} : { error: turn.error }),
    unknownEvents: [...turn.unknownEvents],
  }));
  return {
    turns,
    ...(activeTurnId === undefined ? {} : { activeTurnId }),
  };
}
