import type {
  ContextReceiptCard, ContextSelection, ContextSelectionCard, DiagnosticReceipt,
  EditorContextReceipt, FileChangeCard, MutableTurn, TurnTimelineItem,
} from "./model.js";
import { expandedContextMarker, maxCardText } from "./model.js";
import { projectMarkdown } from "../markdown.js";
export function projectContextSelections(
  selections: readonly ContextSelection[],
): ContextSelectionCard[] {
  return selections.map((selection) => ({
    path: selection.path, kind: selection.kind,
    reasons: [...selection.reasons],
    evidence: (selection.evidence ?? []).map((fact) => {
      const tool = fact.tool === undefined ? "" : `/${fact.tool}`;
      const symbol = fact.symbol === undefined ? "" : ` ${fact.symbol}`;
      return `${fact.kind}${tool}${symbol}`;
    }),
    score: selection.score, critical: selection.critical ?? false,
    included: selection.included, truncated: selection.truncated ?? false,
    ...(selection.truncation_reason === undefined
      ? {}
      : { truncationReason: selection.truncation_reason }),
  }));
}
export function projectContextReceipts(
  values: readonly EditorContextReceipt[],
): ContextReceiptCard[] {
  return values.slice(0, 8).map((value) => ({
    kind: value.kind,
    ...(value.source === undefined ? {} : { source: value.source }),
    path: truncateField(value.path, 4096),
    ...(value.label === undefined ? {} : { label: truncateField(value.label, 512) }),
    digest: truncateField(value.digest, 64),
    ...(value.range === undefined
      ? {}
      : {
          range: `${String(value.range.start.line + 1)}:` +
            `${String(value.range.start.character + 1)}-` +
            `${String(value.range.end.line + 1)}:` +
            String(value.range.end.character + 1),
          navigationRange: {
            start: { ...value.range.start },
            end: { ...value.range.end },
          },
        }),
    ...(value.symbol === undefined
      ? {}
      : {
          symbol: truncateField(`${value.symbol.kind} ${value.symbol.name}`, 640),
          symbolName: truncateField(value.symbol.name, 512),
        }),
    diagnosticCount: value.diagnostic_count ?? 0,
    omittedDiagnostics: value.omitted_diagnostics ?? 0,
    originalBytes: value.original_bytes, retainedBytes: value.retained_bytes,
    truncated: value.truncated ?? false,
  }));
}
export function truncateField(value: string, maximum: number): string {
  return value.length <= maximum ? value : `${value.slice(0, maximum - 1)}…`;
}
export function fileChangeKind(
  value: string,
): FileChangeCard["kind"] | undefined {
  return value === "created" || value === "modified" || value === "deleted"
    ? value : undefined;
}
export function summarizeDiagnostics(
  receipts: Iterable<DiagnosticReceipt>,
): string[] {
  const result: string[] = [];
  const unconfigured = new Map<string, Set<string>>();
  for (const receipt of receipts) {
    const extension = unconfiguredDiagnosticsExtension(receipt);
    if (extension !== undefined) {
      const paths = unconfigured.get(extension) ?? new Set<string>();
      paths.add(receipt.path);
      unconfigured.set(extension, paths);
      continue;
    }
    const detail = receipt.message ??
      `${String(receipt.diagnostics.length)} diagnostics`;
    result.push(truncate(`${receipt.path}: ${receipt.status} (${detail})`));
  }
  for (const [extension, paths] of unconfigured) {
    result.push(
      `${extension} 文件未配置编辑后诊断（${String(paths.size)} 个文件）`,
    );
  }
  return result;
}
function unconfiguredDiagnosticsExtension(
  receipt: DiagnosticReceipt,
): string | undefined {
  if (receipt.status !== "unavailable" || receipt.message === undefined) return undefined;
  const matched = /^no post-edit diagnostics command is configured for (\.[a-z0-9]+)$/iu
    .exec(receipt.message);
  return matched?.[1]?.toLowerCase();
}
export function displayPrompt(prompt: string): string {
  const marker = prompt.indexOf(expandedContextMarker);
  return marker < 0 ? truncate(prompt) : truncate(prompt.slice(0, marker));
}
export function appendBounded(current: string, addition: string): string {
  return truncate(`${current}${addition}`);
}
export function appendTimelineText(
  turn: MutableTurn,
  kind: "output" | "reasoning",
  sequence: number,
  text: string,
): void {
  const last = turn.timeline.at(-1);
  if (last?.kind === kind) {
    last.text = kind === "reasoning" ? appendReasoning(last.text, text)
      : appendBounded(last.text, text);
    return;
  }
  turn.timeline.push({
    id: `${kind}:${String(sequence)}`,
    sequence,
    kind,
    text: truncate(text),
  });
}
export function reconcileCompletedOutput(
  turn: MutableTurn,
  sequence: number,
  completedText: string,
): void {
  const completed = truncate(completedText);
  if (completed === turn.output) {
    if (!turn.timeline.some((item) => item.kind === "output")) {
      appendTimelineText(turn, "output", sequence, completed);
    }
    return;
  }
  let lastOutputIndex = -1;
  for (let index = turn.timeline.length - 1; index >= 0; index -= 1) {
    if (turn.timeline[index]?.kind === "output") {
      lastOutputIndex = index;
      break;
    }
  }
  const last = turn.timeline[lastOutputIndex];
  if (last?.kind === "output" && last.text.endsWith(completed)) {
    const progress = last.text.slice(0, -completed.length);
    if (progress.length > 0) {
      last.text = progress;
      turn.timeline.splice(lastOutputIndex + 1, 0, {
        id: `output:${String(sequence)}`,
        sequence,
        kind: "output",
        text: completed,
      });
    }
  } else if (last?.kind !== "output" || last.text !== completed) {
    turn.timeline.push({
      id: `output:${String(sequence)}`,
      sequence,
      kind: "output",
      text: completed,
    });
  }
  if (turn.output.length === 0) {
    turn.output = completed;
  } else if (!turn.output.endsWith(completed)) {
    const separator = turn.output.endsWith("\n") ? "" : "\n\n";
    turn.output = appendBounded(turn.output, `${separator}${completed}`);
  }
}
export function ensureToolTimelineItem(
  turn: MutableTurn,
  sequence: number,
  callId: string,
): void {
  if (turn.timeline.some(
    (item) => item.kind === "tool" && item.callId === callId,
  )) return;
  turn.timeline.push({
    id: `tool:${callId}`,
    sequence,
    kind: "tool",
    callId,
  });
}
export function projectTimeline(turn: MutableTurn): TurnTimelineItem[] {
  let lastActivity = -1;
  let lastOutput = -1;
  for (let index = 0; index < turn.timeline.length; index += 1) {
    if (turn.timeline[index]?.kind === "output") {
      lastOutput = index;
    } else {
      lastActivity = index;
    }
  }
  return turn.timeline.map((item, index): TurnTimelineItem => {
    switch (item.kind) {
      case "output":
        return {
          ...item,
          markdown: projectMarkdown(item.text),
          final: turn.status === "completed" &&
            index === lastOutput &&
            index > lastActivity,
        };
      case "reasoning":
        return {
          ...item,
          markdown: projectMarkdown(item.text),
          active: turn.reasoningActive && index === turn.timeline.length - 1,
        };
      case "diagnostics":
        return { ...item, messages: [...item.messages] };
      default:
        return { ...item };
    }
  });
}
export function appendReasoning(current: string, addition: string): string {
  if (addition === current) return current;
  if (current.length > 0 && addition.startsWith(current)) {
    return truncate(addition);
  }
  return appendBounded(current, addition);
}
export function stringify(value: unknown): string {
  try {
    return truncate(JSON.stringify(value, null, 2));
  } catch {
    return "[unserializable]";
  }
}
export function truncate(value: string): string {
  if (value.length <= maxCardText) return value;
  return `${value.slice(0, maxCardText)}\n...[truncated]`;
}
