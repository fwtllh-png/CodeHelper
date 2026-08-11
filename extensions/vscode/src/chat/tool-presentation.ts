import type { ToolCard } from "./projector.js";

export interface ToolPresentation {
  readonly label: string;
  readonly target?: string;
  readonly command?: string;
  readonly detail?: string;
  readonly files?: ToolCard["changes"];
  readonly fileOperation?: boolean;
}

export function toolGroupLabel(tools: readonly ToolCard[]): string {
  const failed = tools.filter((tool) => tool.status === "failed").length;
  const running = tools.filter((tool) => tool.status === "running").length;
  const reads = tools.filter((tool) => toolFamily(tool.tool) === "read").length;
  if (failed > 0) return `${String(failed)} operation${failed === 1 ? "" : "s"} failed`;
  if (reads === tools.length && tools.length > 0) {
    return `${running > 0 ? "Viewing" : "Viewed"} ${String(reads)} ` +
      `resource${reads === 1 ? "" : "s"}`;
  }
  if (running > 0) {
    return `Running ${String(running)} operation${running === 1 ? "" : "s"}`;
  }
  return `Completed ${String(tools.length)} ` +
    `operation${tools.length === 1 ? "" : "s"}`;
}

export function presentTool(tool: ToolCard): ToolPresentation {
  const input = parseArguments(tool.arguments);
  if (isFileEditTool(tool.tool)) {
    if (tool.changes.length > 0) {
      return {
        label: fileEditStatusLabel(tool.status, tool.changes.length),
        files: tool.changes,
        fileOperation: true,
      };
    }
    const paths = requestedFilePaths(input);
    return {
      label: fileEditStatusLabel(tool.status, paths.length),
      ...(paths.length === 0 ? {} : { target: compactFileTargets(paths) }),
      ...(tool.output.length === 0 ? {} : { detail: tool.output }),
      fileOperation: true,
    };
  }
  const command = firstString(input, ["command"]);
  if (command !== undefined && isCommandTool(tool.tool)) {
    const detail = commandDetail(input);
    return {
      label: commandStatusLabel(tool.status),
      target: compactTarget(command),
      command,
      ...(detail === undefined ? {} : { detail }),
    };
  }
  const target = firstString(input, [
    "path", "file", "file_path", "directory", "dir", "query", "pattern",
    "url", "symbol",
  ]);
  const description = firstString(input, ["description", "reason"]);
  return {
    label: description ?? actionLabel(tool.tool),
    ...(target === undefined ? {} : { target: compactTarget(target) }),
  };
}

function fileEditStatusLabel(
  status: ToolCard["status"],
  files: number,
): string {
  const count = files === 0
    ? undefined
    : `${String(files)} file${files === 1 ? "" : "s"}`;
  switch (status) {
    case "running":
      return count === undefined ? "Editing" : `Editing ${count}`;
    case "failed":
      return count === undefined ? "Edit failed" : `Edit failed · ${count}`;
    case "completed":
      return count === undefined ? "Edited" : `Edited ${count}`;
  }
}

function isFileEditTool(tool: string): boolean {
  return /(^|_)(write|edit|patch|apply|replace)(_|$)/u.test(tool.toLowerCase());
}

function commandStatusLabel(status: ToolCard["status"]): string {
  switch (status) {
    case "running":
      return "Running command";
    case "failed":
      return "Command failed";
    case "completed":
      return "Command completed";
  }
}

function isCommandTool(tool: string): boolean {
  return /(^|_)(shell|exec|command|run)(_|$)/u.test(tool.toLowerCase());
}

function commandDetail(
  input: Readonly<Record<string, unknown>> | undefined,
): string | undefined {
  if (input === undefined) return undefined;
  const detail = Object.entries(input)
    .filter(([key]) => !["command", "description", "reason"].includes(key))
    .map(([key, value]) => `${key}: ${formatValue(value)}`)
    .join("\n");
  return detail === "" ? undefined : detail;
}

function formatValue(value: unknown): string {
  if (typeof value === "string") return value;
  if (typeof value === "number" || typeof value === "boolean" || value === null) {
    return String(value);
  }
  return JSON.stringify(value);
}

function actionLabel(tool: string): string {
  const normalized = tool.toLowerCase();
  if (/(^|_)(read|view|open|cat)(_|$)/u.test(normalized)) return "View";
  if (/(^|_)(search|grep|find|glob)(_|$)/u.test(normalized)) return "Search";
  if (/(^|_)(write|edit|patch|replace)(_|$)/u.test(normalized)) return "Edit";
  if (/(^|_)(shell|exec|command|run)(_|$)/u.test(normalized)) return "Run command";
  if (/(^|_)(test|verify|check)(_|$)/u.test(normalized)) return "Verify";
  if (/(^|_)(agent|spawn|delegate)(_|$)/u.test(normalized)) return "Agent";
  if (/(^|_)(fetch|request|http|web)(_|$)/u.test(normalized)) return "Access network";
  return humanize(tool);
}

function toolFamily(tool: string): "read" | "other" {
  return /(^|_)(read|view|open|cat)(_|$)/u.test(tool.toLowerCase())
    ? "read"
    : "other";
}

function parseArguments(
  value: string | undefined,
): Readonly<Record<string, unknown>> | undefined {
  if (value === undefined) return undefined;
  try {
    const parsed: unknown = JSON.parse(value);
    return typeof parsed === "object" && parsed !== null && !Array.isArray(parsed)
      ? parsed as Readonly<Record<string, unknown>>
      : undefined;
  } catch {
    return undefined;
  }
}

function requestedFilePaths(
  input: Readonly<Record<string, unknown>> | undefined,
): readonly string[] {
  const paths: string[] = [];
  const add = (value: unknown): void => {
    if (typeof value === "string" && value.trim() !== "") {
      const path = value.trim();
      if (!paths.includes(path)) paths.push(path);
    }
  };
  add(input?.["path"]);
  const changes = input?.["changes"];
  if (Array.isArray(changes)) {
    for (const change of changes) {
      if (typeof change !== "object" || change === null) continue;
      const record = change as Readonly<Record<string, unknown>>;
      add(record["path"]);
      add(record["to"]);
    }
  }
  return paths;
}

function compactFileTargets(paths: readonly string[]): string {
  const first = compactTarget(paths[0] ?? "");
  return paths.length === 1
    ? first
    : `${first} + ${String(paths.length - 1)} more`;
}

function firstString(
  input: Readonly<Record<string, unknown>> | undefined,
  fields: readonly string[],
): string | undefined {
  for (const field of fields) {
    const value = input?.[field];
    if (typeof value === "string" && value.trim() !== "") return value.trim();
  }
  return undefined;
}

function compactTarget(value: string): string {
  const line = value.replace(/\s+/gu, " ").trim();
  return line.length <= 120 ? line : `${line.slice(0, 119)}…`;
}

function humanize(value: string): string {
  const words = value.replaceAll(/[_-]+/gu, " ").trim();
  return words === "" ? "Tool" : words.charAt(0).toUpperCase() + words.slice(1);
}
