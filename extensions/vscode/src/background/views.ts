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
  readonly projectors: Map<string, BackgroundProjector>;
  readonly emptyProjector: BackgroundProjector;
  readonly query: BackgroundQuery;
  readonly pending: Set<BackgroundView>;
  selectedSessionId: string;
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
      registry.onEvent(({ root, sessionId, event, replayed }) => {
        this.#onEvent(root, sessionId, event, replayed);
      }),
      registry.onStateChange(({ root, snapshot }) => {
        if (snapshot.state === "ready") {
          this.#syncSessions(root);
          for (const kind of this.#visible) this.#schedule(root.rootId, kind);
        }
      }),
      registry.onDidChangeRoots(() => {
        this.#syncRoots();
        for (const provider of this.#providers.values()) provider.refresh();
      }),
      registry.onDidChangeSessions((root) => {
        this.#syncSessions(root);
        for (const kind of this.#visible) this.#schedule(root.rootId, kind);
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
          projectors: new Map(),
          emptyProjector: new BackgroundProjector(),
          query: new BackgroundQuery({
            request: (method, params) => root.controller.query(method, params),
          }),
          pending: new Set(),
          selectedSessionId: "",
        });
      }
    }
    for (const rootId of this.#roots.keys()) {
      if (!live.has(rootId)) this.#roots.delete(rootId);
    }
  }

  #snapshots(): readonly RootSnapshot[] {
    return this.#registry.roots.map((root) => {
      const background = this.#root(root.rootId);
      const projector = background.projectors.get(background.selectedSessionId) ??
        background.emptyProjector;
      return {
        rootId: root.rootId,
        label: root.label,
        snapshot: projector.snapshot(),
      };
    });
  }

  #onEvent(
    root: WorkspaceRuntime,
    sessionId: string,
    event: DecodedEvent,
    replayed: boolean,
  ): void {
    const background = this.#root(root.rootId);
    const projector = this.#projector(background, sessionId);
    this.#notify(root, projector.applyEvent(event, replayed));
    if (sessionId !== background.selectedSessionId) return;
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
      const sessionId = root.selectedSessionId;
      if (sessionId === "") return;
      const projector = this.#projector(root, sessionId);
      try {
        const reads: Promise<void>[] = [];
        if (pending.includes("threads")) {
          reads.push(root.query.threads().then((rows) => {
            projector.replaceThreads(rows);
            this.#providers.get("threads")?.refresh();
          }));
        }
        if (pending.includes("agents")) {
          reads.push(root.query.agents(sessionId).then((rows) => {
            projector.replaceAgents(rows);
            this.#providers.get("agents")?.refresh();
          }));
        }
        if (pending.includes("tasks") || pending.includes("jobs")) {
          reads.push(root.query.tasks(sessionId).then((rows) => {
            this.#notify(root.root, projector.replaceTasks(rows));
            this.#providers.get("tasks")?.refresh();
            this.#providers.get("jobs")?.refresh();
          }));
        }
        if (pending.includes("usage")) {
          reads.push(root.query.usage(sessionId).then((usage) => {
            projector.replaceUsage(usage);
            this.#providers.get("usage")?.refresh();
          }));
        }
        if (pending.includes("approvals")) {
          this.#providers.get("approvals")?.refresh();
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

  #syncSessions(root: WorkspaceRuntime): void {
    const background = this.#root(root.rootId);
    let sessionIds: readonly string[];
    let selectedSessionId: string;
    try {
      sessionIds = root.controller.sessions().map((session) => session.sessionId);
      selectedSessionId = root.controller.identity().sessionId;
    } catch {
      return;
    }
    const live = new Set(sessionIds);
    for (const sessionId of background.projectors.keys()) {
      if (!live.has(sessionId)) background.projectors.delete(sessionId);
    }
    background.selectedSessionId = selectedSessionId;
    this.#projector(background, selectedSessionId);
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

  #projector(
    root: RootBackground,
    sessionId: string,
  ): BackgroundProjector {
    let projector = root.projectors.get(sessionId);
    if (projector === undefined) {
      projector = new BackgroundProjector();
      root.projectors.set(sessionId, projector);
    }
    return projector;
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
    if (element.kind === "root") {
      return snapshotNodes(
        this.#kind, element.root.rootId, element.root.snapshot,
      );
    }
    if (element.kind === "agent") return agentChildren(element);
    if (element.kind === "agentTimelineGroup") {
      return timelineNodes(element.snapshot, element.agentId);
    }
    return [];
  }

  public dispose(): void {
    this.#changes.dispose();
  }
}

type TreeNode =
  | { readonly kind: "root"; readonly root: RootSnapshot }
  | {
      readonly kind: "agent";
      readonly row: BackgroundSnapshot["agents"][number];
      readonly snapshot: BackgroundSnapshot;
    }
  | {
      readonly kind: "integration";
      readonly row: BackgroundSnapshot["integrations"][number];
    }
  | {
      readonly kind: "agentTimelineGroup";
      readonly snapshot: BackgroundSnapshot;
      readonly agentId?: string;
    }
  | {
      readonly kind: "agentTimeline";
      readonly row: BackgroundSnapshot["agentTimeline"][number];
    }
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
  if (node.kind === "agent") {
    const children = agentChildren(node);
    const item = new vscode.TreeItem(
      node.row.path,
      children.length === 0
        ? vscode.TreeItemCollapsibleState.None
        : vscode.TreeItemCollapsibleState.Expanded,
    );
    item.description = node.row.status;
    item.tooltip =
      `Role: ${node.row.role}\nID: ${node.row.id}\n` +
      `Revision: ${String(node.row.revision)}\nThread: ${node.row.threadId}\n` +
      `Parent: ${node.row.parentPath}\nSession: ${node.row.sessionId}\n` +
      node.row.lastMessage;
    item.iconPath = new vscode.ThemeIcon(
      terminal(node.row.status) ? "check" : "hubot",
    );
    return item;
  }
  if (node.kind === "integration") {
    const item = new vscode.TreeItem(
      `Integration ${node.row.previewDigest.slice(0, 12)}`,
    );
    item.description = node.row.status;
    item.tooltip = integrationTooltip(node.row);
    item.iconPath = new vscode.ThemeIcon(integrationIcon(node.row));
    return item;
  }
  if (node.kind === "agentTimelineGroup") {
    const count = node.agentId === undefined
      ? node.snapshot.agentTimeline.length
      : node.snapshot.agentTimeline.filter(
        (row) => row.agentId === node.agentId,
      ).length;
    const item = new vscode.TreeItem(
      node.agentId === undefined ? "Global Timeline" : "Timeline",
      vscode.TreeItemCollapsibleState.Collapsed,
    );
    item.description = `${String(count)} events`;
    item.iconPath = new vscode.ThemeIcon("history");
    return item;
  }
  if (node.kind === "agentTimeline") {
    const identity = node.row.agentPath || node.row.agentId;
    const item = new vscode.TreeItem(
      `#${String(node.row.sequence)} ${identity}`,
    );
    item.description = node.row.status === ""
      ? node.row.kind
      : `${node.row.kind} · ${node.row.status}`;
    item.tooltip = node.row.message;
    item.iconPath = new vscode.ThemeIcon(timelineIcon(node.row.kind));
    return item;
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
      return rootAgentNodes(snapshot);
    case "tasks":
      return snapshot.tasks.map(taskNode);
    case "jobs":
      return snapshot.jobs.map(taskNode);
    case "approvals":
      return snapshot.approvals.map((row) => entry(
        row.agentPath === "" ? row.tool : `${row.agentPath}: ${row.tool}`,
        row.agentRole === "" ? "waiting" : `${row.agentRole} · waiting`,
        `${row.requestId}\n` +
          (row.parentPath === "" ? "" : `Parent: ${row.parentPath}\n`) +
          `${row.resources.join("\n")}\nExpires: ${row.expiresAt}`,
        "shield",
      ));
    case "usage":
      return usageNodes(snapshot);
  }
}

