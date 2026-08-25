import type {
  AgentPreset,
  AgentPresetApplyResult,
  AgentPresetList,
  AgentPresetMutationResult,
  AgentPresetProfile,
  AgentList,
  AgentSummary,
  Bootstrap,
  CheckpointList,
  CheckpointForkResult,
  CredentialStatus,
  EditPlan,
  EditorContextReference,
  EditorRange,
  Envelope,
  EventFrame,
  ExtensionControlAction,
  ExtensionControlResult,
  ExtensionProjection,
  ModelCatalog,
  ModelCatalogEntry,
  OperationReceipt,
  PresentationSnapshot,
  Problem,
  ProviderCatalog,
  ProviderCatalogEntry,
  QueuedTurn,
  RuntimeEvent,
  SessionBinding,
  SessionDeleteResult,
  SessionExport,
  SessionHistoryPage,
  SessionLifecycleUpdate,
  SessionList,
  SessionMergeResult,
  SessionPlanArtifact,
  SessionPlanSnapshot,
  SessionProfileSnapshot,
  SessionProfileUpdateResult,
  SessionSummary,
  TaskList,
  TaskSummary,
  TraceSnapshot,
  ToolCatalog,
  TurnQueue,
  UsageQueryResult,
  UsageRollup,
  WorkspaceBrowseResult,
  WorkspaceDiagnosticContext,
  WorkspaceDiagnostics,
  WorkspaceDiff,
  WorkspaceImage,
  WorkspaceOpenResult,
  WorkspaceResource,
  WorkspaceSearchResult,
  WorkspaceSymbol,
  WorkspaceSymbolList
} from "../protocol";
import {
  webEventKinds,
  webProtocolVersion,
  type WebRPCRoute
} from "../protocol/web-host.generated";
import {
  IndexedDBBrowserStorage,
  type BrowserProjectionState,
  type BrowserStorage
} from "./storage";
import {
  ConversationProjection,
  emptyConversationSnapshot,
  type ConversationSnapshot
} from "../projection/conversation";
import {projectTurnQueue} from "../projection/turnQueue";
import {FrameNotifier} from "./notifier";

export type RuntimePhase =
  | "booting"
  | "ready"
  | "reconnecting"
  | "desynchronized"
  | "failed"
  | "draining";

export interface RuntimeSnapshot {
  phase: RuntimePhase;
  workspaceRoot: string;
  canOpenPath: boolean;
  includeArchived: boolean;
  contextResources: readonly EditorContextReference[];
  messageFeedback: Readonly<Record<string, "positive" | "negative">>;
  sessions: readonly SessionSummary[];
  selectedSessionID: string;
  hydratingSessionID: string;
  events: readonly RuntimeEvent[];
  queuedTurns: readonly QueuedTurn[];
  conversation: ConversationSnapshot;
  historyMoreBefore: boolean;
  providers: readonly ProviderCatalogEntry[];
  models: readonly ModelCatalogEntry[];
  profile?: SessionProfileSnapshot;
  tools: Readonly<ToolCatalog["tools"]>;
  checkpoints: Readonly<CheckpointList["checkpoints"]>;
  plan?: SessionPlanArtifact;
  tasks: readonly TaskSummary[];
  agents: readonly AgentSummary[];
  usage?: UsageRollup;
  trace?: TraceSnapshot;
  tracePhase: "idle" | "loading" | "ready" | "unavailable";
  traceProblem?: string;
  extensions: readonly ExtensionProjection[];
  mergePlan?: EditPlan;
  problem?: Problem;
  socketConnected: boolean;
}

type Listener = () => void;
type BufferedEvent = {event: RuntimeEvent; sessionID: string};
type Hydration = {
  generation: number;
  sessionID: string;
  events: BufferedEvent[];
};

const emptySnapshot: RuntimeSnapshot = {
  phase: "booting",
  workspaceRoot: "",
  canOpenPath: false,
  includeArchived: false,
  contextResources: [],
  messageFeedback: {},
  sessions: [],
  selectedSessionID: "",
  hydratingSessionID: "",
  events: [],
  queuedTurns: [],
  conversation: emptyConversationSnapshot(),
  historyMoreBefore: false,
  providers: [],
  models: [],
  tools: [],
  checkpoints: [],
  tasks: [],
  agents: [],
  extensions: [],
  tracePhase: "idle",
  socketConnected: false
};

const immediateEventKinds = new Set([
  "approval.required",
  "approval.resolved",
  "input.required",
  "input.resolved",
  "operation.rejected",
  "turn.queued",
  "turn.queue.updated",
  "turn.queue.removed",
  "turn.started",
  "turn.steered",
  "turn.completed",
  "turn.failed",
  "turn.canceled",
  "turn.receipt"
]);

const progressEventKinds = new Set([
  "plan.delta",
  "agent.spawned",
  "agent.status",
  "agent.message",
  "agent.integration",
  "run.started",
  "run.status",
  "run.completed",
  "run.failed",
  "run.canceled",
  "node.status",
  "attempt.status"
]);

const sessionActivityEventKinds = new Set([
  "approval.required",
  "approval.resolved",
  "input.required",
  "input.resolved",
  "turn.started",
  "turn.completed",
  "turn.failed",
  "turn.canceled"
]);

export class RuntimeClient {
  private token = "";
  private cursor = 0;
  private socket?: WebSocket;
  private sessionRefreshQueued = false;
  private reconnectTimer?: number;
  private bootTimer?: number;
  private generation = 0;
  private sessionListGeneration = 0;
  private selectionGeneration = 0;
  private hydration?: Hydration;
  private state: RuntimeSnapshot = emptySnapshot;
  private listeners = new Set<Listener>();
  private conversationProjection = new ConversationProjection();
  private pendingSelectedEvents: RuntimeEvent[] = [];
  private readonly eventNotifier = new FrameNotifier(
    () => this.flushSelectedEvents()
  );
  private storageScope = "";
  private stored: BrowserProjectionState = {
    cursor: 0,
    selectedSessionID: "",
    drafts: {},
    messageFeedback: {}
  };
  private storageWrite: Promise<void> = Promise.resolve();
  private storageTimer?: number;
  private pendingStorage?: {scope: string; value: BrowserProjectionState};

  constructor(
    private readonly storage: BrowserStorage = new IndexedDBBrowserStorage()
  ) {
    if (typeof window !== "undefined") {
      window.addEventListener("pagehide", this.flushBrowserState);
      document.addEventListener("visibilitychange", this.flushWhenHidden);
    }
  }

  subscribe = (listener: Listener): (() => void) => {
    this.listeners.add(listener);
    return () => this.listeners.delete(listener);
  };

