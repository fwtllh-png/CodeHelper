import * as vscode from "vscode";

import { run } from "./suite/index.js";
import type { ExtensionAPI } from "../extension.js";

export async function activate(): Promise<void> {
  const workspace = vscode.workspace.workspaceFolders?.[0];
  if (workspace !== undefined) {
    const reconnect = await vscode.workspace.fs.stat(
      vscode.Uri.joinPath(workspace.uri, ".codehelper-reconnect"),
    ).then(() => true, () => false);
    if (reconnect) {
      process.env["CODEHELPER_ELECTRON_SCENARIO"] =
        vscode.workspace.workspaceFolders?.length === 2
          ? "remote-reconnect-multi"
          : "remote-reconnect";
    }
  }
  let result: {
    readonly ok: true;
    readonly sessions: readonly {
      readonly rootId: string;
      readonly sessionId?: string;
      readonly threadId?: string;
      readonly replayedEvents?: number;
    }[];
  } | {
    readonly ok: false;
    readonly error: string;
  };
  try {
    await run();
    const extension = vscode.extensions.getExtension<ExtensionAPI>(
      "codehelper.codehelper-vscode",
    );
    const api = extension?.exports;
    result = {
      ok: true,
      sessions: (api?.runtimeHosts?.() ?? []).map((host) => ({
        rootId: host.rootId,
        ...(host.sessionId === undefined ? {} : { sessionId: host.sessionId }),
        ...(host.threadId === undefined ? {} : { threadId: host.threadId }),
        ...(host.replayedEvents === undefined
          ? {}
          : { replayedEvents: host.replayedEvents }),
      })),
    };
  } catch (error) {
    result = {
      ok: false,
      error: error instanceof Error
        ? `${error.message}\n${error.stack ?? ""}`
        : String(error),
    };
  }
  if (workspace !== undefined) {
    await vscode.workspace.fs.writeFile(
      vscode.Uri.joinPath(workspace.uri, ".codehelper-remote-result.json"),
      new TextEncoder().encode(JSON.stringify(result)),
    );
  }
  await vscode.commands.executeCommand("workbench.action.closeWindow");
}
