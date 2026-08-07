import { createHash } from "node:crypto";
import { basename, extname, isAbsolute, relative, sep } from "node:path";

import * as vscode from "vscode";

import type { TurnStartPayload } from "../protocol/generated.js";
import type { ContextDirective } from "./directives.js";
import {
  prepareDiagnostics,
  selectInnermostSymbol,
  type NativeDiagnostic,
  type NativeRange,
  type NativeSymbol,
} from "./native.js";
import {
  sameDiagnostic,
  type DiagnosticSnapshot,
} from "../diagnostics/policy.js";
import { canonicalEditorURI } from "../workspace/uri.js";

type EditorContextReference = NonNullable<TurnStartPayload["context"]>[number];
type EditorContextSource = NonNullable<EditorContextReference["source"]>;

interface DocumentIdentity {
  readonly uri: string;
  readonly path: string;
  readonly document_version: number;
  readonly digest: string;
  readonly explicit: true;
}

const maxContextFileBytes = 1 << 20;
const maxContextImageBytes = 5 << 20;
const maxProviderSymbols = 4096;
const maxProviderDiagnostics = 4096;

export class ContextBridge {
  readonly #workspace: vscode.WorkspaceFolder;

  public constructor(workspace: vscode.WorkspaceFolder) {
    this.#workspace = workspace;
  }

