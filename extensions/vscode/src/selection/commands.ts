import * as vscode from "vscode";

import type { WorkspaceRuntimeRegistry } from "../workspace/registry.js";
import { SelectionFlow } from "./flow.js";
import {
  normalizeInstruction,
  selectionCommandSpecs,
  type SelectionCommandSpec,
} from "./policy.js";

export function registerSelectionCommands(
  registry: WorkspaceRuntimeRegistry | undefined,
  output: vscode.LogOutputChannel,
): readonly vscode.Disposable[] {
  return selectionCommandSpecs.map((spec) =>
    vscode.commands.registerCommand(
      spec.id,
      async (argument?: unknown) => {
        try {
          if (registry === undefined) {
            throw new Error("CodeHelper Runtime requires an open workspace folder");
          }
          const target = registry.requireForURI(
            vscode.window.activeTextEditor?.document.uri,
          );
          const flow = new SelectionFlow({
            isTrusted: () => vscode.workspace.isTrusted,
            requestInstruction,
            captureSelection: async () => target.contextBridge.capture(
              new Set(["selection"]),
              "selection_command",
            ),
            submit: async (prompt, editorContext) => {
              const { sessionId } = target.controller.identity();
              return target.controller.submitPrompt(
                sessionId,
                prompt,
                editorContext,
                spec.id === "codehelper.explainSelection"
                  ? "answer"
                  : "workspace_change",
              );
            },
            focusChat: async () => {
              await registry.select(target.rootId);
              await vscode.commands.executeCommand("codehelper.chat.focus");
            },
          });
          const instruction = decodeInstructionArgument(argument);
          return await flow.execute(spec, instruction);
        } catch (error) {
          const message = error instanceof Error ? error.message : String(error);
          output.error(`[selection] ${spec.id}: ${message}`);
          void vscode.window.showErrorMessage(`CodeHelper: ${message}`);
          return undefined;
        }
      },
    ));
}

async function requestInstruction(
  spec: SelectionCommandSpec,
): Promise<string | undefined> {
  return vscode.window.showInputBox({
    title: `CodeHelper: ${spec.title}`,
    prompt: spec.id === "codehelper.editSelection"
      ? "Describe the change to make to the selected code."
      : "Describe the refactoring goal for the selected code.",
    placeHolder: spec.id === "codehelper.editSelection"
      ? "For example: handle an empty input without changing the API"
      : "For example: extract validation into a focused helper",
    ignoreFocusOut: true,
    validateInput: (value) => {
      try {
        normalizeInstruction(value);
        return undefined;
      } catch (error) {
        return error instanceof Error ? error.message : String(error);
      }
    },
  });
}

function decodeInstructionArgument(value: unknown): string | undefined {
  if (value === undefined) {
    return undefined;
  }
  if (typeof value !== "string") {
    throw new Error("selection command argument must be a string");
  }
  return value;
}
