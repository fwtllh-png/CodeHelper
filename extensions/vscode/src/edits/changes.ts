import * as vscode from "vscode";

import type { WorkspaceRuntimeRegistry } from "../workspace/registry.js";
import {
  decodePlanDecisionTarget,
  decodePlanFileTarget,
  EditPlanProjector,
  type EditPlanReview,
  type PlanAnnotation,
} from "./model.js";
import type { EditPlanPreview } from "./preview.js";

const changesViewId = "codehelper.changes";

export class ChangesView implements vscode.Disposable {
  readonly #projectors = new Map<string, Map<string, EditPlanProjector>>();
  readonly #provider: ChangesProvider;
  readonly #subscriptions: vscode.Disposable[];
  readonly #preview: EditPlanPreview;
  readonly #decidePlan: (
    rootId: string,
    requestId: string,
    decision: "approve" | "deny",
  ) => Promise<void>;
  readonly #output: vscode.LogOutputChannel;

  public constructor(
    registry: WorkspaceRuntimeRegistry,
    preview: EditPlanPreview,
    decidePlan: (
      rootId: string,
      requestId: string,
      decision: "approve" | "deny",
    ) => Promise<void>,
    output: vscode.LogOutputChannel,
  ) {
    this.#preview = preview;
    this.#decidePlan = decidePlan;
    this.#output = output;
    for (const root of registry.roots) {
      this.#projectors.set(root.rootId, new Map());
    }
    this.#provider = new ChangesProvider(
      () => registry.roots.map((root) => ({
        rootId: root.rootId,
        label: root.label,
        reviews: this.#reviews(root.rootId),
      })),
      () => vscode.workspace.isTrusted,
    );
    this.#subscriptions = [
      this.#provider,
      vscode.window.createTreeView(changesViewId, {
        treeDataProvider: this.#provider,
        showCollapseAll: true,
      }),
      registry.onEvent(({ root, sessionId, event }) => {
        if (this.#projector(root.rootId, sessionId).apply(event)) {
          this.#provider.refresh();
        }
      }),
      registry.onDidChangeRoots(() => {
        const live = new Set(registry.roots.map((root) => root.rootId));
        for (const root of registry.roots) {
          if (!this.#projectors.has(root.rootId)) {
            this.#projectors.set(root.rootId, new Map());
          }
        }
        for (const rootId of this.#projectors.keys()) {
          if (!live.has(rootId)) this.#projectors.delete(rootId);
        }
        this.#provider.refresh();
      }),
      vscode.commands.registerCommand(
        "codehelper.openPlanDiff",
        async (value: unknown) => {
          return this.#run("open diff", async () => {
            const target = decodePlanFileTarget(value);
            const review = this.#find(
              target.rootId,
              (projector) => projector.find(target.planId),
            );
            if (review === undefined) {
              throw new Error("edit plan is unknown or no longer retained");
            }
            await this.#preview.showFile(
              review.plan,
              target.fileIndex,
              false,
              target.rootId,
            );
          });
        },
      ),
      vscode.commands.registerCommand(
        "codehelper.approvePlan",
        async (value: unknown) => {
          return this.#decision(value, "approve");
        },
      ),
      vscode.commands.registerCommand(
        "codehelper.denyPlan",
        async (value: unknown) => {
          return this.#decision(value, "deny");
        },
      ),
    ];
  }

  public dispose(): void {
    for (const subscription of this.#subscriptions.splice(0)) {
      subscription.dispose();
    }
  }

  async #decision(value: unknown, expected: "approve" | "deny"): Promise<boolean> {
    return this.#run(`${expected} plan`, async () => {
      const target = decodePlanDecisionTarget(value);
      if (target.decision !== expected) {
        throw new Error("plan decision command does not match its target");
      }
      const review = this.#find(
        target.rootId,
        (projector) => projector.findRequest(target.requestId),
      );
      if (review === undefined || review.status !== "pending") {
        throw new Error("edit plan approval is unknown or already resolved");
      }
      if (isExpired(review.expiresAt)) {
        throw new Error("edit plan approval has expired");
      }
      await this.#decidePlan(target.rootId, review.requestId, target.decision);
    });
  }

  async #run(label: string, action: () => Promise<void>): Promise<boolean> {
    try {
      await action();
      return true;
    } catch (error) {
      const message = error instanceof Error ? error.message : String(error);
      this.#output.error(`[changes] ${label}: ${message}`);
      void vscode.window.showErrorMessage(`CodeHelper: ${message}`);
      return false;
    }
  }

  #projector(rootId: string, sessionId: string): EditPlanProjector {
    const projectors = this.#projectors.get(rootId);
    if (projectors === undefined) {
      throw new Error("workspace root edit projection is unavailable");
    }
    let projector = projectors.get(sessionId);
    if (projector === undefined) {
      projector = new EditPlanProjector();
      projectors.set(sessionId, projector);
    }
    return projector;
  }

  #reviews(rootId: string): readonly EditPlanReview[] {
    const projectors = this.#projectors.get(rootId);
    if (projectors === undefined) return [];
    return [...projectors.values()].flatMap((projector) => projector.snapshot());
  }

  #find(
    rootId: string,
    find: (projector: EditPlanProjector) => EditPlanReview | undefined,
  ): EditPlanReview | undefined {
    const projectors = this.#projectors.get(rootId);
    if (projectors === undefined) return undefined;
    for (const projector of projectors.values()) {
      const review = find(projector);
      if (review !== undefined) return review;
    }
    return undefined;
  }
}

