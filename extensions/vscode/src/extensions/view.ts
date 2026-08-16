import * as vscode from "vscode";
import { randomUUID } from "node:crypto";

import { protocolVersion } from "../protocol/generated.js";
import {
  decodeExtensionResult,
  type ExtensionProjection,
} from "./model.js";
import type {
  WorkspaceRuntime,
  WorkspaceRuntimeRegistry,
} from "../workspace/registry.js";

type ExtensionNode =
  | { readonly kind: "root"; readonly root: WorkspaceRuntime }
  | {
      readonly kind: "extension";
      readonly root: WorkspaceRuntime;
      readonly projection: ExtensionProjection;
    };

export class ExtensionView implements vscode.Disposable,
vscode.TreeDataProvider<ExtensionNode> {
  readonly #registry: WorkspaceRuntimeRegistry;
  readonly #changes = new vscode.EventEmitter<ExtensionNode | undefined>();
  readonly #subscriptions: vscode.Disposable[];

  public constructor(registry: WorkspaceRuntimeRegistry) {
    this.#registry = registry;
    const tree = vscode.window.createTreeView("codehelper.extensions", {
      treeDataProvider: this,
      showCollapseAll: true,
    });
    this.#subscriptions = [
      tree,
      this.#changes,
      registry.onDidChangeRoots(() => {
        this.refresh();
      }),
      registry.onStateChange(() => {
        this.refresh();
      }),
      vscode.commands.registerCommand(
        "codehelper.refreshExtensions",
        () => {
          this.refresh();
        },
      ),
      vscode.commands.registerCommand(
        "codehelper.enableExtension",
        async (node: ExtensionNode) => this.#setEnabled(node, true),
      ),
      vscode.commands.registerCommand(
        "codehelper.disableExtension",
        async (node: ExtensionNode) => this.#setEnabled(node, false),
      ),
    ];
  }

  public readonly onDidChangeTreeData = this.#changes.event;

  public refresh(): void {
    this.#changes.fire(undefined);
  }

  public getTreeItem(node: ExtensionNode): vscode.TreeItem {
    if (node.kind === "root") {
      const item = new vscode.TreeItem(
        node.root.label,
        vscode.TreeItemCollapsibleState.Expanded,
      );
      item.iconPath = new vscode.ThemeIcon("root-folder");
      return item;
    }
    const value = node.projection;
    const item = new vscode.TreeItem(
      value.name,
      vscode.TreeItemCollapsibleState.None,
    );
    item.description = [
      value.kind,
      value.version,
      value.health,
      value.trust,
    ].filter((part) => part !== undefined && part !== "").join(" | ");
    item.iconPath = new vscode.ThemeIcon(
      value.enabled ? "extensions" : "circle-slash",
    );
    item.contextValue = value.enabled
      ? "codehelperExtensionEnabled"
      : "codehelperExtensionDisabled";
    item.tooltip = `${value.name}: ${value.health}`;
    return item;
  }

  public async getChildren(node?: ExtensionNode): Promise<ExtensionNode[]> {
    if (node === undefined) {
      return this.#registry.roots.map((root) => ({ kind: "root", root }));
    }
    if (node.kind === "extension") return [];
    const result = decodeExtensionResult(
      await node.root.controller.query("extension/list", { kind: "all" }),
    );
    return result.map((projection) => ({
      kind: "extension", root: node.root, projection,
    }));
  }

  public dispose(): void {
    for (const subscription of this.#subscriptions) subscription.dispose();
  }

  async #setEnabled(node: ExtensionNode, enabled: boolean): Promise<void> {
    if (node.kind !== "extension") {
      throw new Error("extension command target is invalid");
    }
    const operationId = `vscode:${randomUUID()}`;
    await node.root.controller.query("extension/control", {
      version: protocolVersion,
      id: operationId,
      kind: node.projection.kind,
      action: enabled ? "enable" : "disable",
      name: node.projection.name,
      created_at: new Date().toISOString(),
    });
    this.refresh();
  }
}

export function unavailableExtensionView(message: string): vscode.Disposable {
  const provider: vscode.TreeDataProvider<{ readonly message: string }> = {
    getTreeItem: ({ message: label }) => new vscode.TreeItem(label),
    getChildren: () => [{ message }],
  };
  return vscode.window.createTreeView("codehelper.extensions", {
    treeDataProvider: provider,
  });
}