  getSnapshot = (): RuntimeSnapshot => this.state;

  async start(): Promise<void> {
    if (this.bootTimer !== undefined) {
      window.clearTimeout(this.bootTimer);
      this.bootTimer = undefined;
    }
    try {
      const bootstrap = await this.fetchBootstrap();
      this.token = bootstrap.token;
      if (bootstrap.draining) {
        this.update({phase: "draining", problem: bootstrap.problem});
        return;
      }
      if (!bootstrap.ready) {
        this.update({
          phase: bootstrap.problem ? "failed" : "booting",
          workspaceRoot: bootstrap.workspace_root ?? "",
          canOpenPath: Boolean(bootstrap.can_open_path),
          problem: bootstrap.problem
        });
        if (!bootstrap.problem) {
          this.bootTimer = window.setTimeout(() => void this.start(), 500);
        }
        return;
      }
      await this.restoreBrowserState(bootstrap);
      this.update({
        phase: "reconnecting",
        workspaceRoot: bootstrap.workspace_root ?? "",
        canOpenPath: Boolean(bootstrap.can_open_path),
        problem: undefined
      });
      this.socket?.close(1000, "client reconnect");
      await this.connect();
      await this.refreshModelCatalog();
      await this.refreshSessions();
    } catch (error) {
      if (this.state.phase !== "reconnecting" &&
          this.state.phase !== "desynchronized") {
        this.fail(error);
      }
    }
  }

  stop(): void {
    this.eventNotifier.flushNow();
    this.flushBrowserState();
    this.generation += 1;
    if (this.reconnectTimer !== undefined) {
      window.clearTimeout(this.reconnectTimer);
    }
    if (this.bootTimer !== undefined) {
      window.clearTimeout(this.bootTimer);
    }
    this.sessionRefreshQueued = false;
    this.socket?.close(1000, "client stopped");
    this.socket = undefined;
  }

  async refreshSessions(
    query = "",
    hydrate = true,
    includeArchived = this.state.includeArchived
  ): Promise<void> {
    const generation = ++this.sessionListGeneration;
    const list = await this.call<SessionList>("session/list", {
      query,
      include_archived: includeArchived,
      limit: 200
    });
    if (generation !== this.sessionListGeneration) return;
    const selected = this.state.selectedSessionID || this.stored.selectedSessionID;
    const nextSelected =
      selected && list.sessions.some((item) => item.session_id === selected)
        ? selected
        : (list.sessions[0]?.session_id ?? "");
    this.update({
      sessions: list.sessions,
      selectedSessionID: nextSelected,
      includeArchived
    });
    if (hydrate && nextSelected && (!this.state.profile || nextSelected !== selected)) {
      await this.selectSession(nextSelected);
    }
  }

  async setArchivedVisible(includeArchived: boolean): Promise<void> {
    await this.refreshSessions("", true, includeArchived);
  }

  async createSession(
    isolation: "shared" | "worktree" = "shared",
    profilePatch?: Record<string, unknown>
  ): Promise<void> {
    const idempotencyKey = crypto.randomUUID();
    const sessionID = `session_web_${idempotencyKey}`;
    const binding = await this.call<SessionBinding>(
      "session/create",
      {session_id: sessionID, title: "New Chat", isolation},
      {idempotencyKey, retryNetwork: true}
    );
    await this.refreshSessions("", false);
    await this.selectSession(binding.session_id);
    if (profilePatch && Object.keys(profilePatch).length > 0) {
      await this.updateProfile(profilePatch);
    }
  }

  async updateSession(
    sessionID: string,
    expectedRevision: number,
    patch: {title?: string; pinned?: boolean; archived?: boolean}
  ): Promise<void> {
    await this.call<SessionLifecycleUpdate>("session/update", {
      session_id: sessionID,
      expected_revision: expectedRevision,
      patch
    });
    await this.refreshSessions();
  }

  async deleteSession(
    sessionID: string,
    expectedRevision: number,
    discard = false
  ): Promise<void> {
    await this.call<SessionDeleteResult>("session/delete", {
      session_id: sessionID,
      expected_revision: expectedRevision,
      discard
    });
    if (this.state.selectedSessionID === sessionID) {
      this.selectionGeneration += 1;
      this.hydration = undefined;
      this.update({
        selectedSessionID: "",
        hydratingSessionID: "",
        events: [],
        conversation: this.replaceConversation([]),
        queuedTurns: [],
        historyMoreBefore: false,
        profile: undefined,
        tools: [],
        checkpoints: [],
        plan: undefined,
        tasks: [],
        agents: [],
        usage: undefined,
        trace: undefined,
        tracePhase: "idle",
        traceProblem: undefined,
        mergePlan: undefined,
        contextResources: []
      });
    }
    await this.refreshSessions();
  }

