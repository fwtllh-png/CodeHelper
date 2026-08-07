import * as vscode from "vscode";

import type { EditPlanPreview } from "../edits/preview.js";
import type { WorkspaceRuntime } from "../workspace/registry.js";
import {
  validateResourceReference,
  type ResourceRange,
  type ResourceReference,
} from "./resources.js";

export class ResourceNavigator {
  readonly #registry: ResourceWorkspaceRegistry;
  readonly #editPreview: EditPlanPreview;

  public constructor(
    registry: ResourceWorkspaceRegistry,
    editPreview: EditPlanPreview,
  ) {
    this.#registry = registry;
    this.#editPreview = editPreview;
  }

  public async open(reference: ResourceReference): Promise<void> {
    const safe = validateResourceReference(reference);
    const root = this.#registry.find(safe.rootId);
    if (root === undefined) {
      throw new Error("resource workspace root is no longer open");
    }
    const uri = vscode.Uri.joinPath(root.folder.uri, ...safe.path.split("/"));
    if (this.#registry.forURI(uri)?.rootId !== root.rootId) {
      throw new Error("resource resolved outside its workspace root");
    }
    switch (safe.kind) {
      case "directory":
        await vscode.commands.executeCommand("revealInExplorer", uri);
        return;
      case "diff":
        if (safe.plan === undefined || safe.fileIndex === undefined) {
          throw new Error("resource diff is incomplete");
        }
        await this.#editPreview.showFile(
          safe.plan,
          safe.fileIndex,
          false,
          safe.rootId,
        );
        return;
      case "symbol":
        await this.#openSymbol(uri, safe);
        return;
      case "diagnostic":
        await this.#openDiagnostic(uri, safe.range);
        return;
      case "file":
      case "range":
        await this.#openDocument(uri, safe.range);
    }
  }

  async #openSymbol(
    uri: vscode.Uri,
    reference: ResourceReference,
  ): Promise<void> {
    const range = reference.range;
    if (range === undefined) {
      throw new Error("symbol resource has no range");
    }
    const definitions = await vscode.commands.executeCommand<
      vscode.Location[] | vscode.LocationLink[] | undefined
    >(
      "vscode.executeDefinitionProvider",
      uri,
      position(range.start),
    );
    const definition = definitions?.[0];
    if (definition !== undefined) {
      if ("targetUri" in definition) {
        this.#assertSameRoot(reference.rootId, definition.targetUri);
        await this.#openDocument(
          definition.targetUri,
          fromVSCodeRange(
            definition.targetSelectionRange ?? definition.targetRange,
          ),
        );
      } else {
        this.#assertSameRoot(reference.rootId, definition.uri);
        await this.#openDocument(
          definition.uri,
          fromVSCodeRange(definition.range),
        );
      }
      return;
    }
    await this.#openDocument(uri, range);
  }

  #assertSameRoot(rootId: string, uri: vscode.Uri): void {
    if (this.#registry.forURI(uri)?.rootId !== rootId) {
      throw new Error("resource definition resolved outside its workspace root");
    }
  }

  async #openDiagnostic(
    uri: vscode.Uri,
    fallback?: ResourceRange,
  ): Promise<void> {
    const diagnostic = vscode.languages.getDiagnostics(uri)[0];
    await this.#openDocument(
      uri,
      diagnostic === undefined ? fallback : fromVSCodeRange(diagnostic.range),
    );
  }

  async #openDocument(
    uri: vscode.Uri,
    range?: ResourceRange,
  ): Promise<void> {
    const document = await vscode.workspace.openTextDocument(uri);
    const editor = await vscode.window.showTextDocument(document, {
      preview: true,
      preserveFocus: false,
    });
    if (range === undefined) return;
    const selection = new vscode.Selection(
      position(range.start),
      position(range.end),
    );
    editor.selection = selection;
    editor.revealRange(selection, vscode.TextEditorRevealType.InCenterIfOutsideViewport);
  }
}

export interface ResourceWorkspaceRegistry {
  find(rootId: string): WorkspaceRuntime | undefined;
  forURI(uri: vscode.Uri | undefined): WorkspaceRuntime | undefined;
}

function position(value: {
  readonly line: number;
  readonly character: number;
}): vscode.Position {
  return new vscode.Position(value.line, value.character);
}

function fromVSCodeRange(value: vscode.Range): ResourceRange {
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
