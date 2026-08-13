import type { ApprovalCard } from "./projector.js";

const maxCommandPreview = 1200;
const maxGenericPreview = 800;
const maxCardPreview = 360;

export interface ApprovalDialogContent {
  readonly title: string;
  readonly detail: string;
}

export interface ApprovalCardContent {
  readonly summary: string;
  readonly detail: string;
}

export function approvalCardContent(
  approval: ApprovalCard,
): ApprovalCardContent {
  const input = parseArguments(approval.arguments);
  const fileApply = fileApplySummary(approval, input);
  const path = stringField(input, "path");
  const purpose = stringField(input, "description") ??
    stringField(input, "reason") ??
    approval.reason;
  const target = fileApply?.label ?? path ?? purpose;
  const source = approvalSource(approval);
  const summary = `${source === undefined ? "Approval" : source}: ${approval.tool}` +
    (target === undefined ? "" : ` · ${singleLine(target, 100)}`) +
    (approval.resolved === undefined ? "" : ` · ${approval.resolved}`);
  const lines: string[] = [];
  if (source !== undefined) {
    lines.push(`Requested by: ${source}`);
  }
  if (fileApply !== undefined) {
    lines.push(`Changes: ${fileApply.label}`);
    lines.push(...fileApply.files.slice(0, 3).map((file) => `• ${file}`));
    if (fileApply.fileCount > 3) {
      lines.push(`• +${String(fileApply.fileCount - 3)} more files`);
    }
    return {
      summary,
      detail: truncate(lines.join("\n"), maxCardPreview),
    };
  }
  if (path !== undefined) {
    lines.push(`File: ${singleLine(path, 180)}`);
  }
  const command = stringField(input, "command");
  if (command !== undefined) {
    lines.push(`Command: ${singleLine(command, maxCardPreview)}`);
  }
  const content = stringField(input, "content");
  if (content !== undefined) {
    lines.push(`Content: ${textSize(content)}`);
  }
  for (const [key, value] of Object.entries(input ?? {})) {
    if (["path", "command", "content", "description", "reason"].includes(key)) {
      continue;
    }
    const formatted = formatValue(value);
    lines.push(`${key}: ${singleLine(formatted, 160)}`);
    if (lines.join("\n").length >= maxCardPreview) break;
  }
  const resources = summarizeResources(approval.resources);
  if (resources.length > 0) {
    lines.push(`Access: ${resources.join(", ")}`);
  }
  return {
    summary,
    detail: truncate(lines.join("\n"), maxCardPreview),
  };
}

export function approvalDialogContent(
  approval: ApprovalCard,
): ApprovalDialogContent {
  const input = parseArguments(approval.arguments);
  const fileApply = fileApplySummary(approval, input);
  const purpose = stringField(input, "description") ??
    stringField(input, "reason") ??
    approval.reason;
  const command = stringField(input, "command");
  const resources = summarizeResources(approval.resources);
  // #region debug-point A-C-D:approval-dialog-input-shape
  void fetch("http://127.0.0.1:7777/event", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ sessionId: "approval-content-formatting", runId: "post-fix", hypothesisId: "A-C-D", location: "extensions/vscode/src/chat/approval-summary.ts:approvalDialogContent", msg: "[DEBUG] Approval dialog input shape", data: { tool: approval.tool, argumentsChars: approval.arguments.length, parsed: input !== undefined, changes: Array.isArray(input?.["changes"]) ? input["changes"].length : 0, hasEditPlan: approval.editPlan !== undefined, resources: approval.resources.length, presentationBranch: fileApply !== undefined ? "file-apply" : command === undefined ? "generic-request" : "command" }, ts: Date.now() }) }).catch(() => {});
  // #endregion
  const title = fileApply !== undefined
    ? `file_apply: ${fileApply.label}`
    : purpose === undefined
    ? `${approval.tool} needs approval`
    : `${approval.tool}: ${singleLine(purpose, 140)}`;
  const sections: string[] = [];
  const source = approvalSource(approval);
  if (source !== undefined) {
    sections.push(
      `Source\n${source}\nParent: ${approval.source?.parentPath ?? "/root"}`,
    );
  }
  if (fileApply !== undefined) {
    sections.push(`Request\nApply ${fileApply.label}`);
    sections.push(`Files\n${fileApply.files.map((file) => `• ${file}`).join("\n")}`);
    sections.push(`Access\n• Write ${plural(fileApply.fileCount, "file")} in workspace`);
    sections.push("A diff preview is open in Changes.");
  } else if (command !== undefined) {
    sections.push(`Command\n${truncate(command.trim(), maxCommandPreview)}`);
  } else {
    const preview = readableArguments(input, approval.arguments);
    if (preview !== "") {
      sections.push(`Request\n${truncate(preview, maxGenericPreview)}`);
    }
  }
  if (fileApply === undefined && resources.length > 0) {
    sections.push(`Access\n${resources.map((value) => `• ${value}`).join("\n")}`);
  }
  sections.push("Full request details are available in the Chat approval card.");
  return { title, detail: sections.join("\n\n") };
}