  async selectSession(sessionID: string): Promise<void> {
    this.eventNotifier.cancel();
    this.pendingSelectedEvents = [];
    const previousSessionID = this.state.selectedSessionID;
    const generation = ++this.selectionGeneration;
    const hydration: Hydration = {generation, sessionID, events: []};
    this.hydration = hydration;
    this.update({
      selectedSessionID: sessionID,
      hydratingSessionID: sessionID,
      events: [],
      conversation: this.replaceConversation([]),
      queuedTurns: [],
      historyMoreBefore: false,
      profile: undefined,
      tools: [],
      checkpoints: [],
      plan: undefined,
      tasks: [],
      agents: [],
      usage: undefined,
      trace: undefined,
      tracePhase: "loading",
      traceProblem: undefined,
      extensions: [],
      mergePlan: undefined,
      contextResources: [],
      problem: undefined
    });
    try {
    const summary = this.state.sessions.find((item) => item.session_id === sessionID);
    await this.call<SessionBinding>("session/activate", {
      session_id: sessionID,
      thread_id: summary?.thread_id
    });
    if (generation !== this.selectionGeneration) return;
    const snapshot = await this.call<PresentationSnapshot>("session/snapshot", {
      session_id: sessionID
    });
    if (generation !== this.selectionGeneration) return;
    const snapshotEvents = snapshot.events ?? [];
    const traceTurnIDs = turnIDs(snapshotEvents);
    const details = await Promise.allSettled([
      this.call<SessionProfileSnapshot>("profile/get", {session_id: sessionID}),
      this.call<ToolCatalog>("tool/catalog", {session_id: sessionID}),
      this.call<CheckpointList>("checkpoint/list", {session_id: sessionID, limit: 20}),
      this.call<SessionPlanSnapshot>("plan/get", {session_id: sessionID}),
      this.call<TaskList>("task/list", {session_id: sessionID, limit: 20}),
      this.call<AgentList>("agent/list", {session_id: sessionID, limit: 20}),
      this.call<UsageQueryResult>("usage/query", {
        session_id: sessionID,
        include_children: true,
        limit: 100
      }),
      this.call<ExtensionControlResult>("extension/list", {kind: "all"}),
      traceTurnIDs.length > 0
        ? this.call<TraceSnapshot>("trace/query", {
          session_id: sessionID,
          turn_ids: traceTurnIDs,
          through_sequence: snapshot.through_sequence
        })
        : Promise.resolve<TraceSnapshot>({
          version: 1,
          session_id: sessionID,
          through_sequence: snapshot.through_sequence,
          turns: []
        }),
      this.call<TurnQueue>("turn/queue", {session_id: sessionID})
    ]);
    if (generation !== this.selectionGeneration || this.hydration !== hydration) return;
    const profile = fulfilled(details[0]);
    const catalog = fulfilled(details[1]);
    const checkpoints = fulfilled(details[2]);
    const plan = fulfilled(details[3]);
    const tasks = fulfilled(details[4]);
    const agents = fulfilled(details[5]);
    const usage = fulfilled(details[6]);
    const extensions = fulfilled(details[7]);
    const trace = fulfilled(details[8]);
    const queue = fulfilled(details[9]);
    const liveEvents = hydration.events
      .filter(({event, sessionID: owner}) =>
        owner === sessionID && event.sequence > snapshot.through_sequence
      )
      .map(({event}) => event)
      .sort((left, right) => left.sequence - right.sequence);
    const latestBufferedSequence = hydration.events.reduce(
      (latest, {event}) => Math.max(latest, event.sequence),
      0
    );
    const refreshForForeignEvent = hydration.events.some(
      ({sessionID: owner}) => owner !== sessionID
    );
    const refreshForDefaultTitle = summary?.title === "New Chat" &&
      snapshotEvents.some((event) => event.kind === "turn.started");
    this.commitCursor(Math.max(
      snapshot.through_sequence,
      latestBufferedSequence
    ));
    this.hydration = undefined;
    const events = [...(snapshot.events ?? []), ...liveEvents];
    this.update({
      selectedSessionID: sessionID,
      hydratingSessionID: "",
      events,
      conversation: this.replaceConversation(events),
      historyMoreBefore: Boolean(snapshot.history_truncated_before),
      profile,
      tools: catalog?.tools ?? [],
      checkpoints: checkpoints?.checkpoints ?? [],
      plan: plan?.artifact,
      mergePlan: undefined,
      tasks: tasks?.tasks ?? [],
      agents: agents?.agents ?? [],
      usage: usage?.rollup,
      trace,
      tracePhase: trace ? "ready" : "unavailable",
      traceProblem: details[8]?.status === "rejected"
        ? errorMessage(details[8].reason)
        : undefined,
      extensions: extensions?.extensions ?? [],
      queuedTurns: projectTurnQueue(queue?.items ?? [], liveEvents),
      contextResources: [],
      problem: undefined
    });
    this.persistSelectedSession(sessionID);
    if (refreshForForeignEvent || refreshForDefaultTitle) {
      void this.refreshSessions("", false);
    }
    } catch (error) {
      if (this.hydration === hydration) {
        this.hydration = undefined;
        this.update({
          selectedSessionID: previousSessionID,
          hydratingSessionID: ""
        });
      }
      throw error;
    }
  }

  async submitPrompt(prompt: string): Promise<OperationReceipt> {
    const sessionID = this.requireSession();
    const key = crypto.randomUUID();
    const receipt = await this.call<OperationReceipt>("operation/submit", {
      session_id: sessionID,
      kind: "turn.start",
      idempotency_key: key,
      payload: {
        prompt,
        display_prompt: prompt,
        intent: this.state.profile?.profile.mode === "plan" ? "plan" : "answer",
        context: this.state.contextResources
      }
    });
    this.update({contextResources: []});
    await this.refreshSessions("", false);
    return receipt;
  }

  async loadDraft(sessionID = this.state.selectedSessionID): Promise<string> {
    if (!sessionID) return "";
    return this.stored.drafts[sessionID] ?? "";
  }

  saveDraft(
    value: string,
    sessionID = this.state.selectedSessionID
  ): void {
    if (!sessionID) return;
    const drafts = {...this.stored.drafts};
    if (value) {
      drafts[sessionID] = value;
    } else {
      delete drafts[sessionID];
    }
    this.stored = {...this.stored, drafts};
    this.persistBrowserState();
  }

  toggleMessageFeedback(
    messageID: string,
    rating: "positive" | "negative",
    sessionID = this.state.selectedSessionID
  ): void {
    if (!sessionID || !messageID) return;
    const key = `${sessionID}:${messageID}`;
    const messageFeedback = {...(this.stored.messageFeedback ?? {})};
    if (messageFeedback[key] === rating) {
      delete messageFeedback[key];
    } else {
      messageFeedback[key] = rating;
    }
    this.stored = {...this.stored, messageFeedback};
    this.update({messageFeedback});
    this.persistBrowserState();
  }

  async compactThread(): Promise<OperationReceipt> {
    const session = this.state.sessions.find(
      (item) => item.session_id === this.state.selectedSessionID
    );
    const turnID = session?.latest_turn_id;
    if (!session || !turnID || this.state.conversation.activeTurnID) {
      throw new Error("No completed turn is available to compact");
    }
    return this.call<OperationReceipt>("operation/submit", {
      session_id: session.session_id,
      kind: "thread.compact",
      idempotency_key: crypto.randomUUID(),
      payload: {
        thread_id: session.thread_id,
        turn_id: turnID,
        item_id: `compact-${crypto.randomUUID()}`
      }
    });
  }

  async cancel(turnID: string): Promise<OperationReceipt> {
    return this.call<OperationReceipt>("operation/submit", {
      session_id: this.requireSession(),
      kind: "turn.cancel",
      idempotency_key: crypto.randomUUID(),
      payload: {turn_id: turnID, reason: "user requested stop"}
    });
  }

  async steer(turnID: string, prompt: string): Promise<OperationReceipt> {
    const normalized = prompt.trim();
    if (!normalized) throw new Error("Steering prompt is required");
    return this.call<OperationReceipt>("operation/submit", {
      session_id: this.requireSession(),
      kind: "turn.steer",
      idempotency_key: crypto.randomUUID(),
      payload: {turn_id: turnID, prompt: normalized}
    });
  }

