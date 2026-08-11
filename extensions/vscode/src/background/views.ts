import * as vscode from "vscode";

import {
  BackgroundProjector,
  type BackgroundSnapshot,
  type BackgroundView,
  type TerminalNotice,
} from "./model.js";
import { BackgroundQuery } from "./query.js";
import type { DecodedEvent } from "../protocol/decode.js";
import type {
  WorkspaceRuntime,
  WorkspaceRuntimeRegistry,
} from "../workspace/registry.js";

type VisibleBackgroundView = "agents" | "approvals" | "usage";

const viewIds: Readonly<Record<VisibleBackgroundView, string>> = {
  agents: "codehelper.agents",
  approvals: "codehelper.approvals",
  usage: "codehelper.usage",
};

interface RootBackground {
  readonly root: WorkspaceRuntime;
  readonly projector: BackgroundProjector;
  readonly query: BackgroundQuery;
  readonly pending: Set<BackgroundView>;
}

interface RootSnapshot {
  readonly rootId: string;
  readonly label: string;
  readonly snapshot: BackgroundSnapshot;
}

export class BackgroundViews implements vscode.Disposable {
  readonly #registry: WorkspaceRuntimeRegistry;
  readonly #output: vscode.OutputChannel;
  readonly #roots = new Map<string, RootBackground>();
  readonly #providers = new Map<BackgroundView, SnapshotProvider>();
  readonly #visible = new Set<BackgroundView>();
  readonly #subscriptions: vscode.Disposable[] = [];
  #flushTimer: ReturnType<typeof setTimeout> | undefined;
  #disposed = false;

  public constructor(
    registry: WorkspaceRuntimeRegistry,
    output: vscode.OutputChannel,
  ) {
    this.#registry = registry;
    this.#output = output;
    this.#syncRoots();
    for (const kind of Object.keys(viewIds) as VisibleBackgroundView[]) {
      const provider = new SnapshotProvider(kind, () => this.#snapshots());
      this.#providers.set(kind, provider);
      const tree = vscode.window.createTreeView(viewIds[kind], {
        treeDataProvider: provider,
        showCollapseAll: true,
      });
      this.#subscriptions.push(
        provider,
        tree,
        tree.onDidChangeVisibility((event) => {
          if (event.visible) {
            this.#visible.add(kind);
            this.#scheduleAll(kind);
          } else {
            this.#visible.delete(kind);
          }
        }),
      );
      if (tree.visible) this.#visible.add(kind);
    }
    this.#subscriptions.push(
      vscode.commands.registerCommand(
        "codehelper.openChatSession",
        async (value: unknown) => {
          if (!isChatTarget(value)) {
            throw new Error("Chat thread target is invalid");
          }
          const root = this.#registry.find(value.rootId);
          if (root === undefined) throw new Error("workspace root is unavailable");
          await this.#registry.select(root.rootId);
          await root.controller.selectChat(value.sessionId);
          await vscode.commands.executeCommand("codehelper.chat.focus");
        },
      ),
      registry.onEvent(({ root, event, replayed }) => {
        this.#onEvent(root, event, replayed);
      }),
      registry.onStateChange(({ root, snapshot }) => {
        if (snapshot.state === "ready") {
          for (const kind of this.#visible) this.#schedule(root.rootId, kind);
        }
      }),
      registry.onDidChangeRoots(() => {
        this.#syncRoots();
        for (const provider of this.#providers.values()) provider.refresh();
      }),
    );
  }

  public dispose(): void {
    this.#disposed = true;
    if (this.#flushTimer !== undefined) clearTimeout(this.#flushTimer);
    for (const subscription of this.#subscriptions) subscription.dispose();
    this.#subscriptions.length = 0;
  }

  #syncRoots(): void {
    const live = new Set(this.#registry.roots.map((root) => root.rootId));
    for (const root of this.#registry.roots) {
      if (!this.#roots.has(root.rootId)) {
        this.#roots.set(root.rootId, {
          root,
          projector: new BackgroundProjector(),
          query: new BackgroundQuery({
            request: (method, params) => root.controller.query(method, params),
          }),
          pending: new Set(),
        });
      }
    }
    for (const rootId of this.#roots.keys()) {
      if (!live.has(rootId)) this.#roots.delete(rootId);
    }
  }

  #snapshots(): readonly RootSnapshot[] {
    return this.#registry.roots.map((root) => ({
      rootId: root.rootId,
      label: root.label,
      snapshot: this.#root(root.rootId).projector.snapshot(),
    }));
  }

  #onEvent(
    root: WorkspaceRuntime,
    event: DecodedEvent,
    replayed: boolean,
  ): void {
    const background = this.#root(root.rootId);
    this.#notify(root, background.projector.applyEvent(event, replayed));
    this.#providers.get("approvals")?.refresh();
    for (const kind of affectedViews(event.kind)) {
      if (this.#visible.has(kind)) {
        this.#schedule(root.rootId, kind);
      }
    }
  }

  #scheduleAll(kind: BackgroundView): void {
    for (const root of this.#registry.roots) {
      this.#schedule(root.rootId, kind);
    }
  }

  #schedule(rootId: string, kind: BackgroundView): void {
    if (this.#disposed) return;
    this.#root(rootId).pending.add(kind);
    if (this.#flushTimer !== undefined) return;
    this.#flushTimer = setTimeout(() => {
      this.#flushTimer = undefined;
      void this.#flush();
    }, 50);
  }

  async #flush(): Promise<void> {
    const roots = [...this.#roots.values()].filter(
      (root) => root.pending.size > 0,
    );
    await Promise.all(roots.map(async (root) => {
      const pending = [...root.pending];
      root.pending.clear();
      try {
        const reads: Promise<void>[] = [];
        if (pending.includes("threads")) {
          reads.push(root.query.threads().then((rows) => {
            root.projector.replaceThreads(rows);
            this.#providers.get("threads")?.refresh();
          }));
        }
        if (pending.includes("agents")) {
          reads.push(root.query.agents().then((rows) => {
            root.projector.replaceAgents(rows);
            this.#providers.get("agents")?.refresh();
          }));
        }
        if (pending.includes("tasks") || pending.includes("jobs")) {
          reads.push(root.query.tasks().then((rows) => {
            this.#notify(root.root, root.projector.replaceTasks(rows));
            this.#providers.get("tasks")?.refresh();
            this.#providers.get("jobs")?.refresh();
          }));
        }
        if (pending.includes("usage")) {
          reads.push(root.query.usage().then((usage) => {
            root.projector.replaceUsage(usage);
            this.#providers.get("usage")?.refresh();
          }));
        }
        await Promise.all(reads);
      } catch (error) {
        const detail = error instanceof Error ? error.message : String(error);
        this.#output.appendLine(
          `[background:${root.root.label}] refresh failed: ${detail}`,
        );
      }
    }));
  }

  #notify(
    root: WorkspaceRuntime,
    notices: readonly TerminalNotice[],
  ): void {
    for (const notice of notices) {
      const message = `${root.label}: ${notice.title}: ${notice.detail}`;
      if (notice.failed) {
        void vscode.window.showErrorMessage(message);
      } else {
        void vscode.window.showInformationMessage(message);
      }
    }
  }

  #root(rootId: string): RootBackground {
    const root = this.#roots.get(rootId);
    if (root === undefined) {
      throw new Error("workspace root background projection is unavailable");
    }
    return root;
  }
}

