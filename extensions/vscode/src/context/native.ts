export interface NativePosition {
  readonly line: number;
  readonly character: number;
}

export interface NativeRange {
  readonly start: NativePosition;
  readonly end: NativePosition;
}

export interface NativeSymbol {
  readonly name: string;
  readonly kind: string;
  readonly range: NativeRange;
  readonly selectionRange?: NativeRange;
  readonly children?: readonly NativeSymbol[];
}

export interface NativeDiagnostic {
  readonly range: NativeRange;
  readonly severity: "error" | "warning" | "information" | "hint";
  readonly code?: string;
  readonly message: string;
  readonly source?: string;
}

export interface PreparedDiagnostics {
  readonly diagnostics: readonly NativeDiagnostic[];
  readonly omitted: number;
}

const maxDiagnostics = 32;
const maxSymbolNameBytes = 512;
const maxSymbolKindBytes = 128;
const maxDiagnosticMessageBytes = 8192;
const maxDiagnosticMetadataBytes = 256;

export function selectInnermostSymbol(
  symbols: readonly NativeSymbol[],
  target: NativeRange,
): NativeSymbol | undefined {
  if (!validRange(target, false)) {
    return undefined;
  }
  const candidates: NativeSymbol[] = [];
  const visit = (values: readonly NativeSymbol[]): void => {
    for (const symbol of values) {
      if (validSymbol(symbol) && containsRange(symbol.range, target)) {
        candidates.push(symbol);
      }
      visit(symbol.children ?? []);
    }
  };
  visit(symbols);
  candidates.sort((left, right) =>
    comparePosition(right.range.start, left.range.start) ||
    comparePosition(left.range.end, right.range.end));
  return candidates[0];
}

export function prepareDiagnostics(
  values: readonly NativeDiagnostic[],
): PreparedDiagnostics {
  const diagnostics = values.map((value) => {
    if (!validRange(value.range, false)) {
      throw new Error("diagnostic provider returned an invalid range");
    }
    if (utf8Bytes(value.message) === 0) {
      throw new Error("diagnostic message is empty");
    }
    assertUTF8Bound(value.message, maxDiagnosticMessageBytes, "diagnostic message");
    if (value.code !== undefined) {
      assertUTF8Bound(value.code, maxDiagnosticMetadataBytes, "diagnostic code");
    }
    if (value.source !== undefined) {
      assertUTF8Bound(value.source, maxDiagnosticMetadataBytes, "diagnostic source");
    }
    return value;
  }).sort(compareDiagnostic);
  if (diagnostics.length === 0) {
    throw new Error("@diagnostics requires diagnostics for the active file");
  }
  return {
    diagnostics: diagnostics.slice(0, maxDiagnostics),
    omitted: Math.max(0, diagnostics.length - maxDiagnostics),
  };
}

function validSymbol(value: NativeSymbol): boolean {
  if (!validRange(value.range, true) ||
    utf8Bytes(value.name) === 0 || utf8Bytes(value.name) > maxSymbolNameBytes ||
    utf8Bytes(value.kind) === 0 || utf8Bytes(value.kind) > maxSymbolKindBytes) {
    return false;
  }
  return value.selectionRange === undefined ||
    (validRange(value.selectionRange, true) &&
      containsRange(value.range, value.selectionRange));
}

function validRange(value: NativeRange, requireNonEmpty: boolean): boolean {
  if (!validPosition(value.start) || !validPosition(value.end)) {
    return false;
  }
  const order = comparePosition(value.start, value.end);
  return order < 0 || (!requireNonEmpty && order === 0);
}

function validPosition(value: NativePosition): boolean {
  return Number.isInteger(value.line) && value.line >= 0 &&
    Number.isInteger(value.character) && value.character >= 0;
}

function containsRange(outer: NativeRange, inner: NativeRange): boolean {
  return comparePosition(outer.start, inner.start) <= 0 &&
    comparePosition(inner.end, outer.end) <= 0;
}

function compareDiagnostic(left: NativeDiagnostic, right: NativeDiagnostic): number {
  return severityOrder(left.severity) - severityOrder(right.severity) ||
    comparePosition(left.range.start, right.range.start) ||
    comparePosition(left.range.end, right.range.end) ||
    compareString(left.message, right.message) ||
    compareString(left.code ?? "", right.code ?? "") ||
    compareString(left.source ?? "", right.source ?? "");
}

function severityOrder(value: NativeDiagnostic["severity"]): number {
  switch (value) {
    case "error":
      return 0;
    case "warning":
      return 1;
    case "information":
      return 2;
    case "hint":
      return 3;
  }
}

function comparePosition(left: NativePosition, right: NativePosition): number {
  return left.line - right.line || left.character - right.character;
}

function compareString(left: string, right: string): number {
  return left < right ? -1 : left > right ? 1 : 0;
}

function assertUTF8Bound(value: string, maximum: number, label: string): void {
  if (utf8Bytes(value) > maximum) {
    throw new Error(`${label} exceeds ${String(maximum)} UTF-8 bytes`);
  }
}

function utf8Bytes(value: string): number {
  return Buffer.byteLength(value, "utf8");
}