  async enqueue(turnID: string, prompt: string): Promise<OperationReceipt> {
    const normalized = prompt.trim();
    if (!normalized) throw new Error("Queued prompt is required");
    const receipt = await this.call<OperationReceipt>("operation/submit", {
      session_id: this.requireSession(),
      kind: "turn.enqueue",
      idempotency_key: crypto.randomUUID(),
      payload: {
        turn_id: turnID,
        prompt: normalized,
        display_prompt: normalized,
        intent: this.state.profile?.profile.mode === "plan" ? "plan" : "answer",
        context: this.state.contextResources
      }
    });
    this.update({contextResources: []});
    return receipt;
  }

  async updateQueuedTurn(queueID: string, prompt: string): Promise<OperationReceipt> {
    const item = this.requireQueuedTurn(queueID);
    const normalized = prompt.trim();
    if (!normalized) throw new Error("Queued prompt is required");
    return this.call<OperationReceipt>("operation/submit", {
      session_id: this.requireSession(),
      kind: "turn.queue.update",
      idempotency_key: crypto.randomUUID(),
      payload: {
        thread_id: item.thread_id,
        turn_id: item.source_turn_id,
        queue_id: queueID,
        prompt: normalized,
        display_prompt: normalized
      }
    });
  }

  async removeQueuedTurn(queueID: string): Promise<OperationReceipt> {
    const item = this.requireQueuedTurn(queueID);
    return this.call<OperationReceipt>("operation/submit", {
      session_id: this.requireSession(),
      kind: "turn.queue.remove",
      idempotency_key: crypto.randomUUID(),
      payload: {
        thread_id: item.thread_id,
        turn_id: item.source_turn_id,
        queue_id: queueID
      }
    });
  }

  async promoteQueuedTurn(
    queueID: string,
    turnID: string
  ): Promise<OperationReceipt> {
    const item = this.requireQueuedTurn(queueID);
    return this.call<OperationReceipt>("operation/submit", {
      session_id: this.requireSession(),
      kind: "turn.queue.promote",
      idempotency_key: crypto.randomUUID(),
      payload: {
        thread_id: item.thread_id,
        turn_id: turnID,
        queue_id: queueID
      }
    });
  }

  async decideApproval(
    requestID: string,
    decision: "approve" | "deny" | "cancel",
    planID = "",
    scope = "",
    replacementArguments?: Record<string, unknown>
  ): Promise<OperationReceipt> {
    return this.call<OperationReceipt>("operation/submit", {
      session_id: this.requireSession(),
      kind: "approval.decision",
      idempotency_key: crypto.randomUUID(),
      payload: {
        request_id: requestID,
        decision,
        plan_id: planID,
        scope,
        replacement_arguments: replacementArguments
      }
    });
  }

  async replyInput(
    requestID: string,
    answer: string,
    values?: Record<string, string>
  ): Promise<OperationReceipt> {
    return this.call<OperationReceipt>("operation/submit", {
      session_id: this.requireSession(),
      kind: "input.reply",
      idempotency_key: crypto.randomUUID(),
      payload: {request_id: requestID, answer, values}
    });
  }

  async recoverTurn(
    sourceTurnID: string,
    action: "retry" | "continue",
    guidance = ""
  ): Promise<OperationReceipt> {
    return this.call<OperationReceipt>("turn/recover", {
      version: 1,
      action,
      session_id: this.requireSession(),
      source_turn_id: sourceTurnID,
      guidance,
      idempotency_key: crypto.randomUUID()
    });
  }

  async updateProfile(
    patch: Record<string, unknown>
  ): Promise<SessionProfileUpdateResult> {
    const generation = this.selectionGeneration;
    const snapshot = this.state.profile;
    const profile = snapshot?.profile;
    const session = this.state.sessions.find(
      (item) => item.session_id === this.state.selectedSessionID
    );
    if (!profile || !session) {
      throw new Error("No active session");
    }
    const result = await this.call<SessionProfileUpdateResult>("profile/update", {
      session_id: session.session_id,
      thread_id: session.thread_id,
      expected_revision: profile.revision,
      patch
    });
    const [authoritative, catalog] = await Promise.all([
      this.call<SessionProfileSnapshot>("profile/get", {
        session_id: session.session_id
      }),
      this.call<ToolCatalog>("tool/catalog", {
        session_id: session.session_id
      })
    ]);
    if (generation !== this.selectionGeneration ||
        session.session_id !== this.state.selectedSessionID) {
      return result;
    }
    this.update({profile: authoritative, tools: catalog.tools ?? []});
    return {...result, profile: authoritative.profile};
  }

  async listAgentPresets(): Promise<AgentPresetList> {
    return this.call<AgentPresetList>("agent-preset/list", {
      session_id: this.requireSession()
    });
  }

  async saveAgentPreset(input: {
    id?: string;
    expectedRevision?: number;
    name: string;
    description?: string;
    profile: AgentPresetProfile;
  }): Promise<AgentPresetMutationResult> {
    const id = input.id || `preset-${crypto.randomUUID()}`;
    return this.call<AgentPresetMutationResult>("agent-preset/save", {
      session_id: this.requireSession(),
      id,
      expected_revision: input.expectedRevision ?? 0,
      name: input.name,
      description: input.description ?? "",
      profile: input.profile
    }, {
      idempotencyKey: id,
      retryNetwork: true
    });
  }

  async deleteAgentPreset(
    preset: Pick<AgentPreset, "id" | "revision">
  ): Promise<AgentPresetMutationResult> {
    return this.call<AgentPresetMutationResult>("agent-preset/delete", {
      session_id: this.requireSession(),
      id: preset.id,
      expected_revision: preset.revision
    });
  }

  async applyAgentPreset(presetID: string): Promise<AgentPresetApplyResult> {
    const session = this.state.sessions.find(
      (item) => item.session_id === this.state.selectedSessionID
    );
    const profile = this.state.profile?.profile;
    if (!session || !profile) throw new Error("No active session");
    const result = await this.call<AgentPresetApplyResult>("agent-preset/apply", {
      session_id: session.session_id,
      thread_id: session.thread_id,
      preset_id: presetID,
      expected_profile_revision: profile.revision
    });
    const [authoritative, catalog] = await Promise.all([
      this.call<SessionProfileSnapshot>("profile/get", {
        session_id: session.session_id
      }),
      this.call<ToolCatalog>("tool/catalog", {
        session_id: session.session_id
      })
    ]);
    this.update({profile: authoritative, tools: catalog.tools ?? []});
    return result;
  }