export function unavailableChangesView(message: string): vscode.Disposable {
  const provider: vscode.TreeDataProvider<ChangeNode> = {
    getTreeItem: (element) => treeItem(element),
    getChildren: () => [{
      kind: "info",
      label: message,
    }],
  };
  return vscode.window.createTreeView(changesViewId, {
    treeDataProvider: provider,
  });
}

class ChangesProvider implements
vscode.TreeDataProvider<ChangeNode>, vscode.Disposable {
  readonly #snapshot: () => readonly RootPlanReviews[];
  readonly #isTrusted: () => boolean;
  readonly #changes = new vscode.EventEmitter<ChangeNode | undefined | null>();

  public constructor(
    snapshot: () => readonly RootPlanReviews[],
    isTrusted: () => boolean,
  ) {
    this.#snapshot = snapshot;
    this.#isTrusted = isTrusted;
  }

  public readonly onDidChangeTreeData = this.#changes.event;

  public refresh(): void {
    this.#changes.fire(undefined);
  }

  public getTreeItem(element: ChangeNode): vscode.TreeItem {
    return treeItem(element);
  }

  public getChildren(element?: ChangeNode): ChangeNode[] {
    if (element === undefined) {
      const roots = this.#snapshot();
      return roots.every((root) => root.reviews.length === 0)
        ? [{ kind: "info", label: "No edit plans yet." }]
        : roots.map((root) => ({ kind: "root", root }));
    }
    switch (element.kind) {
      case "root":
        return element.root.reviews.map((review) => ({
          kind: "plan",
          rootId: element.root.rootId,
          review,
        }));
      case "plan": {
        const files: ChangeNode[] = element.review.plan.files.map(
          (file, fileIndex) => ({
            kind: "file",
            rootId: element.rootId,
            review: element.review,
            fileIndex,
            annotations: element.review.annotations[file.path] ?? [],
          }),
        );
        if (element.review.status !== "pending" ||
          isExpired(element.review.expiresAt)) {
          return files;
        }
        if (this.#isTrusted() &&
          element.review.allowedScopes.includes("once")) {
          files.push({
            kind: "decision",
            rootId: element.rootId,
            review: element.review,
            decision: "approve",
          });
        }
        files.push({
          kind: "decision",
          rootId: element.rootId,
          review: element.review,
          decision: "deny",
        });
        return files;
      }
      case "file":
        return element.annotations.map((annotation) => ({
          kind: "annotation",
          annotation,
        }));
      case "decision":
      case "annotation":
      case "info":
        return [];
    }
  }

  public dispose(): void {
    this.#changes.dispose();
  }
}

type ChangeNode =
  | { readonly kind: "root"; readonly root: RootPlanReviews }
  | {
      readonly kind: "plan";
      readonly rootId: string;
      readonly review: EditPlanReview;
    }
  | {
      readonly kind: "file";
      readonly rootId: string;
      readonly review: EditPlanReview;
      readonly fileIndex: number;
      readonly annotations: readonly PlanAnnotation[];
    }
  | {
      readonly kind: "decision";
      readonly rootId: string;
      readonly review: EditPlanReview;
      readonly decision: "approve" | "deny";
    }
  | { readonly kind: "annotation"; readonly annotation: PlanAnnotation }
  | { readonly kind: "info"; readonly label: string };