  public async capture(
    directives: ReadonlySet<ContextDirective>,
    source: EditorContextSource = "composer",
  ): Promise<readonly EditorContextReference[]> {
    if (directives.size === 0) {
      return [];
    }
    const editor = vscode.window.activeTextEditor;
    if (editor === undefined) {
      throw new Error("editor context requires an active text editor");
    }
    const document = editor.document;
    this.#validateDocument(document);
    if (document.isDirty) {
      throw new Error("save the active file before attaching editor context");
    }
    const documentVersion = document.version;
    const targetRange = toNativeRange(editor.selection);
    const selectionSensitive = directives.has("selection") || directives.has("symbol");
    let symbol: NativeSymbol | undefined;
    if (directives.has("symbol")) {
      const providerSymbols = await vscode.commands.executeCommand<
        readonly (vscode.DocumentSymbol | vscode.SymbolInformation)[] | undefined
      >("vscode.executeDocumentSymbolProvider", document.uri);
      const symbols = flattenSymbols(providerSymbols ?? [], document.uri);
      symbol = selectInnermostSymbol(symbols, targetRange);
      if (symbol === undefined) {
        throw new Error("@symbol requires a document symbol containing the active selection");
      }
    }
    let diagnostics: ReturnType<typeof prepareDiagnostics> | undefined;
    if (directives.has("diagnostics")) {
      const visible = vscode.languages.getDiagnostics(document.uri);
      if (visible.length > maxProviderDiagnostics) {
        throw new Error(
          `diagnostic provider exceeds ${String(maxProviderDiagnostics)} entries`,
        );
      }
      diagnostics = prepareDiagnostics(visible.map(toNativeDiagnostic));
    }
    const identity = await this.#captureIdentity(
      document,
      documentVersion,
      () => {
        assertEditorStable(
          editor,
          documentVersion,
          targetRange,
          selectionSensitive,
        );
      },
    );
    const common = {
      source,
      ...identity,
    } as const;
    const references: EditorContextReference[] = [];
    if (directives.has("file")) {
      references.push({ ...common, kind: "file" });
    }
    if (directives.has("selection")) {
      if (targetRange.start.line === targetRange.end.line &&
        targetRange.start.character === targetRange.end.character) {
        throw new Error("@selection requires a non-empty editor selection");
      }
      references.push({
        ...common,
        kind: "selection",
        range: targetRange,
      });
    }
    if (symbol !== undefined) {
      references.push({
        ...common,
        kind: "symbol",
        range: symbol.range,
        symbol: {
          name: symbol.name,
          kind: symbol.kind,
          ...(symbol.selectionRange === undefined
            ? {}
            : { selection_range: symbol.selectionRange }),
        },
      });
    }
    if (diagnostics !== undefined) {
      references.push({
        ...common,
        kind: "diagnostics",
        diagnostics: diagnostics.diagnostics,
        ...(diagnostics.omitted === 0
          ? {}
          : { omitted_diagnostics: diagnostics.omitted }),
      });
    }
    return references;
  }

  public async captureWorkspaceFile(
    uri: vscode.Uri,
    kind: "file" | "image",
  ): Promise<readonly EditorContextReference[]> {
    this.#validateURI(uri);
    const content = await vscode.workspace.fs.readFile(uri);
    const limit = kind === "image" ? maxContextImageBytes : maxContextFileBytes;
    if (content.byteLength === 0 || content.byteLength > limit) {
      throw new Error(`${kind} context must be between 1 and ${String(limit)} bytes`);
    }
    if (kind === "file") {
      new TextDecoder("utf-8", { fatal: true }).decode(content);
    }
    const open = vscode.workspace.textDocuments.find(
      (document) => canonicalEditorURI(document.uri) === canonicalEditorURI(uri),
    );
    if (open?.isDirty === true) {
      throw new Error("save the selected file before attaching it");
    }
    const common = {
      source: "native_picker" as const,
      uri: canonicalEditorURI(uri),
      path: this.#workspacePath(uri),
      document_version: open?.version ?? 1,
      digest: createHash("sha256").update(content).digest("hex"),
      explicit: true as const,
    };
    if (kind === "file") return [{ ...common, kind }];
    return [{
      ...common,
      kind,
      label: basename(uri.fsPath),
      media_type: imageMediaType(uri),
    }];
  }

  public captureInline(
    kind: "terminal" | "git_diff",
    label: string,
    content: string,
  ): readonly EditorContextReference[] {
    const normalized = content.replaceAll("\r\n", "\n").trim();
    if (normalized.length === 0 || Buffer.byteLength(normalized) > 64 << 10 ||
      normalized.includes("\0")) {
      throw new Error("inline context must contain at most 64 KiB of plain text");
    }
    return [{
      kind,
      source: "native_picker",
      uri: "",
      path: "",
      document_version: 0,
      digest: createHash("sha256").update(normalized).digest("hex"),
      label: label.slice(0, 512),
      media_type: "text/plain",
      content: normalized,
      explicit: true,
    }];
  }

  public async captureDiagnostic(
    snapshot: DiagnosticSnapshot,
  ): Promise<readonly EditorContextReference[]> {
    let uri: vscode.Uri;
    try {
      uri = vscode.Uri.parse(snapshot.uri, true);
    } catch {
      throw new Error("diagnostic action URI is invalid");
    }
    if (canonicalEditorURI(uri) !== snapshot.uri) {
      throw new Error("diagnostic action URI is not canonical");
    }
    const document = vscode.workspace.textDocuments.find(
      (candidate) =>
        candidate.uri.scheme === "file" &&
        canonicalEditorURI(candidate.uri) ===
          snapshot.uri,
    );
    if (document === undefined) {
      throw new Error("diagnostic action is stale because its document is no longer open");
    }
    this.#validateDocument(document);
    const prepared = prepareDiagnostics([snapshot.diagnostic]).diagnostics[0];
    if (prepared === undefined) {
      throw new Error("diagnostic action is invalid");
    }
    const assertStable = (): void => {
      if (!vscode.workspace.textDocuments.includes(document) ||
        document.version !== snapshot.documentVersion ||
        document.isDirty) {
        throw new Error("diagnostic action is stale because its document changed");
      }
      const current = vscode.languages.getDiagnostics(document.uri);
      if (current.length > maxProviderDiagnostics) {
        throw new Error(
          `diagnostic provider exceeds ${String(maxProviderDiagnostics)} entries`,
        );
      }
      if (!current.map(toNativeDiagnostic).some(
        (diagnostic) => sameDiagnostic(diagnostic, prepared),
      )) {
        throw new Error("diagnostic action is stale because the diagnostic changed");
      }
    };
    const identity = await this.#captureIdentity(
      document,
      snapshot.documentVersion,
      assertStable,
    );
    return [{
      ...identity,
      source: "code_action",
      kind: "diagnostics",
      diagnostics: [prepared],
    }];
  }

  #validateDocument(document: vscode.TextDocument): void {
    if (!isWorkspaceDocumentScheme(document.uri.scheme) ||
      document.uri.scheme !== this.#workspace.uri.scheme ||
      vscode.workspace.getWorkspaceFolder(document.uri)?.uri.toString() !==
        this.#workspace.uri.toString()) {
      throw new Error("editor context must be a file in the current workspace");
    }
  }

  #validateURI(uri: vscode.Uri): void {
    if (uri.scheme !== "file" ||
      vscode.workspace.getWorkspaceFolder(uri)?.uri.toString() !==
        this.#workspace.uri.toString()) {
      throw new Error("context must be a file in the current workspace");
    }
    this.#workspacePath(uri);
  }

  #workspacePath(uri: vscode.Uri): string {
    const workspacePath = relative(this.#workspace.uri.fsPath, uri.fsPath);
    if (workspacePath === "" || isAbsolute(workspacePath) ||
      workspacePath === ".." || workspacePath.startsWith(`..${sep}`)) {
      throw new Error("context is outside the current workspace");
    }
    return workspacePath.split(sep).join("/");
  }

  async #captureIdentity(
    document: vscode.TextDocument,
    documentVersion: number,
    assertStable: () => void,
  ): Promise<DocumentIdentity> {
    assertStable();
    const content = await vscode.workspace.fs.readFile(document.uri);
    assertStable();
    if (content.byteLength > maxContextFileBytes) {
      throw new Error(`editor context exceeds ${String(maxContextFileBytes)} bytes`);
    }
    const decoded = new TextDecoder("utf-8", { fatal: true }).decode(content);
    if (decoded !== document.getText()) {
      throw new Error("active document is not canonical UTF-8 workspace content");
    }
    return {
      uri: canonicalEditorURI(document.uri),
      path: this.#workspacePath(document.uri),
      document_version: documentVersion,
      digest: createHash("sha256").update(content).digest("hex"),
      explicit: true,
    };
  }
}