  async loadEarlierHistory(limit = 200): Promise<number> {
    const sessionID = this.requireSession();
    const before = this.state.events[0]?.sequence;
    if (!before || !this.state.historyMoreBefore) return 0;
    const page = await this.call<SessionHistoryPage>("session/history", {
      session_id: sessionID,
      before_sequence: before,
      limit
    });
    if (sessionID !== this.state.selectedSessionID) return 0;
    const known = new Set(this.state.events.map((event) => event.sequence));
    const earlier = page.events.filter((event) => !known.has(event.sequence));
    const events = [...earlier, ...this.state.events];
    this.update({
      events,
      conversation: this.replaceConversation(events),
      historyMoreBefore: Boolean(page.more_before)
    });
    return earlier.length;
  }

  async setToolEnabled(toolID: string, enabled: boolean): Promise<void> {
    const current = this.state.profile?.profile.enabled_tool_ids ?? [];
    const enabledToolIDs = enabled
      ? [...new Set([...current, toolID])]
      : current.filter((id) => id !== toolID);
    await this.updateProfile({enabled_tool_ids: enabledToolIDs});
  }

  async restoreCheckpoint(checkpointID: string): Promise<void> {
    await this.call("checkpoint/restore", {
      session_id: this.requireSession(),
      checkpoint_id: checkpointID
    });
    await this.selectSession(this.requireSession());
  }

  async forkCheckpoint(checkpointID: string): Promise<void> {
    const result = await this.call<CheckpointForkResult>("checkpoint/fork", {
      session_id: this.requireSession(),
      checkpoint_id: checkpointID,
      title: "Checkpoint Fork"
    });
    await this.refreshSessions();
    await this.selectSession(result.session_id);
  }

  async transitionPlan(transition: "implement" | "autopilot"): Promise<void> {
    if (!this.state.plan) {
      throw new Error("No active plan");
    }
    await this.call("plan/transition", {
      session_id: this.requireSession(),
      plan_id: this.state.plan.id,
      transition
    });
  }

  async setExtensionEnabled(
    kind: "plugin" | "skill",
    name: string,
    enabled: boolean
  ): Promise<ExtensionControlResult> {
    const mutation = await this.call<ExtensionControlResult>("extension/control", {
      version: 1,
      id: `extop-${crypto.randomUUID()}`,
      kind,
      action: enabled ? "enable" : "disable",
      name,
      created_at: new Date().toISOString()
    });
    const result = await this.call<ExtensionControlResult>("extension/list", {kind: "all"});
    this.update({extensions: result.extensions ?? []});
    return {...mutation, extensions: result.extensions ?? mutation.extensions};
  }

  async controlExtension(
    kind: "plugin" | "skill",
    name: string,
    action: ExtensionControlAction,
    capability = ""
  ): Promise<ExtensionControlResult> {
    const result = await this.call<ExtensionControlResult>("extension/control", {
      version: 1,
      id: `extop-${crypto.randomUUID()}`,
      kind,
      action,
      name,
      capability,
      created_at: new Date().toISOString()
    });
    if (!["detail", "health", "permissions", "receipts"].includes(action)) {
      const refreshed = await this.call<ExtensionControlResult>(
        "extension/list",
        {kind: "all"}
      );
      this.update({extensions: refreshed.extensions ?? []});
    }
    return result;
  }

  async previewMerge(): Promise<void> {
    const result = await this.call<SessionMergeResult>("session/merge", {
      session_id: this.requireSession(),
      action: "preview"
    });
    this.update({mergePlan: result.plan});
  }

  async applyMerge(): Promise<void> {
    if (!this.state.mergePlan) {
      throw new Error("No merge preview");
    }
    await this.call<SessionMergeResult>("session/merge", {
      session_id: this.requireSession(),
      action: "apply",
      plan_id: this.state.mergePlan.id
    });
    this.update({mergePlan: undefined});
    await this.refreshSessions();
  }

  async browseWorkspace(path = "."): Promise<WorkspaceBrowseResult> {
    return this.call<WorkspaceBrowseResult>("workspace/browse", {path, limit: 200});
  }

  async searchWorkspace(query: string): Promise<WorkspaceSearchResult> {
    return this.call<WorkspaceSearchResult>("workspace/search", {query, limit: 100});
  }

  async readWorkspaceResource(path: string): Promise<WorkspaceResource> {
    return this.call<WorkspaceResource>("workspace/resource", {path});
  }

  async openWorkspacePath(path: string): Promise<WorkspaceOpenResult> {
    return this.call<WorkspaceOpenResult>("workspace/open", {path});
  }

  async readWorkspaceImage(path: string): Promise<WorkspaceImage> {
    return this.call<WorkspaceImage>("workspace/image", {path});
  }

  async searchWorkspaceSymbols(
    query: string,
    path = ""
  ): Promise<WorkspaceSymbolList> {
    return this.call<WorkspaceSymbolList>("workspace/symbols", {
      query,
      path,
      limit: 100
    });
  }

  async workspaceDiagnostics(): Promise<WorkspaceDiagnostics> {
    return this.call<WorkspaceDiagnostics>("workspace/diagnostics", {
      session_id: this.requireSession()
    });
  }

  async downloadWorkspaceContent(handle: string): Promise<Blob> {
    if (!handle) throw new Error("Content handle is required");
    const response = await fetch(`/api/v1/content/${encodeURIComponent(handle)}`, {
      method: "GET",
      headers: {"Authorization": `Bearer ${this.token}`},
      credentials: "same-origin"
    });
    if (!response.ok) {
      throw new Error(`Content download failed (${response.status})`);
    }
    return response.blob();
  }

  async workspaceDiff(): Promise<WorkspaceDiff> {
    return this.call<WorkspaceDiff>("workspace/diff", {
      session_id: this.requireSession()
    });
  }

  addWorkspaceContext(resource: WorkspaceResource, range?: EditorRange): void {
    const resources = this.state.contextResources.filter(
      (value) => value.path !== resource.path
    );
    this.update({
      contextResources: [
        ...resources,
        {
          kind: range ? "selection" : "file",
          source: "composer",
          uri: resource.uri,
          path: resource.path,
          document_version: resource.document_version,
          digest: resource.digest,
          range,
          explicit: true
        }
      ]
    });
  }

  addImageContext(image: WorkspaceImage): void {
    const resources = this.state.contextResources.filter(
      (value) => value.kind !== "image" || value.path !== image.path
    );
    this.update({
      contextResources: [
        ...resources,
        {
          kind: "image",
          source: "native_picker",
          uri: image.uri,
          path: image.path,
          document_version: image.document_version,
          digest: image.digest,
          label: image.label,
          media_type: image.media_type,
          explicit: true
        }
      ]
    });
  }