function treeItem(node: ChangeNode): vscode.TreeItem {
  switch (node.kind) {
    case "root": {
      const pending = node.root.reviews.filter(
        (review) => review.status === "pending",
      ).length;
      const item = new vscode.TreeItem(
        node.root.label,
        vscode.TreeItemCollapsibleState.Expanded,
      );
      item.description = pending === 0
        ? `${String(node.root.reviews.length)} plans`
        : `${String(pending)} pending`;
      item.tooltip = `Workspace root: ${node.root.rootId}`;
      item.iconPath = new vscode.ThemeIcon("root-folder");
      return item;
    }
    case "plan": {
      const item = new vscode.TreeItem(
        `Plan · ${node.review.tool}`,
        vscode.TreeItemCollapsibleState.Expanded,
      );
      item.description =
        `${node.review.status} · ${String(node.review.plan.files.length)} files`;
      item.tooltip =
        `Plan: ${node.review.plan.id}\nRequest: ${node.review.requestId}\n` +
        `Turn: ${node.review.turnId}\nExpires: ${node.review.expiresAt}`;
      item.iconPath = new vscode.ThemeIcon(
        node.review.status === "pending" ? "diff" : "check",
      );
      item.contextValue = `codehelper.plan.${node.review.status}`;
      return item;
    }
    case "file": {
      const file = node.review.plan.files[node.fileIndex];
      if (file === undefined) {
        throw new Error("edit plan file is unknown");
      }
      const item = new vscode.TreeItem(
        file.path,
        node.annotations.length === 0
          ? vscode.TreeItemCollapsibleState.None
          : vscode.TreeItemCollapsibleState.Collapsed,
      );
      const evidence = node.annotations.map((annotation) =>
        `${annotation.kind}:${annotation.status}`).join(", ");
      item.description = evidence === "" ? file.kind : `${file.kind} · ${evidence}`;
      item.tooltip =
        `${file.path}\nKind: ${file.kind}\nApproval: ${node.review.status}\n` +
        `Before: ${file.beforeDigest}\nAfter: ${file.afterDigest}` +
        (evidence === "" ? "" : `\nEvidence: ${evidence}`);
      item.iconPath = new vscode.ThemeIcon(fileIcon(file.kind));
      item.command = {
        command: "codehelper.openPlanDiff",
        title: "Open CodeHelper Plan Diff",
        arguments: [{
          rootId: node.rootId,
          planId: node.review.plan.id,
          fileIndex: node.fileIndex,
        }],
      };
      item.contextValue = `codehelper.planFile.${file.kind}`;
      return item;
    }
    case "decision": {
      const approve = node.decision === "approve";
      const item = new vscode.TreeItem(approve ? "Approve once" : "Deny");
      item.description = "Runtime decision";
      item.tooltip = approve
        ? "Approve this exact Runtime edit plan once."
        : "Deny this Runtime edit plan.";
      item.iconPath = new vscode.ThemeIcon(approve ? "pass" : "circle-slash");
      item.command = {
        command: approve ? "codehelper.approvePlan" : "codehelper.denyPlan",
        title: approve ? "Approve CodeHelper Plan" : "Deny CodeHelper Plan",
        arguments: [{
          rootId: node.rootId,
          requestId: node.review.requestId,
          decision: node.decision,
        }],
      };
      return item;
    }
    case "annotation": {
      const item = new vscode.TreeItem(
        `${node.annotation.kind}: ${node.annotation.status}`,
      );
      item.description = node.annotation.detail;
      item.tooltip = node.annotation.detail;
      item.iconPath = new vscode.ThemeIcon(
        node.annotation.status === "failed" ? "error" : "info",
      );
      return item;
    }
    case "info":
      return new vscode.TreeItem(node.label);
  }
}

interface RootPlanReviews {
  readonly rootId: string;
  readonly label: string;
  readonly reviews: readonly EditPlanReview[];
}

function fileIcon(kind: EditPlanReview["plan"]["files"][number]["kind"]): string {
  switch (kind) {
    case "created":
      return "diff-added";
    case "modified":
      return "diff-modified";
    case "deleted":
      return "diff-removed";
  }
}

function isExpired(value: string): boolean {
  const timestamp = Date.parse(value);
  return !Number.isFinite(timestamp) || timestamp <= Date.now();
}