export function unavailableBackgroundViews(message: string): vscode.Disposable {
  const subscriptions: vscode.Disposable[] = [];
  for (const kind of Object.keys(viewIds) as VisibleBackgroundView[]) {
    const provider: vscode.TreeDataProvider<TreeNode> = {
      getTreeItem: (element) => treeItem(element),
      getChildren: () => [{ kind: "info", label: message }],
    };
    subscriptions.push(vscode.window.createTreeView(viewIds[kind], {
      treeDataProvider: provider,
    }));
  }
  return vscode.Disposable.from(...subscriptions);
}

class SnapshotProvider implements
vscode.TreeDataProvider<TreeNode>, vscode.Disposable {
  readonly #kind: BackgroundView;
  readonly #snapshot: () => readonly RootSnapshot[];
  readonly #changes = new vscode.EventEmitter<TreeNode | undefined | null>();

  public constructor(
    kind: BackgroundView,
    snapshot: () => readonly RootSnapshot[],
  ) {
    this.#kind = kind;
    this.#snapshot = snapshot;
  }

  public readonly onDidChangeTreeData = this.#changes.event;

  public refresh(): void {
    this.#changes.fire(undefined);
  }

  public getTreeItem(element: TreeNode): vscode.TreeItem {
    return treeItem(element);
  }

  public getChildren(element?: TreeNode): TreeNode[] {
    if (element === undefined) {
      return this.#snapshot().map((root) => ({ kind: "root", root }));
    }
    if (element.kind !== "root") {
      return [];
    }
    return snapshotNodes(
      this.#kind, element.root.rootId, element.root.snapshot,
    );
  }

  public dispose(): void {
    this.#changes.dispose();
  }
}

type TreeNode =
  | { readonly kind: "root"; readonly root: RootSnapshot }
  | {
      readonly kind: "entry";
      readonly label: string;
      readonly description: string;
      readonly tooltip: string;
      readonly icon: string;
      readonly command?: vscode.Command;
    }
  | { readonly kind: "info"; readonly label: string };