  addAttachmentContext(reference: EditorContextReference): void {
    if (reference.kind !== "attachment" &&
        !(reference.kind === "image" && !reference.path)) {
      throw new Error("Composer attachment context is invalid");
    }
    const resources = this.state.contextResources.filter(
      (value) => value.digest !== reference.digest
    );
    this.update({contextResources: [...resources, reference]});
  }

  removeAttachmentContext(digest: string): void {
    this.update({
      contextResources: this.state.contextResources.filter(
        (value) => value.digest !== digest
      )
    });
  }

  addSymbolContext(symbol: WorkspaceSymbol): void {
    const resources = this.state.contextResources.filter(
      (value) =>
        value.kind !== "symbol" ||
        value.path !== symbol.path ||
        value.symbol?.name !== symbol.name
    );
    this.update({
      contextResources: [
        ...resources,
        {
          kind: "symbol",
          source: "native_picker",
          uri: symbol.uri,
          path: symbol.path,
          document_version: symbol.document_version,
          digest: symbol.digest,
          range: symbol.range,
          symbol: {
            name: symbol.name,
            kind: symbol.kind,
            selection_range: symbol.selection_range
          },
          explicit: true
        }
      ]
    });
  }

  addDiagnosticsContext(value: WorkspaceDiagnosticContext): void {
    const context = value.context;
    const resources = this.state.contextResources.filter(
      (resource) =>
        resource.kind !== "diagnostics" ||
        resource.path !== context.path
    );
    this.update({contextResources: [...resources, context]});
  }

  addGitDiffContext(diff: WorkspaceDiff): void {
    const resources = this.state.contextResources.filter(
      (value) => value.kind !== "git_diff"
    );
    this.update({
      contextResources: [
        ...resources,
        {
          kind: "git_diff",
          source: "composer",
          digest: diff.digest,
          label: "Current workspace diff",
          media_type: "text/plain",
          content: diff.diff,
          explicit: true
        }
      ]
    });
  }

  async addTerminalContext(callID: string, content: string): Promise<void> {
    if (!callID || !content) throw new Error("Tool output context is incomplete");
    const digest = await sha256Hex(content);
    const resources = this.state.contextResources.filter(
      (value) => value.kind !== "terminal" || value.label !== callID
    );
    this.update({
      contextResources: [
        ...resources,
        {
          kind: "terminal",
          source: "composer",
          digest,
          label: callID,
          media_type: "text/plain",
          content,
          explicit: true
        }
      ]
    });
  }

  removeContext(
    kind: EditorContextReference["kind"],
    path = "",
    label = "",
    symbolName = ""
  ): void {
    this.update({
      contextResources: this.state.contextResources.filter(
        (resource) =>
          resource.kind !== kind ||
          (resource.path ?? "") !== path ||
          (resource.label ?? "") !== label ||
          (symbolName !== "" && resource.symbol?.name !== symbolName)
      )
    });
  }

  async exportSession(): Promise<SessionExport> {
    return this.call<SessionExport>("session/export", {
      session_id: this.requireSession()
    });
  }

  async diagnostics(): Promise<Record<string, unknown>> {
    return this.call<Record<string, unknown>>("system/diagnostics", {});
  }

  async credentialStatus(): Promise<CredentialStatus> {
    return this.call<CredentialStatus>("credential/status", {});
  }

  async setKeyringCredential(secret: string): Promise<CredentialStatus> {
    return this.call<CredentialStatus>("credential/set-keyring", {secret});
  }

  async clearKeyringCredential(): Promise<CredentialStatus> {
    return this.call<CredentialStatus>("credential/clear-keyring", {});
  }

  async validateCredential(): Promise<CredentialStatus> {
    return this.call<CredentialStatus>("credential/validate", {});
  }

  private async fetchBootstrap(): Promise<Bootstrap> {
    const response = await fetch("/api/v1/bootstrap", {
      cache: "no-store",
      credentials: "same-origin"
    });
    if (!response.ok) {
      throw new Error(`Bootstrap failed (${response.status})`);
    }
    return response.json() as Promise<Bootstrap>;
  }

  private async call<T>(
    route: WebRPCRoute,
    body: unknown,
    options: {idempotencyKey?: string; retryNetwork?: boolean} = {}
  ): Promise<T> {
    const headers: Record<string, string> = {
      "Authorization": `Bearer ${this.token}`,
      "Content-Type": "application/json",
      "X-CodeHelper-Request-ID": crypto.randomUUID()
    };
    if (options.idempotencyKey) {
      headers["Idempotency-Key"] = options.idempotencyKey;
    }
    let response: Response;
    try {
      response = await fetch(`/api/v1/${route}`, {
        method: "POST",
        headers,
        body: JSON.stringify(body)
      });
    } catch (error) {
      if (!options.retryNetwork) throw error;
      response = await fetch(`/api/v1/${route}`, {
        method: "POST",
        headers,
        body: JSON.stringify(body)
      });
    }
    const envelope = (await response.json()) as Envelope<T>;
    if (!response.ok || envelope.problem) {
      throw new RuntimeProblem(
        envelope.problem ?? {
          version: 1,
          code: "internal",
          message: `Request failed (${response.status})`,
          retryable: false
        }
      );
    }
    if (envelope.result === undefined) {
      throw new Error(`Web API ${route} returned no result`);
    }
    return envelope.result;
  }

