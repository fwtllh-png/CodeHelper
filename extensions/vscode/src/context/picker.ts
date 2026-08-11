import { randomUUID } from "node:crypto";

import * as vscode from "vscode";

import type { SessionProfileSnapshot } from "../runtime/session.js";
import type { EditorContextReference } from "../runtime/session.js";
import type { ContextBridge } from "./bridge.js";

export interface NativeContextAttachment {
  readonly id: string;
  readonly kind: EditorContextReference["kind"];
  readonly label: string;
  readonly reference: EditorContextReference;
}

interface GitRepository {
  readonly rootUri: vscode.Uri;
  diff(cached?: boolean): Promise<string>;
}

interface GitAPI {
  readonly repositories: readonly GitRepository[];
}

interface GitExtension {
  getAPI(version: 1): GitAPI;
}

type NativeContextKind =
  | "file" | "selection" | "symbol" | "diagnostics"
  | "image" | "terminal" | "git_diff";

interface ContextPickItem extends vscode.QuickPickItem {
  readonly contextKind: NativeContextKind;
  readonly unavailable?: boolean;
}

export async function pickNativeContext(
  workspace: vscode.WorkspaceFolder,
  bridge: ContextBridge,
  profile: SessionProfileSnapshot,
): Promise<NativeContextAttachment | undefined> {
  const editor = vscode.window.activeTextEditor;
  const hasSelection = editor !== undefined && !editor.selection.isEmpty;
  const imageSupported =
    profile.capabilities.model_capabilities.image_input ||
    profile.capabilities.model_capabilities.vision;
  const items: ContextPickItem[] = [
    {
      label: "$(file) File",
      description: "Select a saved workspace file",
      contextKind: "file",
    },
    {
      label: "$(selection) Selection",
      description: hasSelection ? "Attach the active selection" : "No active selection",
      contextKind: "selection",
      unavailable: !hasSelection,
    },
    {
      label: "$(symbol-method) Symbol",
      description: editor === undefined ? "No active editor" : "Resolve the active symbol",
      contextKind: "symbol",
      unavailable: editor === undefined,
    },
    {
      label: "$(warning) Diagnostics",
      description: editor === undefined ? "No active editor" : "Attach visible diagnostics",
      contextKind: "diagnostics",
      unavailable: editor === undefined,
    },
    {
      label: "$(file-media) Image",
      description: imageSupported
        ? "Select PNG, JPEG, GIF, or WebP"
        : "Current model does not accept image input",
      contextKind: "image",
      unavailable: !imageSupported,
    },
    {
      label: "$(terminal) Terminal Output",
      description: "Explicitly attach clipboard text as terminal output",
      contextKind: "terminal",
    },
    {
      label: "$(git-compare) Git Diff",
      description: "Attach the current workspace diff from VS Code Git",
      contextKind: "git_diff",
    },
  ];
  const picked = await vscode.window.showQuickPick<ContextPickItem>(items, {
    title: "CodeHelper: Add Context",
    placeHolder: "Choose context owned by the local Extension Host",
    ignoreFocusOut: true,
  });
  if (picked === undefined) return undefined;
  if (picked.unavailable === true) {
    throw new Error(picked.description);
  }

  let reference: EditorContextReference | undefined;
  switch (picked.contextKind) {
    case "file":
      reference = await pickWorkspaceFile(workspace, bridge, "file");
      break;
    case "image":
      reference = await pickWorkspaceFile(workspace, bridge, "image");
      break;
    case "selection":
    case "symbol":
    case "diagnostics":
      reference = (await bridge.capture(
        new Set([picked.contextKind]),
        "native_picker",
      ))[0];
      break;
    case "terminal":
      reference = await terminalClipboardContext(bridge);
      break;
    case "git_diff":
      reference = await gitDiffContext(workspace, bridge);
      break;
  }
  if (reference === undefined) return undefined;
  return {
    id: randomUUID(),
    kind: reference.kind,
    label: contextLabel(reference),
    reference,
  };
}

async function pickWorkspaceFile(
  workspace: vscode.WorkspaceFolder,
  bridge: ContextBridge,
  kind: "file" | "image",
): Promise<EditorContextReference | undefined> {
  const pattern = kind === "image"
    ? "**/*.{png,jpg,jpeg,gif,webp}"
    : "**/*";
  const uris = await vscode.workspace.findFiles(
    new vscode.RelativePattern(workspace, pattern),
    "**/{.git,node_modules}/**",
    2000,
  );
  const picked = await vscode.window.showQuickPick(
    uris.map((uri: vscode.Uri) => ({
      label: vscode.workspace.asRelativePath(uri, false),
      uri,
    })),
    {
      title: `CodeHelper: Select ${kind === "image" ? "Image" : "File"}`,
      matchOnDescription: true,
      ignoreFocusOut: true,
    },
  );
  if (picked === undefined) return undefined;
  return (await bridge.captureWorkspaceFile(picked.uri, kind))[0];
}

async function terminalClipboardContext(
  bridge: ContextBridge,
): Promise<EditorContextReference | undefined> {
  const confirmation = await vscode.window.showWarningMessage(
    "Attach clipboard text as terminal output?",
    {
      modal: true,
      detail: "Review the clipboard first. Terminal output can contain credentials or private data.",
    },
    "Attach Clipboard",
  );
  if (confirmation !== "Attach Clipboard") return undefined;
  const content = await vscode.env.clipboard.readText();
  return bridge.captureInline("terminal", "Terminal output", content)[0];
}

async function gitDiffContext(
  workspace: vscode.WorkspaceFolder,
  bridge: ContextBridge,
): Promise<EditorContextReference> {
  const extension = (vscode as unknown as {
    readonly extensions: {
      getExtension<T>(id: string): vscode.Extension<T> | undefined;
    };
  }).extensions.getExtension<GitExtension>("vscode.git");
  const api = extension?.isActive === true
    ? extension.exports.getAPI(1)
    : await extension?.activate().then((git: GitExtension) => git.getAPI(1));
  const repository = api?.repositories.find(
    (candidate: GitRepository) =>
      vscode.workspace.getWorkspaceFolder(candidate.rootUri)?.uri.toString() ===
      workspace.uri.toString(),
  );
  if (repository === undefined) {
    throw new Error("VS Code Git has no repository for this workspace root");
  }
  const diff = await repository.diff();
  const reference = bridge.captureInline("git_diff", "Workspace Git diff", diff)[0];
  if (reference === undefined) {
    throw new Error("VS Code Git returned no diff context");
  }
  return reference;
}

function contextLabel(reference: EditorContextReference): string {
  switch (reference.kind) {
    case "file":
    case "selection":
    case "symbol":
    case "diagnostics":
      return `${reference.kind}: ${reference.path}`;
    case "image":
      return `image: ${reference.label ?? reference.path}`;
    case "terminal":
    case "git_diff":
      return reference.label ?? reference.kind;
  }
}