function imageMediaType(uri: vscode.Uri): "image/png" | "image/jpeg" | "image/gif" | "image/webp" {
  switch (extname(uri.fsPath).toLowerCase()) {
    case ".png":
      return "image/png";
    case ".jpg":
    case ".jpeg":
      return "image/jpeg";
    case ".gif":
      return "image/gif";
    case ".webp":
      return "image/webp";
    default:
      throw new Error("image context must be PNG, JPEG, GIF, or WebP");
  }
}

function isWorkspaceDocumentScheme(value: string): boolean {
  return value === "file";
}

function flattenSymbols(
  values: readonly (vscode.DocumentSymbol | vscode.SymbolInformation)[],
  documentURI: vscode.Uri,
): NativeSymbol[] {
  const result: NativeSymbol[] = [];
  const pending = [...values].reverse();
  let visited = 0;
  while (pending.length > 0) {
    visited++;
    if (visited > maxProviderSymbols) {
      throw new Error(`symbol provider exceeds ${String(maxProviderSymbols)} entries`);
    }
    const value = pending.pop();
    if (value === undefined) {
      break;
    }
    if (isDocumentSymbol(value)) {
      result.push({
        name: value.name,
        kind: symbolKind(value.kind),
        range: toNativeRange(value.range),
        selectionRange: toNativeRange(value.selectionRange),
      });
      pending.push(...[...value.children].reverse());
    } else if (value.location.uri.toString() === documentURI.toString()) {
      result.push({
        name: value.name,
        kind: symbolKind(value.kind),
        range: toNativeRange(value.location.range),
      });
    }
  }
  return result;
}

function isDocumentSymbol(
  value: vscode.DocumentSymbol | vscode.SymbolInformation,
): value is vscode.DocumentSymbol {
  return "range" in value && "selectionRange" in value && "children" in value;
}

function symbolKind(value: vscode.SymbolKind): string {
  return vscode.SymbolKind[value].toLowerCase();
}

function toNativeDiagnostic(value: vscode.Diagnostic): NativeDiagnostic {
  const code = value.code === undefined
    ? undefined
    : typeof value.code === "object"
      ? String(value.code.value)
      : String(value.code);
  return {
    range: toNativeRange(value.range),
    severity: diagnosticSeverity(value.severity),
    message: value.message,
    ...(code === undefined ? {} : { code }),
    ...(value.source === undefined ? {} : { source: value.source }),
  };
}

function diagnosticSeverity(
  value: vscode.DiagnosticSeverity,
): NativeDiagnostic["severity"] {
  switch (value) {
    case vscode.DiagnosticSeverity.Error:
      return "error";
    case vscode.DiagnosticSeverity.Warning:
      return "warning";
    case vscode.DiagnosticSeverity.Information:
      return "information";
    case vscode.DiagnosticSeverity.Hint:
      return "hint";
  }
}

function toNativeRange(value: vscode.Range): NativeRange {
  return {
    start: {
      line: value.start.line,
      character: value.start.character,
    },
    end: {
      line: value.end.line,
      character: value.end.character,
    },
  };
}

function assertEditorStable(
  editor: vscode.TextEditor,
  documentVersion: number,
  selection: NativeRange,
  selectionSensitive: boolean,
): void {
  if (vscode.window.activeTextEditor !== editor ||
    editor.document.version !== documentVersion ||
    (selectionSensitive && !sameRange(toNativeRange(editor.selection), selection))) {
    throw new Error("active document or selection changed while capturing editor context");
  }
}

function sameRange(left: NativeRange, right: NativeRange): boolean {
  return left.start.line === right.start.line &&
    left.start.character === right.start.character &&
    left.end.line === right.end.line &&
    left.end.character === right.end.character;
}