  private connect(): Promise<void> {
    const generation = ++this.generation;
    const scheme = window.location.protocol === "https:" ? "wss:" : "ws:";
    const socket = new WebSocket(`${scheme}//${window.location.host}/api/v1/events`);
    this.socket = socket;
    return new Promise<void>((resolve, reject) => {
      let connected = false;
      socket.addEventListener("open", () => {
        if (generation !== this.generation) return;
        socket.send(JSON.stringify({
          type: "authenticate",
          token: this.token,
          cursor: this.cursor
        }));
      });
      socket.addEventListener("message", (message) => {
        if (generation !== this.generation) return;
        let frame: EventFrame;
        try {
          frame = decodeEventFrame(message.data);
        } catch (error) {
          this.resetProjection();
          this.update({
            phase: "desynchronized",
            socketConnected: false,
            problem: protocolProblem(error)
          });
          socket.close();
          if (!connected) reject(error);
          return;
        }
        if (frame.type === "hello") {
          connected = true;
          this.update({socketConnected: true, phase: "ready"});
          resolve();
          return;
        }
        if (!connected) {
          const error = new Error("Web event stream sent data before hello");
          this.resetProjection();
          this.update({
            phase: "desynchronized",
            socketConnected: false,
            problem: protocolProblem(error)
          });
          socket.close();
          reject(error);
          return;
        }
        if (frame.type === "desync") {
          this.resetProjection();
          this.update({
            phase: "desynchronized",
            socketConnected: false,
            problem: frame.problem
          });
          socket.close();
          return;
        }
        if (frame.type === "resync") {
          this.resetProjection();
          this.update({
            phase: "reconnecting",
            socketConnected: false,
            problem: frame.problem
          });
          socket.close();
          return;
        }
        if (frame.type === "watermark") {
          this.commitCursor(frame.sequence);
          return;
        }
        if (frame.type === "event" && frame.event) {
          this.applyEvent(frame.event, frame.session_id ?? "");
        }
      });
      socket.addEventListener("close", () => {
        if (generation !== this.generation) {
          if (!connected) reject(new Error("Web event stream was superseded"));
          return;
        }
        if (this.state.phase === "desynchronized") {
          return;
        }
        this.update({socketConnected: false, phase: "reconnecting"});
        if (!connected) {
          reject(new Error("Web event stream closed before readiness"));
        }
        this.reconnectTimer = window.setTimeout(() => void this.start(), 700);
      });
      socket.addEventListener("error", () => {
        socket.close();
      });
    });
  }

  private resetProjection(): void {
    this.eventNotifier.cancel();
    this.pendingSelectedEvents = [];
    this.commitCursor(0, true);
    this.selectionGeneration += 1;
    this.hydration = undefined;
    this.update({
      events: [],
      conversation: this.replaceConversation([]),
      queuedTurns: [],
      historyMoreBefore: false,
      hydratingSessionID: "",
      profile: undefined,
      tools: [],
      checkpoints: [],
      plan: undefined,
      tasks: [],
      agents: [],
      usage: undefined,
      trace: undefined,
      tracePhase: "idle",
      traceProblem: undefined,
      extensions: [],
      mergePlan: undefined,
      contextResources: []
    });
  }

  private applyEvent(event: RuntimeEvent, sessionID: string): void {
    if (event.sequence <= this.cursor) return;
    if (this.hydration) {
      if (!this.hydration.events.some(({event: value}) => value.sequence === event.sequence)) {
        this.hydration.events.push({event, sessionID});
      }
      return;
    }
    this.commitCursor(event.sequence);
    if (sessionID !== this.state.selectedSessionID) {
      if (sessionActivityEventKinds.has(event.kind)) {
        this.scheduleSessionRefresh();
      }
      return;
    }
    this.pendingSelectedEvents.push(event);
    if (immediateEventKinds.has(event.kind)) {
      this.eventNotifier.flushNow();
    } else {
      this.eventNotifier.schedule();
    }
    if (sessionActivityEventKinds.has(event.kind)) {
      this.scheduleSessionRefresh();
    }
    if (isTerminal(event.kind)) {
      this.scheduleSessionRefresh();
      void this.refreshUsage(sessionID);
      void this.refreshTrace(sessionID);
    }
    if (isTerminal(event.kind) || progressEventKinds.has(event.kind)) {
      void this.refreshProgress(sessionID);
    }
  }

  private flushSelectedEvents(): void {
    if (this.pendingSelectedEvents.length === 0) return;
    const pending = this.pendingSelectedEvents;
    this.pendingSelectedEvents = [];
    for (const event of pending) this.conversationProjection.apply(event);
    this.update({
      events: [...this.state.events, ...pending],
      conversation: this.conversationProjection.snapshot(),
      queuedTurns: projectTurnQueue(this.state.queuedTurns, pending)
    });
  }

  private replaceConversation(
    events: readonly RuntimeEvent[]
  ): ConversationSnapshot {
    this.conversationProjection = new ConversationProjection();
    this.conversationProjection.applyAll(events);
    return this.conversationProjection.snapshot();
  }

  private requireQueuedTurn(queueID: string): QueuedTurn {
    const item = this.state.queuedTurns.find((value) => value.queue_id === queueID);
    if (!item) throw new Error("Queued turn is no longer available");
    return item;
  }

  private async refreshUsage(sessionID: string): Promise<void> {
    const generation = this.selectionGeneration;
    const result = await this.call<UsageQueryResult>("usage/query", {
      session_id: sessionID,
      include_children: true,
      limit: 100
    });
    if (
      generation === this.selectionGeneration &&
      sessionID === this.state.selectedSessionID
    ) {
      this.update({usage: result.rollup});
    }
  }

  private async refreshProgress(sessionID: string): Promise<void> {
    const generation = this.selectionGeneration;
    try {
      const [plan, tasks, agents] = await Promise.all([
        this.call<SessionPlanSnapshot>("plan/get", {session_id: sessionID}),
        this.call<TaskList>("task/list", {session_id: sessionID, limit: 20}),
        this.call<AgentList>("agent/list", {session_id: sessionID, limit: 20})
      ]);
      if (
        generation === this.selectionGeneration &&
        sessionID === this.state.selectedSessionID
      ) {
        this.update({
          plan: plan.artifact,
          tasks: tasks.tasks ?? [],
          agents: agents.agents ?? []
        });
      }
    } catch {
      // Initial hydration remains the last authoritative read model.
    }
  }

  async refreshTrace(sessionID = this.state.selectedSessionID): Promise<void> {
    if (!sessionID || sessionID !== this.state.selectedSessionID) return;
    this.eventNotifier.flushNow();
    const generation = this.selectionGeneration;
    const events = this.state.events;
    const ids = turnIDs(events);
    if (ids.length === 0) {
      this.update({trace: undefined, tracePhase: "ready", traceProblem: undefined});
      return;
    }
    this.update({tracePhase: "loading", traceProblem: undefined});
    try {
      const trace = await this.call<TraceSnapshot>("trace/query", {
        session_id: sessionID,
        turn_ids: ids,
        through_sequence: events.at(-1)?.sequence ?? 0
      });
      if (
        generation !== this.selectionGeneration ||
        sessionID !== this.state.selectedSessionID ||
        trace.through_sequence < (this.state.events.at(-1)?.sequence ?? 0)
      ) {
        return;
      }
      this.update({trace, tracePhase: "ready", traceProblem: undefined});
    } catch (error) {
      if (
        generation === this.selectionGeneration &&
        sessionID === this.state.selectedSessionID
      ) {
        this.update({
          tracePhase: "unavailable",
          traceProblem: errorMessage(error)
        });
      }
    }
  }