function treeItem(node: TreeNode): vscode.TreeItem {
  if (node.kind === "root") {
    const item = new vscode.TreeItem(
      node.root.label,
      vscode.TreeItemCollapsibleState.Expanded,
    );
    item.description = rootDescription(node.root.snapshot);
    item.tooltip = `Workspace root: ${node.root.rootId}`;
    item.iconPath = new vscode.ThemeIcon("root-folder");
    return item;
  }
  if (node.kind === "info") {
    return new vscode.TreeItem(node.label);
  }
  const item = new vscode.TreeItem(node.label);
  item.description = node.description;
  item.tooltip = node.tooltip;
  item.iconPath = new vscode.ThemeIcon(node.icon);
  if (node.command !== undefined) item.command = node.command;
  return item;
}

function snapshotNodes(
  kind: BackgroundView,
  rootId: string,
  snapshot: BackgroundSnapshot,
): TreeNode[] {
  switch (kind) {
    case "threads":
      return snapshot.threads.map((row) => ({
        ...entry(
        row.title === "" ? row.id : row.title,
        row.status,
        `${row.id}\nSession: ${row.sessionId}\nUpdated: ${row.updatedAt}`,
        row.status === "open" ? "comment-discussion" : "archive",
        ),
        command: {
          command: "codehelper.openChatSession",
          title: "Open CodeHelper Chat",
          arguments: [{ rootId, sessionId: row.sessionId }],
        },
      }));
    case "agents":
      return snapshot.agents.map((row) => entry(
        `${row.role} · ${row.id}`,
        row.status,
        `Session: ${row.sessionId}\n${row.lastMessage}`,
        terminal(row.status) ? "check" : "hubot",
      ));
    case "tasks":
      return snapshot.tasks.map(taskNode);
    case "jobs":
      return snapshot.jobs.map(taskNode);
    case "approvals":
      return snapshot.approvals.map((row) => entry(
        row.tool,
        "waiting",
        `${row.requestId}\n${row.resources.join("\n")}\nExpires: ${row.expiresAt}`,
        "shield",
      ));
    case "usage":
      return usageNodes(snapshot);
  }
}

function taskNode(row: BackgroundSnapshot["tasks"][number]): TreeNode {
  const detail = row.failureReason === "" ? row.id : row.failureReason;
  return entry(
    `${row.kind} · ${row.id}`,
    row.state,
    `${detail}\nAttempt: ${String(row.attempt)}/${String(row.maxAttempts)}`,
    row.state === "failed" ? "error" : terminal(row.state) ? "check" : "sync",
  );
}

function usageNodes(snapshot: BackgroundSnapshot): TreeNode[] {
  const usage = snapshot.usage;
  const cost = usage.costKnown
    ? `${(usage.costMicrounits / 1_000_000).toFixed(6)} USD`
    : "unknown";
  return [
    entry("Turns", String(usage.turns), "", "history"),
    entry("Calls", String(usage.calls), "", "symbol-method"),
    entry("Total tokens", String(usage.totalTokens), "", "symbol-number"),
    entry("Cached tokens", String(usage.cachedTokens), "", "database"),
    entry("Cost", cost, "", "credit-card"),
  ];
}

function rootDescription(snapshot: BackgroundSnapshot): string {
  const pending = snapshot.approvals.length;
  return pending === 0 ? "" : `${String(pending)} approvals`;
}

function entry(
  label: string,
  description: string,
  tooltip: string,
  icon: string,
): TreeNode {
  return { kind: "entry", label, description, tooltip, icon };
}

function terminal(status: string): boolean {
  return ["completed", "failed", "canceled", "errored", "interrupted", "shutdown"]
    .includes(status);
}

function affectedViews(kind: string): readonly BackgroundView[] {
  if (kind.startsWith("agent.")) return ["agents", "tasks", "jobs"];
  if (kind === "usage" || kind === "turn.receipt") return ["usage"];
  if (kind.startsWith("turn.")) return ["threads", "tasks", "jobs", "usage"];
  if (kind === "command.execution") return ["jobs", "tasks"];
  return [];
}

function isChatTarget(
  value: unknown,
): value is { readonly rootId: string; readonly sessionId: string } {
  if (typeof value !== "object" || value === null || Array.isArray(value)) {
    return false;
  }
  const candidate = value as Readonly<Record<string, unknown>>;
  return Object.keys(candidate).length === 2 &&
    typeof candidate["rootId"] === "string" &&
    /^[0-9a-f]{64}$/u.test(candidate["rootId"]) &&
    typeof candidate["sessionId"] === "string" &&
    candidate["sessionId"].length > 0 &&
    candidate["sessionId"].length <= 256;
}
