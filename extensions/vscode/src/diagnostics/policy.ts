import {
  prepareDiagnostics,
  type NativeDiagnostic,
  type NativeRange,
} from "../context/native.js";

export type DiagnosticAction = "fix" | "explain";

export interface DiagnosticSnapshot {
  readonly uri: string;
  readonly documentVersion: number;
  readonly diagnostic: NativeDiagnostic;
}

const maxURIBytes = 4096;

export function diagnosticPrompt(action: DiagnosticAction): string {
  return action === "fix"
    ? "Fix the attached editor diagnostic using the repository's existing patterns."
    : "Explain the attached editor diagnostic, its likely cause, and practical next steps.";
}

export function decodeDiagnosticSnapshot(value: unknown): DiagnosticSnapshot {
  const snapshot = objectWithKeys(
    value,
    ["uri", "documentVersion", "diagnostic"],
    "diagnostic action",
  );
  const uri = requireString(snapshot["uri"], "diagnostic URI");
  if (Buffer.byteLength(uri, "utf8") > maxURIBytes) {
    throw new Error(`diagnostic URI exceeds ${String(maxURIBytes)} UTF-8 bytes`);
  }
  const documentVersion = snapshot["documentVersion"];
  if (!Number.isSafeInteger(documentVersion) || Number(documentVersion) < 0) {
    throw new Error("diagnostic document version is invalid");
  }
  const diagnostic = decodeDiagnostic(snapshot["diagnostic"]);
  return { uri, documentVersion: Number(documentVersion), diagnostic };
}

export function sameDiagnostic(
  left: NativeDiagnostic,
  right: NativeDiagnostic,
): boolean {
  return sameRange(left.range, right.range) &&
    left.severity === right.severity &&
    left.message === right.message &&
    left.code === right.code &&
    left.source === right.source;
}

function decodeDiagnostic(value: unknown): NativeDiagnostic {
  const diagnostic = objectWithKeys(
    value,
    ["range", "severity", "message", "code", "source"],
    "diagnostic",
  );
  const severity = diagnostic["severity"];
  if (severity !== "error" && severity !== "warning" &&
    severity !== "information" && severity !== "hint") {
    throw new Error("diagnostic severity is invalid");
  }
  const result: NativeDiagnostic = {
    range: decodeRange(diagnostic["range"]),
    severity,
    message: requireString(diagnostic["message"], "diagnostic message"),
    ...(diagnostic["code"] === undefined
      ? {}
      : { code: requireString(diagnostic["code"], "diagnostic code") }),
    ...(diagnostic["source"] === undefined
      ? {}
      : { source: requireString(diagnostic["source"], "diagnostic source") }),
  };
  const prepared = prepareDiagnostics([result]).diagnostics[0];
  if (prepared === undefined) {
    throw new Error("diagnostic is required");
  }
  return prepared;
}

function decodeRange(value: unknown): NativeRange {
  const range = objectWithKeys(value, ["start", "end"], "diagnostic range");
  return {
    start: decodePosition(range["start"], "diagnostic range start"),
    end: decodePosition(range["end"], "diagnostic range end"),
  };
}

function decodePosition(
  value: unknown,
  label: string,
): NativeRange["start"] {
  const position = objectWithKeys(value, ["line", "character"], label);
  const line = position["line"];
  const character = position["character"];
  if (!Number.isSafeInteger(line) || Number(line) < 0 ||
    !Number.isSafeInteger(character) || Number(character) < 0) {
    throw new Error(`${label} is invalid`);
  }
  return { line: Number(line), character: Number(character) };
}

function objectWithKeys(
  value: unknown,
  allowed: readonly string[],
  label: string,
): Readonly<Record<string, unknown>> {
  if (typeof value !== "object" || value === null || Array.isArray(value)) {
    throw new Error(`${label} must be an object`);
  }
  const prototype: unknown = Object.getPrototypeOf(value);
  if (prototype !== Object.prototype && prototype !== null) {
    throw new Error(`${label} must be a plain object`);
  }
  const result = value as Readonly<Record<string, unknown>>;
  if (Object.keys(result).some((key) => !allowed.includes(key))) {
    throw new Error(`${label} contains unknown fields`);
  }
  return result;
}

function requireString(value: unknown, label: string): string {
  if (typeof value !== "string" || value.length === 0) {
    throw new Error(`${label} is required`);
  }
  return value;
}

function sameRange(left: NativeRange, right: NativeRange): boolean {
  return left.start.line === right.start.line &&
    left.start.character === right.start.character &&
    left.end.line === right.end.line &&
    left.end.character === right.end.character;
}