  private scheduleSessionRefresh(): void {
    if (this.sessionRefreshQueued) return;
    this.sessionRefreshQueued = true;
    const generation = this.generation;
    queueMicrotask(() => {
      this.sessionRefreshQueued = false;
      if (generation !== this.generation) return;
      void this.refreshSessions("", false);
    });
  }

  private update(patch: Partial<RuntimeSnapshot>): void {
    this.state = Object.freeze({...this.state, ...patch});
    this.listeners.forEach((listener) => listener());
  }

  private async refreshModelCatalog(): Promise<void> {
    const [providers, models] = await Promise.all([
      this.call<ProviderCatalog>("provider/list", {}),
      this.call<ModelCatalog>("model/list", {})
    ]);
    this.update({
      providers: providers.providers ?? [],
      models: models.models ?? []
    });
  }

  private async restoreBrowserState(bootstrap: Bootstrap): Promise<void> {
    const rootID = bootstrap.workspace?.root_id ?? bootstrap.workspace_root ?? "";
    const scope = [
      `v${bootstrap.protocol_version}`,
      bootstrap.server_build || "unknown",
      rootID
    ].join(":");
    if (scope === this.storageScope) return;
    this.storageScope = scope;
    const restored = await this.storage.load(scope).catch(() => undefined);
    this.stored = {
      cursor: Math.max(0, restored?.cursor ?? 0),
      selectedSessionID: restored?.selectedSessionID ?? "",
      drafts: {...(restored?.drafts ?? {})},
      messageFeedback: {...(restored?.messageFeedback ?? {})}
    };
    this.cursor = Math.max(0, this.stored.cursor);
    this.update({
      selectedSessionID: this.stored.selectedSessionID,
      events: [],
      conversation: this.replaceConversation([]),
      queuedTurns: [],
      historyMoreBefore: false,
      providers: [],
      models: [],
      profile: undefined,
      tools: [],
      checkpoints: [],
      plan: undefined,
      tasks: [],
      agents: [],
      usage: undefined,
      trace: undefined,
      tracePhase: "idle",
      traceProblem: undefined,
      extensions: [],
      mergePlan: undefined,
      contextResources: [],
      messageFeedback: {...(this.stored.messageFeedback ?? {})}
    });
  }

  private commitCursor(cursor: number, allowReset = false): void {
    const next = allowReset ? cursor : Math.max(this.cursor, cursor);
    if (next === this.cursor && next === this.stored.cursor) return;
    this.cursor = next;
    this.stored = {...this.stored, cursor: next};
    this.persistBrowserState();
  }

  private persistSelectedSession(sessionID: string): void {
    if (this.stored.selectedSessionID === sessionID) return;
    this.stored = {...this.stored, selectedSessionID: sessionID};
    this.persistBrowserState();
  }

  private persistBrowserState(): void {
    if (!this.storageScope) return;
    this.pendingStorage = {
      scope: this.storageScope,
      value: {
        cursor: this.stored.cursor,
        selectedSessionID: this.stored.selectedSessionID,
        drafts: {...this.stored.drafts},
        messageFeedback: {...(this.stored.messageFeedback ?? {})}
      }
    };
    if (this.storageTimer !== undefined) return;
    this.storageTimer = window.setTimeout(this.flushBrowserState, 100);
  }

  private readonly flushWhenHidden = (): void => {
    if (document.visibilityState === "hidden") this.flushBrowserState();
  };

  private readonly flushBrowserState = (): void => {
    if (this.storageTimer !== undefined) {
      window.clearTimeout(this.storageTimer);
      this.storageTimer = undefined;
    }
    const pending = this.pendingStorage;
    this.pendingStorage = undefined;
    if (!pending) return;
    this.storageWrite = this.storageWrite
      .catch(() => undefined)
      .then(() => this.storage.save(pending.scope, pending.value))
      .catch(() => undefined);
  };

  private fail(error: unknown): void {
    const problem =
      error instanceof RuntimeProblem
        ? error.problem
        : {
            version: 1,
            code: "internal",
            message: error instanceof Error ? error.message : String(error),
            retryable: true
          };
    this.update({phase: "failed", problem});
  }

  private requireSession(): string {
    if (this.state.hydratingSessionID) {
      throw new Error("Session is still loading");
    }
    if (!this.state.selectedSessionID) {
      throw new Error("No active session");
    }
    return this.state.selectedSessionID;
  }
}

export class RuntimeProblem extends Error {
  constructor(readonly problem: Problem) {
    super(problem.message);
  }
}

const eventKindSet = new Set<string>(webEventKinds);
const eventFrameTypes = new Set(["hello", "event", "watermark", "resync", "desync"]);

function decodeEventFrame(value: unknown): EventFrame {
  const decoded = JSON.parse(String(value)) as unknown;
  if (!decoded || typeof decoded !== "object") {
    throw new Error("Web event frame must be an object");
  }
  const frame = decoded as Partial<EventFrame>;
  if (frame.protocol_version !== webProtocolVersion) {
    throw new Error(`Unsupported Web protocol version ${String(frame.protocol_version)}`);
  }
  if (typeof frame.type !== "string" || !eventFrameTypes.has(frame.type)) {
    throw new Error(`Unknown Web event frame type ${String(frame.type)}`);
  }
  if (!Number.isSafeInteger(frame.sequence) || Number(frame.sequence) < 0) {
    throw new Error("Web event frame sequence is invalid");
  }
  if (frame.type === "event") {
    if (!frame.event || !eventKindSet.has(frame.event.kind) ||
        frame.event.sequence !== frame.sequence) {
      throw new Error("Web event frame contains an unknown or inconsistent event");
    }
  } else if (frame.event !== undefined) {
    throw new Error("Non-event Web frame contains an event payload");
  }
  return frame as EventFrame;
}

function protocolProblem(error: unknown): Problem {
  return {
    version: 1,
    code: "protocol_mismatch",
    message: error instanceof Error ? error.message : String(error),
    retryable: false
  };
}

function fulfilled<T>(result: PromiseSettledResult<T>): T | undefined {
  return result.status === "fulfilled" ? result.value : undefined;
}

function turnIDs(events: readonly RuntimeEvent[]): string[] {
  return [...new Set(events.map((event) => event.turn_id).filter(Boolean))];
}

function errorMessage(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}

async function sha256Hex(value: string): Promise<string> {
  const digest = await crypto.subtle.digest(
    "SHA-256",
    new TextEncoder().encode(value)
  );
  return [...new Uint8Array(digest)]
    .map((byte) => byte.toString(16).padStart(2, "0"))
    .join("");
}

export function isTerminal(kind: string): boolean {
  return kind === "turn.completed" || kind === "turn.failed" || kind === "turn.canceled";
}
