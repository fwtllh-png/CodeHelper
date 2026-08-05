import * as vscode from "vscode";

import type { NativeDiagnostic } from "../context/native.js";
import type { WorkspaceRuntimeRegistry } from "../workspace/registry.js";
import { canonicalEditorURI } from "../workspace/uri.js";
import { DiagnosticFlow } from "./flow.js";
import {
  decodeDiagnosticSnapshot,
  type DiagnosticAction,
  type DiagnosticSnapshot,
} from "./policy.js";

const maxCodeActionDiagnostics = 32;
const fixCommand = "codehelper.fixDiagnostic";
const explainCommand = "codehelper.explainDiagnostic";

export function registerDiagnosticActions(
  registry: WorkspaceRuntimeRegistry | undefined,
  output: vscode.LogOutputChannel,
): readonly vscode.Disposable[] {
  const result: vscode.Disposable[] = [
    registerActionCommand(fixCommand, "fix", registry, output),
    registerActionCommand(explainCommand, "explain", registry, output),
  ];
  if (registry !== undefined) {
    result.push(vscode.languages.registerCodeActionsProvider(
      [{ scheme: "file" }, { scheme: "vscode-remote" }],
      new DiagnosticCodeActionProvider(
        () => registry.roots.map((root) => root.folder),
      ),
      { providedCodeActionKinds: [vscode.CodeActionKind.QuickFix] },
    ));
  }
  return result;
}

export class DiagnosticCodeActionProvider implements vscode.CodeActionProvider {
  readonly #workspaces: () => readonly vscode.WorkspaceFolder[];
  readonly #isTrusted: () => boolean;

  public constructor(
    workspaces:
      | vscode.WorkspaceFolder
      | (() => readonly vscode.WorkspaceFolder[]),
    isTrusted: () => boolean = () => vscode.workspace.isTrusted,
  ) {
    this.#workspaces = typeof workspaces === "function"
      ? workspaces
      : () => [workspaces];
    this.#isTrusted = isTrusted;
  }

  public provideCodeActions(
    document: vscode.TextDocument,
    _range: vscode.Range | vscode.Selection,
    context: vscode.CodeActionContext,
    token: vscode.CancellationToken,
  ): vscode.CodeAction[] {
    if (!isWorkspaceDocumentScheme(document.uri.scheme) ||
      !this.#workspaces().some((workspace) =>
        vscode.workspace.getWorkspaceFolder(document.uri)?.uri.toString() ===
          workspace.uri.toString()) ||
      (context.only !== undefined &&
        !context.only.contains(vscode.CodeActionKind.QuickFix))) {
      return [];
    }
    const actions: vscode.CodeAction[] = [];
    for (const diagnostic of context.diagnostics.slice(0, maxCodeActionDiagnostics)) {
      if (token.isCancellationRequested) {
        break;
      }
      const snapshot = diagnosticSnapshot(document, diagnostic);
      if (this.#isTrusted()) {
        actions.push(codeAction(
          "Fix with CodeHelper",
          fixCommand,
          diagnostic,
          snapshot,
        ));
      }
      actions.push(codeAction(
        "Explain with CodeHelper",
        explainCommand,
        diagnostic,
        snapshot,
      ));
    }
    return actions;
  }
}

function isWorkspaceDocumentScheme(value: string): boolean {
  return value === "file" || value === "vscode-remote";
}

function registerActionCommand(
  command: string,
  action: DiagnosticAction,
  registry: WorkspaceRuntimeRegistry | undefined,
  output: vscode.LogOutputChannel,
): vscode.Disposable {
  return vscode.commands.registerCommand(command, async (value: unknown) => {
    try {
      if (registry === undefined) {
        throw new Error("CodeHelper Runtime requires an open workspace folder");
      }
      const snapshot = decodeDiagnosticSnapshot(value);
      const document = vscode.workspace.textDocuments.find(
        (candidate) =>
          canonicalEditorURI(candidate.uri, vscode.env.remoteName) ===
            snapshot.uri,
      );
      if (document === undefined) {
        throw new Error(
          "diagnostic action is stale because its document is no longer open",
        );
      }
      const target = registry.requireForURI(document.uri);
      const flow = new DiagnosticFlow({
        isTrusted: () => vscode.workspace.isTrusted,
        captureDiagnostic: async (current) =>
          target.contextBridge.captureDiagnostic(current),
        focusChat: async () => {
          await registry.select(target.rootId);
          await vscode.commands.executeCommand("codehelper.chat.focus");
        },
        submit: async (prompt, editorContext) => {
          const { sessionId } = target.controller.identity();
          return target.controller.submitPrompt(
            sessionId, prompt, editorContext,
          );
        },
      });
      return await flow.execute(action, snapshot);
    } catch (error) {
      const message = error instanceof Error ? error.message : String(error);
      output.error(`[diagnostic] ${command}: ${message}`);
      void vscode.window.showErrorMessage(`CodeHelper: ${message}`);
      return undefined;
    }
  });
}

function codeAction(
  title: string,
  command: string,
  diagnostic: vscode.Diagnostic,
  snapshot: DiagnosticSnapshot,
): vscode.CodeAction {
  const action = new vscode.CodeAction(title, vscode.CodeActionKind.QuickFix);
  action.command = { command, title, arguments: [snapshot] };
  action.diagnostics = [diagnostic];
  return action;
}

function diagnosticSnapshot(
  document: vscode.TextDocument,
  diagnostic: vscode.Diagnostic,
): DiagnosticSnapshot {
  return {
    uri: canonicalEditorURI(document.uri, vscode.env.remoteName),
    documentVersion: document.version,
    diagnostic: nativeDiagnostic(diagnostic),
  };
}

function nativeDiagnostic(value: vscode.Diagnostic): NativeDiagnostic {
  const code = value.code === undefined
    ? undefined
    : typeof value.code === "object"
      ? String(value.code.value)
      : String(value.code);
  return {
    range: {
      start: {
        line: value.range.start.line,
        character: value.range.start.character,
      },
      end: {
        line: value.range.end.line,
        character: value.range.end.character,
      },
    },
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