function approvalSource(approval: ApprovalCard): string | undefined {
  if (approval.source === undefined) return undefined;
  return `Agent ${approval.source.agentPath} (${approval.source.role})`;
}

interface FileApplySummary {
  readonly label: string;
  readonly fileCount: number;
  readonly files: readonly string[];
}

function fileApplySummary(
  approval: ApprovalCard,
  input: Readonly<Record<string, unknown>> | undefined,
): FileApplySummary | undefined {
  if (approval.tool !== "file_apply") return undefined;
  const changes = Array.isArray(input?.["changes"]) ? input["changes"] : [];
  const counts = new Map<string, number>();
  for (const change of changes) {
    if (typeof change !== "object" || change === null || Array.isArray(change)) {
      continue;
    }
    const path = stringField(change as Readonly<Record<string, unknown>>, "path");
    if (path !== undefined) counts.set(path, (counts.get(path) ?? 0) + 1);
  }
  const paths = approval.editPlan?.files.map((file) => file.path) ??
    [...counts.keys()];
  if (changes.length === 0 && paths.length === 0) return undefined;
  const files = paths.slice(0, 8).map((path) => {
    const count = counts.get(path);
    return count === undefined ? singleLine(path, 180) :
      `${singleLine(path, 160)} (${plural(count, "edit")})`;
  });
  if (paths.length > files.length) {
    files.push(`+${String(paths.length - files.length)} more files`);
  }
  return {
    label: `${plural(changes.length, "edit")} across ${plural(paths.length, "file")}`,
    fileCount: paths.length,
    files,
  };
}

function parseArguments(value: string): Readonly<Record<string, unknown>> | undefined {
  try {
    const parsed: unknown = JSON.parse(value);
    return typeof parsed === "object" && parsed !== null && !Array.isArray(parsed)
      ? parsed as Readonly<Record<string, unknown>>
      : undefined;
  } catch {
    return undefined;
  }
}

function stringField(
  value: Readonly<Record<string, unknown>> | undefined,
  field: string,
): string | undefined {
  const candidate = value?.[field];
  return typeof candidate === "string" && candidate.trim() !== ""
    ? candidate.trim()
    : undefined;
}

function readableArguments(
  parsed: Readonly<Record<string, unknown>> | undefined,
  original: string,
): string {
  if (parsed === undefined) return original.trim();
  return Object.entries(parsed)
    .map(([key, value]) => `${key}: ${formatValue(value)}`)
    .join("\n");
}

function formatValue(value: unknown): string {
  if (typeof value === "string") return value;
  if (typeof value === "number" || typeof value === "boolean" || value === null) {
    return String(value);
  }
  return JSON.stringify(value);
}

function summarizeResources(resources: readonly string[]): string[] {
  const result = new Set<string>();
  for (const resource of resources) {
    const [access, ...rest] = resource.split(":");
    const target = rest.join(":");
    if (target === "" || target === "none" || target === "serial-tools") continue;
    if (target === "workspace") {
      result.add(`${title(access)} workspace`);
      continue;
    }
    if (target.includes("/.codehelper/chats/worktrees/")) {
      result.add(`${title(access)} isolated chat worktree`);
      continue;
    }
    result.add(`${title(access)} ${singleLine(target, 180)}`);
  }
  return [...result];
}

function singleLine(value: string, limit: number): string {
  return truncate(value.replace(/\s+/gu, " ").trim(), limit);
}

function truncate(value: string, limit: number): string {
  if (value.length <= limit) return value;
  return `${value.slice(0, Math.max(0, limit - 1))}…`;
}

function title(value: string | undefined): string {
  if (value === undefined || value === "") return "Access";
  return value.charAt(0).toUpperCase() + value.slice(1);
}

function textSize(value: string): string {
  const lines = value === "" ? 0 : value.split(/\r?\n/u).length;
  return `${String(lines)} lines · ${String(value.length)} characters`;
}

function plural(count: number, noun: string): string {
  return `${String(count)} ${noun}${count === 1 ? "" : "s"}`;
}