function rootAgentNodes(snapshot: BackgroundSnapshot): TreeNode[] {
  const ids = new Set(snapshot.agents.map((row) => row.id));
  const agents: TreeNode[] = snapshot.agents
    .filter((row) => row.parentId === "" || !ids.has(row.parentId))
    .sort((left, right) => left.path.localeCompare(right.path))
    .map((row) => ({ kind: "agent", row, snapshot }));
  if (snapshot.agentTimeline.length !== 0) {
    agents.push({ kind: "agentTimelineGroup", snapshot });
  }
  return agents;
}

function agentChildren(
  node: Extract<TreeNode, { readonly kind: "agent" }>,
): TreeNode[] {
  const agents: TreeNode[] = node.snapshot.agents
    .filter((row) => row.parentId === node.row.id)
    .sort((left, right) => left.path.localeCompare(right.path))
    .map((row) => ({ kind: "agent", row, snapshot: node.snapshot }));
  const integrations: TreeNode[] = node.snapshot.integrations
    .filter((row) => row.agentId === node.row.id)
    .map((row) => ({ kind: "integration", row }));
  const timeline: TreeNode[] = node.snapshot.agentTimeline.some(
    (row) => row.agentId === node.row.id,
  )
    ? [{
      kind: "agentTimelineGroup",
      snapshot: node.snapshot,
      agentId: node.row.id,
    }]
    : [];
  return [...agents, ...integrations, ...timeline];
}

function timelineNodes(
  snapshot: BackgroundSnapshot,
  agentId?: string,
): TreeNode[] {
  return snapshot.agentTimeline
    .filter((row) => agentId === undefined || row.agentId === agentId)
    .slice(-100)
    .reverse()
    .map((row) => ({ kind: "agentTimeline", row }));
}

function timelineIcon(kind: BackgroundSnapshot["agentTimeline"][number]["kind"]): string {
  switch (kind) {
    case "spawn": return "add";
    case "status": return "pulse";
    case "message": return "mail";
    case "approval": return "shield";
    case "integration": return "git-merge";
  }
}

function integrationTooltip(
  row: BackgroundSnapshot["integrations"][number],
): string {
  const details = [
    `Digest: ${row.previewDigest}`,
    `Parent: ${row.parentPath}`,
    `Paths: ${row.paths.join(", ") || "none"}`,
  ];
  if (row.conflicts.length !== 0) {
    details.push(`Conflicts: ${row.conflicts.join("; ")}`);
  }
  if (row.changedPaths.length !== 0) {
    details.push(`Applied: ${row.changedPaths.join(", ")}`);
  }
  if (row.verification !== "") {
    details.push(`Verification: ${row.verification}`);
  }
  if (row.appliedAt !== "") details.push(`Applied at: ${row.appliedAt}`);
  if (row.message !== "") details.push(row.message);
  return details.join("\n");
}

function integrationIcon(
  row: BackgroundSnapshot["integrations"][number],
): string {
  if (row.conflicts.length !== 0 || row.status === "failed") return "error";
  if (row.status === "applied") {
    return row.verification === "failed" ? "warning" : "pass";
  }
  if (row.status === "discarded") return "trash";
  return row.status === "applying" ? "sync" : "diff";
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
  return ["completed", "failed", "interrupted", "integrated",
    "integration_failed", "closed"]
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
