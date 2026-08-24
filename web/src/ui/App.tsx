import {
  AlertTriangle,
  ArrowDown,
  Archive,
  Braces,
  Check,
  ChevronDown,
  ChevronRight,
  CircleStop,
  Download,
  FileCode2,
  FolderTree,
  GitFork,
  GitCompareArrows,
  LoaderCircle,
  KeyRound,
  Moon,
  PanelLeftClose,
  PanelLeftOpen,
  PanelRightClose,
  PanelRightOpen,
  Pencil,
  Pin,
  PinOff,
  Plus,
  Play,
  RefreshCw,
  RotateCcw,
  ScanSearch,
  Search,
  Send,
  Settings2,
  Sun,
  TerminalSquare,
  TextSelect,
  Trash2,
  Wrench,
  X
} from "lucide-react";
import {
  lazy,
  memo,
  Suspense,
  useCallback,
  useEffect,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
  useSyncExternalStore
} from "react";
import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";
import type {
  CredentialStatus,
  EditorRange,
  RuntimeEvent,
  SessionSummary,
  WorkspaceDiagnosticContext,
  WorkspaceEntry,
  WorkspaceDiff,
  WorkspaceImage,
  WorkspaceResource,
  WorkspaceSearchMatch,
  WorkspaceSymbol
} from "../protocol";
import {
  projectConversation,
  type ConversationNode
} from "../projection/conversation";
import {RuntimeClient, type RuntimeSnapshot} from "../runtime/client";
import {CapybaraMark} from "./brand/CapybaraMark";
import {CodeHelperWordmark} from "./brand/CodeHelperWordmark";
import {experience} from "./experience";

interface Props {
  client: RuntimeClient;
}

const transcriptPageSize = 200;
const Trajectory = lazy(async () => ({
  default: (await import("./Trajectory")).Trajectory
}));

function initialDetailOpen(): boolean {
  return typeof window.matchMedia !== "function" ||
    window.matchMedia("(min-width: 1241px)").matches;
}

function initialRailCollapsed(): boolean {
  return readPreference("ch.sidebar.collapsed") === "true";
}

function storedPanelWidth(key: string, fallback: number): number {
  const stored = readPreference(key);
  if (stored === null) return fallback;
  const value = Number(stored);
  return Number.isFinite(value) ? value : fallback;
}

function readPreference(key: string): string | null {
  try {
    return window.localStorage?.getItem(key) ?? null;
  } catch {
    return null;
  }
}

function writePreference(key: string, value: string): void {
  try {
    window.localStorage?.setItem(key, value);
  } catch {
    // Browser preferences are optional and never affect Runtime state.
  }
}

export function App({client}: Props) {
  const snapshot = useSyncExternalStore(
    client.subscribe,
    client.getSnapshot,
    client.getSnapshot
  );
  const [query, setQuery] = useState("");
  const [draft, setDraft] = useState("");
  const [draftOwner, setDraftOwner] = useState("");
  const [detailOpen, setDetailOpen] = useState(initialDetailOpen);
  const [railCollapsed, setRailCollapsed] = useState(initialRailCollapsed);
  const [railWidth, setRailWidth] = useState(
    () => storedPanelWidth(
      "ch.sidebar.width",
      experience.layout.sidebarDefault
    )
  );
  const [detailWidth, setDetailWidth] = useState(
    () => storedPanelWidth(
      "ch.details.width",
      experience.layout.detailsDefault
    )
  );
  const [activeView, setActiveView] = useState<"chat" | "trajectory">("chat");
  const [inspectCallID, setInspectCallID] = useState("");
  const [blankDetailRequested, setBlankDetailRequested] = useState(false);
  const [settingsOpen, setSettingsOpen] = useState(false);
  const [submitting, setSubmitting] = useState(false);
  const [creatingSession, setCreatingSession] = useState(false);
  const [localError, setLocalError] = useState("");
  const [sessionAction, setSessionAction] = useState<{
    sessionID: string;
    pending: boolean;
    error: string;
  }>();
  const [workspaceQuery, setWorkspaceQuery] = useState("");
  const [workspaceMatches, setWorkspaceMatches] = useState<readonly WorkspaceSearchMatch[]>([]);
  const [workspacePath, setWorkspacePath] = useState(".");
  const [workspaceEntries, setWorkspaceEntries] = useState<readonly WorkspaceEntry[]>([]);
  const [workspaceResource, setWorkspaceResource] = useState<WorkspaceResource>();
  const [workspaceSelection, setWorkspaceSelection] = useState<EditorRange>();
  const [workspaceImage, setWorkspaceImage] = useState<WorkspaceImage>();
  const [workspaceImageURL, setWorkspaceImageURL] = useState("");
  const [workspaceSymbolQuery, setWorkspaceSymbolQuery] = useState("");
  const [workspaceSymbols, setWorkspaceSymbols] = useState<readonly WorkspaceSymbol[]>([]);
  const [workspaceDiagnostics, setWorkspaceDiagnostics] =
    useState<readonly WorkspaceDiagnosticContext[]>([]);
  const [workspaceDiff, setWorkspaceDiff] = useState<WorkspaceDiff>();
  const [newIsolation, setNewIsolation] = useState<"shared" | "worktree">("shared");
  const [credentialStatus, setCredentialStatus] = useState<CredentialStatus>();
  const [diagnostics, setDiagnostics] = useState("");
  const [transcriptPage, setTranscriptPage] = useState(0);
  const transcriptRef = useRef<HTMLDivElement>(null);
  const transcriptContentRef = useRef<HTMLDivElement>(null);
  const prependAnchorRef = useRef<{height: number; top: number}>();
  const atBottomRef = useRef(true);
  const [atBottom, setAtBottom] = useState(true);
  const textareaRef = useRef<HTMLTextAreaElement>(null);
  const selectedSessionRef = useRef(snapshot.selectedSessionID);
  const draftRef = useRef(draft);
  selectedSessionRef.current = snapshot.selectedSessionID;
  draftRef.current = draft;
  const selected = snapshot.sessions.find(
    (item) => item.session_id === snapshot.selectedSessionID
  );
  const entries = useMemo(
    () => snapshot.conversation.order.flatMap((id) => {
      const node = snapshot.conversation.nodes.get(id);
      return node ? [node] : [];
    }),
    [snapshot.conversation]
  );
  const transcriptEnd = Math.max(0, entries.length - transcriptPage * transcriptPageSize);
  const transcriptStart = Math.max(0, transcriptEnd - transcriptPageSize);
  const visibleEntries = entries.slice(transcriptStart, transcriptEnd);
  const pendingApproval = snapshot.conversation.pendingApproval;
  const pendingInput = snapshot.conversation.pendingInput;
  const pendingApprovalKey = pendingRequestKey(snapshot.selectedSessionID, pendingApproval);
  const pendingInputKey = pendingRequestKey(snapshot.selectedSessionID, pendingInput);
  const activeTurn = snapshot.conversation.activeTurnID;
  const selectedProvider = snapshot.profile?.profile.provider ?? "";
  const selectedModel = snapshot.profile?.profile.model ?? "";
  const selectedProviderEntry = snapshot.providers.find(
    (provider) => provider.id === selectedProvider
  );
  const selectedModelEntry = snapshot.models.find(
    (model) =>
      model.provider === selectedProvider &&
      model.id === selectedModel
  );
  const modelOptions = snapshot.models
    .filter((model) => model.provider === selectedProvider)
    .map((model) => ({
      value: model.id,
      label: model.capabilities.display_name || model.id,
      disabled: model.capabilities.availability !== "available",
      reason: model.capabilities.unavailable_reason,
      detail: modelCapabilityLabel(model.capabilities)
    }));
  const reasoningValues = [
    "",
    ...(selectedModelEntry?.capabilities.reasoning_efforts ?? [])
  ];
  const latestReceipt = [...entries].reverse().find(
    (entry): entry is Extract<ConversationNode, {kind: "receipt"}> =>
      entry.kind === "receipt"
  );
  const blankSession = Boolean(
    selected && entries.length === 0 && !snapshot.hydratingSessionID
  );
  const detailVisible = Boolean(
    selected &&
    detailOpen &&
    (!blankSession || blankDetailRequested)
  );
  const reportLocalError = useCallback((error: unknown) => {
    setLocalError(error instanceof Error ? error.message : String(error));
  }, []);
  const inspectTool = useCallback((callID: string) => {
    setInspectCallID(callID);
    setActiveView("trajectory");
    void client.refreshTrace();
  }, [client]);

  useEffect(() => {
    setWorkspaceSelection(undefined);
  }, [workspaceResource?.digest]);

  useEffect(() => {
    writePreference("ch.sidebar.collapsed", String(railCollapsed));
  }, [railCollapsed]);

  useEffect(() => {
    writePreference("ch.sidebar.width", String(railWidth));
  }, [railWidth]);

  useEffect(() => {
    writePreference("ch.details.width", String(detailWidth));
  }, [detailWidth]);

  useEffect(() => {
    setTranscriptPage(0);
    setActiveView("chat");
    setInspectCallID("");
    setBlankDetailRequested(false);
    atBottomRef.current = true;
    setAtBottom(true);
  }, [snapshot.selectedSessionID]);

  useEffect(() => () => {
    if (workspaceImageURL) URL.revokeObjectURL(workspaceImageURL);
  }, [workspaceImageURL]);

  useEffect(() => {
    void client.start();
    return () => client.stop();
  }, [client]);

  useEffect(() => {
    const sessionID = snapshot.selectedSessionID;
    let current = true;
    setDraftOwner("");
    setDraft("");
    if (sessionID) {
      void client.loadDraft(sessionID).then((value) => {
        if (!current) return;
        setDraft(value);
        setDraftOwner(sessionID);
      });
    }
    return () => {
      current = false;
    };
  }, [client, snapshot.selectedSessionID]);

  useEffect(() => {
    if (!draftOwner) return;
    const timeout = window.setTimeout(() => {
      client.saveDraft(draft, draftOwner);
    }, 150);
    return () => window.clearTimeout(timeout);
  }, [client, draft, draftOwner]);

  useLayoutEffect(() => {
    const node = transcriptRef.current;
    if (!node) return;
    const prepend = prependAnchorRef.current;
    if (prepend) {
      prependAnchorRef.current = undefined;
      node.scrollTop = prepend.top + node.scrollHeight - prepend.height;
      return;
    }
    if (atBottomRef.current && transcriptPage === 0) {
      node.scrollTop = node.scrollHeight;
    }
  }, [snapshot.conversation.revision, transcriptPage]);

  useEffect(() => {
    const textarea = textareaRef.current;
    if (!textarea) return;
    textarea.style.height = "0";
    textarea.style.height = `${Math.min(textarea.scrollHeight, 336)}px`;
  }, [draft]);

  useEffect(() => {
    if (activeView !== "trajectory" || !activeTurn) return;
    const interval = window.setInterval(() => {
      void client.refreshTrace();
    }, 1_000);
    return () => window.clearInterval(interval);
  }, [activeTurn, activeView, client]);

  useEffect(() => {
    if (!settingsOpen) return;
    const close = (event: KeyboardEvent) => {
      if (event.key === "Escape") setSettingsOpen(false);
    };
    window.addEventListener("keydown", close);
    return () => window.removeEventListener("keydown", close);
  }, [settingsOpen]);

  const submit = async () => {
    const prompt = draft.trim();
    if (!prompt || submitting) return;
    const submittedSessionID = snapshot.selectedSessionID;
    setSubmitting(true);
    setLocalError("");
    try {
      await client.submitPrompt(prompt);
      if (selectedSessionRef.current === submittedSessionID &&
          draftRef.current.trim() === prompt) {
        setDraft("");
        client.saveDraft("", submittedSessionID);
      }
    } catch (error) {
      setLocalError(error instanceof Error ? error.message : String(error));
    } finally {
      setSubmitting(false);
    }
  };

  const createSession = async (profilePatch?: Record<string, unknown>) => {
    if (creatingSession) return;
    setCreatingSession(true);
    setLocalError("");
    try {
      await client.createSession(newIsolation, profilePatch);
    } catch (error) {
      reportLocalError(error);
    } finally {
      setCreatingSession(false);
    }
  };

  const exportSession = async () => {
    try {
      const value = await client.exportSession();
      const blob = new Blob([`${JSON.stringify(value, null, 2)}\n`], {
        type: "application/json"
      });
      const url = URL.createObjectURL(blob);
      const anchor = document.createElement("a");
      anchor.href = url;
      anchor.download = `${safeFilename(value.session.title)}.codehelper.json`;
      anchor.click();
      URL.revokeObjectURL(url);
    } catch (error) {
      reportLocalError(error);
    }
  };

  const downloadWorkspaceResource = async (
    resource: Pick<WorkspaceResource | WorkspaceImage, "content_handle" | "path">
  ) => {
    try {
      const blob = await client.downloadWorkspaceContent(resource.content_handle);
      const url = URL.createObjectURL(blob);
      const anchor = document.createElement("a");
      anchor.href = url;
      anchor.download = resource.path.split("/").at(-1) || "download";
      anchor.click();
      URL.revokeObjectURL(url);
    } catch (error) {
      reportLocalError(error);
    }
  };

  const runSessionAction = async (
    session: SessionSummary,
    action: () => Promise<void>
  ) => {
    setSessionAction({
      sessionID: session.session_id,
      pending: true,
      error: ""
    });
    try {
      await action();
      setSessionAction(undefined);
    } catch (error) {
      setSessionAction({
        sessionID: session.session_id,
        pending: false,
        error: error instanceof Error ? error.message : String(error)
      });
    }
  };

  const deleteSession = (session: SessionSummary) => {
    const hasUnfinishedWork =
      sessionIsBusy(session) || session.isolation === "worktree";
    const prompt = hasUnfinishedWork
      ? `Delete "${session.title}" and permanently discard its unfinished work?`
      : `Delete "${session.title}"?`;
    if (!window.confirm(prompt)) return;
    void runSessionAction(session, () => client.deleteSession(
      session.session_id,
      session.revision,
      true
    ));
  };

  const openWorkspacePath = async (path: string) => {
    try {
      if (isWorkspaceImagePath(path)) {
        const image = await client.readWorkspaceImage(path);
        const blob = await client.downloadWorkspaceContent(image.content_handle);
        setWorkspaceResource(undefined);
        setWorkspaceSelection(undefined);
        setWorkspaceImage(image);
        setWorkspaceImageURL(URL.createObjectURL(blob));
        return;
      }
      const resource = await client.readWorkspaceResource(path);
      setWorkspaceImage(undefined);
      setWorkspaceImageURL("");
      setWorkspaceResource(resource);
    } catch (error) {
      reportLocalError(error);
    }
  };

  if (snapshot.phase === "booting") {
    return <BootState title="Starting CodeHelper" detail={snapshot.workspaceRoot} />;
  }
  if (snapshot.phase === "failed") {
    return (
      <BootState
        title="Runtime unavailable"
        detail={snapshot.problem?.message ?? "The Runtime could not start."}
        failed
      />
    );
  }

  return (
    <div
      className="app"
      data-detail-open={detailVisible || undefined}
      data-rail-collapsed={railCollapsed || undefined}
      style={{
        "--ch-rail-width": `${railWidth}px`,
        "--ch-detail-width": `${detailWidth}px`
      } as React.CSSProperties}
    >
      <aside className="sessionRail" aria-label="Sessions">
        {selected && (
          <div className="newSessionRow">
            <span>New session</span>
            <select
              className="newSessionIsolation"
              aria-label="New session isolation"
              value={newIsolation}
              onChange={(event) => setNewIsolation(
                event.target.value as "shared" | "worktree"
              )}
            >
              <option value="shared">Shared</option>
              <option value="worktree">Worktree</option>
            </select>
          </div>
        )}
        <div className="brandRow">
          <button
            className="railToggle"
            aria-label={railCollapsed ? "Expand sidebar" : "Collapse sidebar"}
            title={railCollapsed ? "Expand sidebar" : "Collapse sidebar"}
            onClick={() => setRailCollapsed((value) => !value)}
          >
            <span className="railLogo"><CapybaraMark /></span>
            <span className="railToggleIcon">
              {railCollapsed
                ? <PanelLeftOpen size={17} />
                : <PanelLeftClose size={17} />}
            </span>
          </button>
          <span className="brandName">CodeHelper</span>
          <IconButton
            label="New chat"
            icon={<Plus size={17} />}
            disabled={creatingSession}
            onClick={() => void createSession()}
          />
        </div>
        <label className="searchBox">
          <Search size={15} aria-hidden="true" />
          <span className="srOnly">Search sessions</span>
          <input
            value={query}
            placeholder="Search"
            onChange={(event) => {
              const value = event.target.value;
              setQuery(value);
              void client.refreshSessions(value);
            }}
          />
          {query && (
            <button
              className="clearSearch"
              aria-label="Clear search"
              onClick={() => {
                setQuery("");
                void client.refreshSessions();
              }}
            >
              <X size={14} />
            </button>
          )}
        </label>
        <div className="sessionList">
          {snapshot.sessions.map((session) => (
            <SessionRow
              key={session.session_id}
              session={session}
              active={session.session_id === snapshot.selectedSessionID}
              onClick={() => void client.selectSession(session.session_id)}
              onRename={() => {
                const title = window.prompt("Rename session", session.title)?.trim();
                if (title && title !== session.title) {
                  void runSessionAction(session, () => client.updateSession(
                    session.session_id, session.revision, {title}
                  ));
                }
              }}
              onPin={() => void runSessionAction(session, () => client.updateSession(
                session.session_id, session.revision, {pinned: !session.pinned}
              ))}
              onArchive={() => {
                if (session.archived || window.confirm(`Archive "${session.title}"?`)) {
                  void runSessionAction(session, () => client.updateSession(
                    session.session_id, session.revision, {archived: !session.archived}
                  ));
                }
              }}
              onDelete={() => deleteSession(session)}
              actionPending={
                sessionAction?.sessionID === session.session_id &&
                sessionAction.pending
              }
              actionError={
                sessionAction?.sessionID === session.session_id
                  ? sessionAction.error
                  : ""
              }
            />
          ))}
        </div>
        <div className="railFooter">
          <IconButton
            label={snapshot.includeArchived ? "Hide archived sessions" : "Show archived sessions"}
            icon={<Archive size={16} />}
            onClick={() => void client.setArchivedVisible(
              !snapshot.includeArchived
            ).catch(reportLocalError)}
          />
          <span className="connectionState" data-online={snapshot.socketConnected || undefined}>
            <span className="statusDot" />
            {snapshot.socketConnected ? "Connected" : snapshot.phase}
          </span>
          <IconButton
            label="Settings"
            icon={<Settings2 size={16} />}
            onClick={() => setSettingsOpen((value) => !value)}
          />
        </div>
        {!railCollapsed && (
          <ResizeHandle
            label="Resize sidebar"
            value={railWidth}
            minimum={experience.layout.sidebarMinimum}
            maximum={experience.layout.sidebarMaximum}
            onDelta={(delta) => setRailWidth((width) =>
              clamp(
                width + delta,
                experience.layout.sidebarMinimum,
                experience.layout.sidebarMaximum
              )
            )}
          />
        )}
      </aside>

      <main className="conversation" data-empty={blankSession || undefined}>
        <header
          className="conversationHeader"
          data-hidden={blankSession || undefined}
        >
          <div className="conversationIdentity">
            <div>
              <h1>{selected?.title ?? "New Chat"}</h1>
              <p>{snapshot.workspaceRoot}</p>
            </div>
            {selected && entries.length > 0 && (
              <nav className="viewTabs" aria-label="Conversation views">
                <button
                  aria-current={activeView === "chat" ? "page" : undefined}
                  onClick={() => setActiveView("chat")}
                >
                  Chat
                </button>
                <button
                  aria-current={activeView === "trajectory" ? "page" : undefined}
                  onClick={() => {
                    setActiveView("trajectory");
                    void client.refreshTrace();
                  }}
                >
                  Trajectory
                </button>
              </nav>
            )}
          </div>
          <div className="headerActions">
            {snapshot.hydratingSessionID ? (
              <span className="workingLabel">Loading</span>
            ) : activeTurn ? (
              <span className="workingLabel">Working</span>
            ) : null}
            {selected && (
              <>
                <IconButton
                  label="Export session"
                  disabled={Boolean(snapshot.hydratingSessionID)}
                  icon={<Download size={17} />}
                  onClick={() => void exportSession()}
                />
                <IconButton
                  label={detailOpen ? "Close detail panel" : "Open detail panel"}
                  icon={detailOpen
                    ? <PanelRightClose size={17} />
                    : <PanelRightOpen size={17} />}
                  onClick={() => setDetailOpen((value) => !value)}
                />
              </>
            )}
          </div>
        </header>

        <div
          className="conversationScrollport"
          ref={transcriptRef}
          data-conversation-scroll
          data-view={activeView}
          onScroll={(event) => {
            const node = event.currentTarget;
            const next = node.scrollHeight - node.scrollTop - node.clientHeight <=
              experience.scrolling.followThreshold;
            atBottomRef.current = next;
            setAtBottom(next);
          }}
        >
          {activeView === "trajectory" && selected ? (
            <Suspense fallback={<div className="trajectoryLoading">Loading trajectory...</div>}>
              <Trajectory
                events={snapshot.events}
                trace={snapshot.trace}
                tracePhase={snapshot.tracePhase}
                traceProblem={snapshot.traceProblem}
                hasEarlier={snapshot.historyMoreBefore}
                inspectCallID={inspectCallID}
                onInspectConsumed={() => setInspectCallID("")}
                onLoadEarlier={() => client.loadEarlierHistory()}
                onRetryTrace={() => client.refreshTrace()}
              />
            </Suspense>
          ) : <div className="transcript" ref={transcriptContentRef} aria-live="polite">
            {snapshot.problem && (
              <div className="inlineProblem">
                <AlertTriangle size={17} />
                <span>{snapshot.problem.message}</span>
                <IconButton
                  label="Reconnect"
                  icon={<RefreshCw size={15} />}
                  onClick={() => void client.start()}
                />
              </div>
            )}
            {(transcriptStart > 0 || snapshot.historyMoreBefore || transcriptPage > 0) && (
              <div className="transcriptPagination" aria-label="Transcript pagination">
                {(transcriptStart > 0 || snapshot.historyMoreBefore) && (
                  <button
                    onClick={() => {
                      const node = transcriptRef.current;
                      if (node) {
                        prependAnchorRef.current = {
                          height: node.scrollHeight,
                          top: node.scrollTop
                        };
                      }
                      if (transcriptStart > 0) {
                        setTranscriptPage((page) => page + 1);
                        return;
                      }
                      void client.loadEarlierHistory().then((loaded) => {
                        if (loaded > 0) setTranscriptPage((page) => page + 1);
                      }).catch(reportLocalError);
                    }}
                  >
                    Earlier messages
                  </button>
                )}
                {transcriptPage > 0 && (
                  <button onClick={() => setTranscriptPage((page) => page - 1)}>
                    Newer messages
                  </button>
                )}
              </div>
            )}
            {!selected ? (
              <StartupSetup
                snapshot={snapshot}
                isolation={newIsolation}
                creating={creatingSession}
                error={localError}
                credentialStatus={credentialStatus}
                onIsolationChange={setNewIsolation}
                onCredentialStatus={setCredentialStatus}
                onCreate={(patch) => void createSession(patch)}
                client={client}
              />
            ) : entries.length === 0 ? (
              <div className="emptyConversation">
                <CodeHelperWordmark hero />
                <p>{snapshot.workspaceRoot}</p>
              </div>
            ) : (
              visibleEntries.map((entry) => (
                <TranscriptItem
                  key={entry.id}
                  entry={entry}
                  client={client}
                  onError={reportLocalError}
                  onInspect={inspectTool}
                />
              ))
            )}
            {activeTurn && <TurnStatus events={snapshot.events} turnID={activeTurn} />}
          </div>}

          {activeView === "chat" && selected && !atBottom && entries.length > 0 && (
            <div className="backToBottom">
              <IconButton
                label="Back to bottom"
                icon={<ArrowDown size={17} />}
                onClick={() => {
                  const node = transcriptRef.current;
                  if (!node) return;
                  node.scrollTo({top: node.scrollHeight, behavior: "smooth"});
                  atBottomRef.current = true;
                  setAtBottom(true);
                }}
              />
            </div>
          )}

          {selected && <div className="composerSeat" data-composer-seat>
            {localError && <div className="composerError">{localError}</div>}
            {snapshot.contextResources.length > 0 && (
              <div className="contextTray" aria-label="Prompt context">
                {snapshot.contextResources.map((resource) => (
                  <span
                    className="contextItem"
                    key={`${resource.kind}:${resource.path ?? ""}:${resource.label ?? ""}:${resource.symbol?.name ?? ""}:${resource.digest}`}
                  >
                    <FileCode2 size={13} />
                    <span>{contextResourceLabel(resource)}</span>
                    <IconButton
                      label={`Remove ${contextResourceLabel(resource)} from prompt context`}
                      icon={<X size={12} />}
                      onClick={() => client.removeContext(
                        resource.kind,
                        resource.path,
                        resource.label,
                        resource.symbol?.name
                      )}
                    />
                  </span>
                ))}
              </div>
            )}
            {pendingApproval ? (
              <ApprovalComposer
                key={pendingApprovalKey}
                event={pendingApproval}
                client={client}
                activeTurn={activeTurn}
              />
            ) : pendingInput ? (
              <InputComposer
                key={pendingInputKey}
                event={pendingInput}
                client={client}
                activeTurn={activeTurn}
              />
            ) : (
              <div className="composer">
                <div className="composerInputRow">
                  <textarea
                    ref={textareaRef}
                    value={draft}
                    rows={1}
                    placeholder="Ask CodeHelper"
                    disabled={Boolean(snapshot.hydratingSessionID) || submitting}
                    onChange={(event) => setDraft(event.target.value)}
                    onKeyDown={(event) => {
                      if (event.key === "Enter" && !event.shiftKey) {
                        event.preventDefault();
                        void submit();
                      }
                    }}
                  />
                  {activeTurn ? (
                    <IconButton
                      label="Stop turn"
                      danger
                      icon={<CircleStop size={19} />}
                      onClick={() => void client.cancel(activeTurn)}
                    />
                  ) : (
                    <IconButton
                      label="Send"
                      primary
                      disabled={
                        Boolean(snapshot.hydratingSessionID) ||
                        !draft.trim() ||
                        submitting
                      }
                      icon={submitting ? <LoaderCircle className="spin" size={19} /> : <Send size={19} />}
                      onClick={() => void submit()}
                    />
                  )}
                </div>
                <div className="composerControls">
                  <div>
                    <IconButton
                      label="Add context"
                      icon={<Plus size={15} />}
                      onClick={() => {
                        setDetailOpen(true);
                        setBlankDetailRequested(true);
                      }}
                    />
                    <CompactSelect
                      label="Mode"
                      value={snapshot.profile?.profile.mode ?? "act"}
                      values={["plan", "act", "operate"]}
                      disabled={!profileMutable(snapshot, "mode")}
                      onChange={(value) => void client.updateProfile({mode: value})
                        .catch(reportLocalError)}
                    />
                    <CompactSelect
                      label="Approval"
                      value={snapshot.profile?.profile.approval_posture ?? "suggest"}
                      values={["suggest", "auto", "never"]}
                      disabled={!profileMutable(snapshot, "approval_posture")}
                      onChange={(value) => void client.updateProfile({
                        approval_posture: value
                      }).catch(reportLocalError)}
                    />
                  </div>
                  <div>
                    {modelOptions.filter((option) => !option.disabled).length > 1 ? (
                      <CompactCatalogSelect
                        label="Model"
                        value={selectedModel}
                        options={modelOptions}
                        disabled={!profileMutable(snapshot, "model")}
                        onChange={(model) => {
                          const target = snapshot.models.find(
                            (entry) =>
                              entry.provider === selectedProvider &&
                              entry.id === model
                          );
                          void client.updateProfile({
                            model,
                            reasoning_effort:
                              target?.capabilities.default_reasoning_effort ?? ""
                          }).catch(reportLocalError);
                        }}
                      />
                    ) : (
                      <output
                        className="composerValue"
                        aria-label="Model"
                        title={selectedModel}
                      >
                        {selectedModelEntry?.capabilities.display_name || selectedModel}
                      </output>
                    )}
                    {reasoningValues.length > 1 && (
                      <CompactSelect
                        label="Reasoning"
                        value={snapshot.profile?.profile.reasoning_effort ?? ""}
                        values={reasoningValues}
                        onChange={(value) => void client.updateProfile({
                          reasoning_effort: value
                        }).catch(reportLocalError)}
                      />
                    )}
                    <ContextMeter
                      receipt={latestReceipt?.data}
                      capacity={selectedModelEntry?.capabilities.context_window}
                    />
                  </div>
                </div>
              </div>
            )}
            <ComposerStats
              receipt={latestReceipt?.data}
              usage={snapshot.usage}
              toolCalls={entries.filter((entry) => entry.kind === "tool").length}
            />
          </div>}
        </div>
      </main>

      {detailVisible && (
        <aside className="detailPanel" aria-label="Session details">
          <ResizeHandle
            label="Resize details"
            edge="start"
            value={detailWidth}
            minimum={experience.layout.detailsMinimum}
            maximum={experience.layout.detailsMaximum}
            onDelta={(delta) => setDetailWidth((width) =>
              clamp(
                width - delta,
                experience.layout.detailsMinimum,
                experience.layout.detailsMaximum
              )
            )}
          />
          <div className="detailHeader">
            <h2>Session</h2>
            <IconButton
              label="Close detail panel"
              icon={<X size={16} />}
              onClick={() => setDetailOpen(false)}
            />
          </div>
          {selected && (
            <section className="detailSection">
              <h3>Lifecycle</h3>
              <div className="lifecycleActions">
                <IconButton
                  label="Rename session"
                  icon={<Pencil size={15} />}
                  disabled={sessionAction?.pending}
                  onClick={() => {
                    const title = window.prompt("Rename session", selected.title)?.trim();
                    if (title && title !== selected.title) {
                      void runSessionAction(selected, () => client.updateSession(
                        selected.session_id, selected.revision, {title}
                      ));
                    }
                  }}
                />
                <IconButton
                  label={selected.pinned ? "Unpin session" : "Pin session"}
                  icon={selected.pinned ? <PinOff size={15} /> : <Pin size={15} />}
                  disabled={sessionAction?.pending}
                  onClick={() => void runSessionAction(selected, () => client.updateSession(
                    selected.session_id, selected.revision, {pinned: !selected.pinned}
                  ))}
                />
                <IconButton
                  label={selected.archived ? "Restore session" : "Archive session"}
                  icon={<Archive size={15} />}
                  disabled={sessionAction?.pending}
                  onClick={() => {
                    if (selected.archived || window.confirm(`Archive "${selected.title}"?`)) {
                      void runSessionAction(selected, () => client.updateSession(
                        selected.session_id, selected.revision,
                        {archived: !selected.archived}
                      ));
                    }
                  }}
                />
                <IconButton
                  label="Delete session"
                  danger
                  icon={<Trash2 size={15} />}
                  disabled={sessionAction?.pending}
                  onClick={() => deleteSession(selected)}
                />
              </div>
            </section>
          )}
          <section className="detailSection">
            <h3>Changes</h3>
            <Metric
              icon={<GitCompareArrows size={16} />}
              label="Changed files"
              value={String(selected?.changed_files ?? 0)}
            />
            <Metric
              icon={<Check size={16} />}
              label="Checkpoints"
              value={String(selected?.checkpoint_count ?? 0)}
            />
            <button
              className="settingsCommand"
              disabled={!selected}
              onClick={() => void client.workspaceDiff().then(
                setWorkspaceDiff,
                reportLocalError
              )}
            >
              <GitCompareArrows size={14} /> Refresh diff
            </button>
            {workspaceDiff?.diff ? (
              <>
                <div className="artifactActions">
                  <button onClick={() => client.addGitDiffContext(workspaceDiff)}>
                    <Plus size={14} /> Add diff
                  </button>
                </div>
                <pre className="mergePreview">{workspaceDiff.diff}</pre>
              </>
            ) : workspaceDiff ? (
              <p className="artifactSummary">No workspace changes</p>
            ) : null}
            {selected?.isolation === "worktree" && (
              <div className="artifactActions">
                <button onClick={() => void client.previewMerge().catch(reportLocalError)}>
                  <GitCompareArrows size={14} /> Preview
                </button>
                <button
                  disabled={!snapshot.mergePlan}
                  onClick={() => void client.applyMerge().catch(reportLocalError)}
                >
                  <Check size={14} /> Apply
                </button>
              </div>
            )}
            {snapshot.mergePlan && (
              <pre className="mergePreview">{snapshot.mergePlan.diff}</pre>
            )}
          </section>
          <section className="detailSection">
            <h3>Activity</h3>
            <div className="activityMetrics">
              <Metric
                icon={<TerminalSquare size={16} />}
                label="Tasks"
                value={String(snapshot.tasks.length)}
              />
              <Metric
                icon={<GitFork size={16} />}
                label="Agents"
                value={String(snapshot.agents.length)}
              />
              <Metric
                icon={<FileCode2 size={16} />}
                label="Tokens"
                value={String(snapshot.usage?.total_tokens ?? selected?.total_tokens ?? 0)}
              />
            </div>
            {snapshot.tasks.length > 0 && (
              <div className="activityList" aria-label="Tasks">
                {snapshot.tasks.map((task) => (
                  <div className="activityLine" key={task.id}>
                    <span>
                      <strong>{task.kind}</strong>
                      <small>{task.id}</small>
                    </span>
                    <span>{task.state}</span>
                    {(task.failure_reason || task.reason) && (
                      <small>{task.failure_reason || task.reason}</small>
                    )}
                  </div>
                ))}
              </div>
            )}
            {snapshot.agents.length > 0 && (
              <div className="activityList" aria-label="Agents">
                {snapshot.agents.map((agent) => (
                  <div className="activityLine" key={agent.id}>
                    <span>
                      <strong>{agent.role}</strong>
                      <small>{agent.id}</small>
                    </span>
                    <span>{agent.status}</span>
                    {agent.last_message && <small>{agent.last_message}</small>}
                  </div>
                ))}
              </div>
            )}
            {snapshot.usage && (
              <dl className="usageFacts" aria-label="Usage">
                <div><dt>Turns</dt><dd>{snapshot.usage.turns}</dd></div>
                <div><dt>Calls</dt><dd>{snapshot.usage.calls}</dd></div>
                <div>
                  <dt>Cost</dt>
                  <dd>
                    {snapshot.usage.cost_known
                      ? `${snapshot.usage.cost_microunits} µ`
                      : "Unpriced"}
                  </dd>
                </div>
              </dl>
            )}
          </section>
          <section className="detailSection workspaceExplorer">
            <div className="sectionTitleRow">
              <h3>Workspace</h3>
              <IconButton
                label="Refresh diagnostics"
                disabled={!selected}
                icon={<AlertTriangle size={14} />}
                onClick={() => void client.workspaceDiagnostics().then(
                  (result) => setWorkspaceDiagnostics(result.diagnostics),
                  reportLocalError
                )}
              />
              <IconButton
                label="Browse workspace"
                icon={<FolderTree size={14} />}
                onClick={() => void client.browseWorkspace(workspacePath).then(
                  (result) => {
                    setWorkspacePath(result.path);
                    setWorkspaceEntries(result.entries);
                  },
                  reportLocalError
                )}
              />
            </div>
            <form
              className="workspaceSearch"
              onSubmit={(event) => {
                event.preventDefault();
                if (!workspaceQuery.trim()) return;
                void client.searchWorkspace(workspaceQuery).then(
                  (result) => setWorkspaceMatches(result.matches),
                  reportLocalError
                );
              }}
            >
              <FolderTree size={15} aria-hidden="true" />
              <input
                aria-label="Search workspace"
                placeholder="Search files"
                value={workspaceQuery}
                onChange={(event) => setWorkspaceQuery(event.target.value)}
              />
            </form>
            <form
              className="workspaceSearch"
              onSubmit={(event) => {
                event.preventDefault();
                if (!workspaceSymbolQuery.trim()) return;
                void client.searchWorkspaceSymbols(workspaceSymbolQuery).then(
                  (result) => setWorkspaceSymbols(result.symbols),
                  reportLocalError
                );
              }}
            >
              <Search size={15} aria-hidden="true" />
              <input
                aria-label="Search workspace symbols"
                placeholder="Search symbols"
                value={workspaceSymbolQuery}
                onChange={(event) => setWorkspaceSymbolQuery(event.target.value)}
              />
            </form>
            {workspaceEntries.length > 0 && (
              <div className="workspaceEntries">
                {workspacePath !== "." && (
                  <button
                    className="resourceMatch"
                    onClick={() => {
                      const parent = workspacePath.split("/").slice(0, -1).join("/") || ".";
                      void client.browseWorkspace(parent).then((result) => {
                        setWorkspacePath(result.path);
                        setWorkspaceEntries(result.entries);
                      }, reportLocalError);
                    }}
                  >
                    <strong>..</strong>
                  </button>
                )}
                {workspaceEntries.map((entry) => (
                  <button
                    className="resourceMatch"
                    key={entry.path}
                    onClick={() => {
                      if (entry.kind === "directory") {
                        void client.browseWorkspace(entry.path).then((result) => {
                          setWorkspacePath(result.path);
                          setWorkspaceEntries(result.entries);
                        }, reportLocalError);
                      } else {
                        void openWorkspacePath(entry.path);
                      }
                    }}
                  >
                    <strong>{entry.path}</strong>
                    <span>{entry.kind}</span>
                  </button>
                ))}
              </div>
            )}
            {workspaceMatches.map((match) => (
              <button
                className="resourceMatch"
                key={`${match.path}:${match.line}:${match.column}`}
                onClick={() => void openWorkspacePath(match.path)}
              >
                <strong>{match.path}:{match.line}</strong>
                <span>{match.preview}</span>
              </button>
            ))}
            {workspaceSymbols.map((symbol) => (
              <button
                className="resourceMatch"
                key={`${symbol.path}:${symbol.line}:${symbol.name}`}
                onClick={() => client.addSymbolContext(symbol)}
              >
                <strong>{symbol.name}</strong>
                <span>{symbol.kind} · {symbol.path}:{symbol.line}</span>
              </button>
            ))}
            {workspaceDiagnostics.map((diagnostic) => (
              <button
                className="resourceMatch"
                key={`${diagnostic.call_id}:${diagnostic.context.path}`}
                onClick={() => client.addDiagnosticsContext(diagnostic)}
              >
                <strong>{diagnostic.context.path}</strong>
                <span>
                  {diagnostic.status} · {diagnostic.context.diagnostics?.length ?? 0} diagnostics
                </span>
              </button>
            ))}
            {workspaceResource && (
              <div className="resourceViewer">
                <div className="resourceHeader">
                  <strong>{workspaceResource.path}</strong>
                  <IconButton
                    label="Add file to prompt context"
                    disabled={snapshot.contextResources.some((resource) =>
                      resource.path === workspaceResource.path &&
                      resource.kind === "file"
                    )}
                    icon={<Plus size={14} />}
                    onClick={() => client.addWorkspaceContext(workspaceResource)}
                  />
                  <IconButton
                    label="Add selection to prompt context"
                    disabled={!workspaceSelection}
                    icon={<TextSelect size={14} />}
                    onClick={() => {
                      if (workspaceSelection) {
                        client.addWorkspaceContext(
                          workspaceResource,
                          workspaceSelection
                        );
                      }
                    }}
                  />
                  <IconButton
                    label="Download resource"
                    icon={<Download size={14} />}
                    onClick={() => void downloadWorkspaceResource(workspaceResource)}
                  />
                </div>
                <textarea
                  className="resourceContent"
                  aria-label="Workspace resource content"
                  readOnly
                  spellCheck={false}
                  value={workspaceResource.content}
                  onSelect={(event) => setWorkspaceSelection(selectionRange(
                    event.currentTarget.value,
                    event.currentTarget.selectionStart,
                    event.currentTarget.selectionEnd
                  ))}
                />
              </div>
            )}
            {workspaceImage && workspaceImageURL && (
              <div className="resourceViewer">
                <div className="resourceHeader">
                  <strong>{workspaceImage.path}</strong>
                  <IconButton
                    label="Add image to prompt context"
                    disabled={snapshot.contextResources.some((resource) =>
                      resource.path === workspaceImage.path &&
                      resource.kind === "image"
                    )}
                    icon={<Plus size={14} />}
                    onClick={() => client.addImageContext(workspaceImage)}
                  />
                  <IconButton
                    label="Download image"
                    icon={<Download size={14} />}
                    onClick={() => void downloadWorkspaceResource(workspaceImage)}
                  />
                </div>
                <img
                  className="workspaceImagePreview"
                  src={workspaceImageURL}
                  alt={workspaceImage.label}
                />
              </div>
            )}
          </section>
          <section className="detailSection">
            <h3>Profile</h3>
            <ReadOnlyField
              label="Provider"
              value={selectedProviderEntry?.display_name || selectedProvider}
              detail="Runtime provider"
            />
            <SelectField
              label="Execution"
              value={snapshot.profile?.profile.execution_target ?? "local"}
              values={["local", "sandbox"]}
              disabled={!profileMutable(snapshot, "execution_target")}
              onChange={(value) => void client.updateProfile({
                execution_target: value
              }).catch(reportLocalError)}
            />
            <NumberField
              label="Max steps"
              value={snapshot.profile?.profile.max_steps ?? 0}
              disabled={!profileMutable(snapshot, "max_steps")}
              onCommit={(value) => client.updateProfile({max_steps: value})}
            />
          </section>
          {snapshot.plan && (
            <section className="detailSection">
              <h3>Plan</h3>
              <p className="artifactSummary">{snapshot.plan.body}</p>
              <div className="artifactActions">
                <button
                  disabled={!snapshot.plan.can_implement}
                  onClick={() => void client.transitionPlan("implement").catch(reportLocalError)}
                >
                  <Play size={14} /> Implement
                </button>
                <button
                  disabled={!snapshot.plan.can_autopilot}
                  onClick={() => void client.transitionPlan("autopilot").catch(reportLocalError)}
                >
                  <LoaderCircle size={14} /> Autopilot
                </button>
              </div>
            </section>
          )}
          {snapshot.checkpoints.length > 0 && (
            <section className="detailSection">
              <h3>Checkpoints</h3>
              {snapshot.checkpoints.map((checkpoint) => (
                <div className="checkpointLine" key={checkpoint.id}>
                  <span title={checkpoint.summary}>{checkpoint.summary}</span>
                  <IconButton
                    label="Restore checkpoint"
                    disabled={!checkpoint.can_restore}
                    icon={<RotateCcw size={14} />}
                    onClick={() => void client.restoreCheckpoint(checkpoint.id).catch(reportLocalError)}
                  />
                  <IconButton
                    label="Fork checkpoint"
                    disabled={!checkpoint.can_fork}
                    icon={<GitFork size={14} />}
                    onClick={() => void client.forkCheckpoint(checkpoint.id).catch(reportLocalError)}
                  />
                </div>
              ))}
            </section>
          )}
          {snapshot.extensions.length > 0 && (
            <section className="detailSection">
              <h3>Extensions</h3>
              {snapshot.extensions.map((extension) => (
                <label className="extensionLine" key={`${extension.kind}:${extension.name}`}>
                  <span>
                    <strong>{extension.name}</strong>
                    <small>{extension.kind} / {extension.health}</small>
                  </span>
                  <input
                    type="checkbox"
                    checked={extension.enabled}
                    onChange={(event) => void client.setExtensionEnabled(
                      extension.kind,
                      extension.name,
                      event.target.checked
                    ).catch(reportLocalError)}
                  />
                </label>
              ))}
            </section>
          )}
          <section className="detailSection toolList">
            <h3>Tools</h3>
            {snapshot.tools.slice(0, 20).map((tool) => (
              <label className="toolLine" key={tool.id}>
                <Wrench size={14} />
                <span>{tool.name}</span>
                <small>{tool.availability === "available"
                  ? tool.risk_level
                  : (tool.unavailable_reason ?? tool.availability)}
                </small>
                <input
                  type="checkbox"
                  checked={tool.enabled}
                  disabled={tool.availability !== "available" ||
                    !profileMutable(snapshot, "enabled_tool_ids")}
                  onChange={(event) => void client.setToolEnabled(
                    tool.id,
                    event.target.checked
                  ).catch(reportLocalError)}
                />
              </label>
            ))}
          </section>
        </aside>
      )}

      {settingsOpen && (
        <div className="settingsPopover" role="dialog" aria-label="Settings">
          <div className="popoverHeader">
            <strong>Settings</strong>
            <IconButton label="Close settings" icon={<X size={15} />} onClick={() => setSettingsOpen(false)} />
          </div>
          <div className="themeActions">
            <button onClick={() => setTheme("light")}><Sun size={15} /> Light</button>
            <button onClick={() => setTheme("dark")}><Moon size={15} /> Dark</button>
          </div>
          <CredentialSettings
            status={credentialStatus}
            onLoad={() => void client.credentialStatus().then(
              setCredentialStatus,
              reportLocalError
            )}
            onSet={(secret) => client.setKeyringCredential(secret)
              .then(setCredentialStatus)
              .catch(reportLocalError)}
            onClear={() => client.clearKeyringCredential()
              .then(setCredentialStatus)
              .catch(reportLocalError)}
            onValidate={() => client.validateCredential()
              .then(setCredentialStatus)
              .catch(reportLocalError)}
          />
          <button
            className="settingsCommand"
            onClick={() => void client.diagnostics().then(
              (value) => setDiagnostics(JSON.stringify(value, null, 2)),
              reportLocalError
            )}
          >
            <TerminalSquare size={15} /> Runtime diagnostics
          </button>
          {diagnostics && <pre className="diagnosticsOutput">{diagnostics}</pre>}
        </div>
      )}
    </div>
  );

}

function SessionRow({
  session,
  active,
  onClick,
  onRename,
  onPin,
  onArchive,
  onDelete,
  actionPending,
  actionError
}: {
  session: SessionSummary;
  active: boolean;
  onClick: () => void;
  onRename: () => void;
  onPin: () => void;
  onArchive: () => void;
  onDelete: () => void;
  actionPending: boolean;
  actionError: string;
}) {
  return (
    <div
      className="sessionRow"
      data-active={active || undefined}
      aria-busy={actionPending || undefined}
    >
      <button className="sessionSelect" onClick={onClick}>
        <span className="sessionTitle">{session.title}</span>
        <span className="sessionMeta">
          <span>{session.status.replaceAll("_", " ")}</span>
          <span>{relativeTime(session.updated_at)}</span>
        </span>
      </button>
      <div className="sessionActions">
        <IconButton
          label="Rename session"
          icon={<Pencil size={13} />}
          disabled={actionPending}
          onClick={onRename}
        />
        <IconButton
          label={session.pinned ? "Unpin session" : "Pin session"}
          icon={session.pinned ? <PinOff size={13} /> : <Pin size={13} />}
          disabled={actionPending}
          onClick={onPin}
        />
        <IconButton
          label={session.archived ? "Restore session" : "Archive session"}
          icon={<Archive size={13} />}
          disabled={actionPending}
          onClick={onArchive}
        />
        <IconButton
          label="Delete session"
          danger
          disabled={actionPending}
          icon={actionPending ? <LoaderCircle className="spin" size={13} /> : <Trash2 size={13} />}
          onClick={onDelete}
        />
      </div>
      {actionError && (
        <span className="sessionActionError" role="alert">
          <AlertTriangle size={13} aria-hidden="true" />
          <span>{actionError}</span>
        </span>
      )}
    </div>
  );
}

function sessionIsBusy(session: SessionSummary): boolean {
  return session.status === "running" ||
    session.status === "awaiting_approval" ||
    session.status === "awaiting_input";
}

const TranscriptItem = memo(function TranscriptItem({
  entry,
  client,
  onError,
  onInspect
}: {
  entry: ConversationNode;
  client: RuntimeClient;
  onError: (error: unknown) => void;
  onInspect: (callID: string) => void;
}) {
  const [open, setOpen] = useState(false);
  if (entry.kind === "user") {
    return <div className="userMessage">{entry.text}</div>;
  }
  if (entry.kind === "assistant") {
    return (
      <article className="assistantMessage">
        <ReactMarkdown
          remarkPlugins={[remarkGfm]}
          components={{
            a: ({href, children, ...properties}) => (
              <a
                {...properties}
                href={href}
                rel={isExternalURL(href) ? "noopener noreferrer" : undefined}
                target={isExternalURL(href) ? "_blank" : undefined}
              >
                {children}
              </a>
            ),
            img: ({alt}) => <span>{alt ?? "Image"}</span>
          }}
        >
          {entry.text}
        </ReactMarkdown>
      </article>
    );
  }
  if (entry.kind === "status") {
    return (
      <div className="terminalState" data-failed={entry.failed || undefined}>
        {entry.failed ? <AlertTriangle size={16} /> : <Check size={16} />}
        <div><strong>{entry.title}</strong><span>{entry.text}</span></div>
        {entry.recoverable && entry.turnID && (
          <div className="artifactActions">
            <button onClick={() => void client.recoverTurn(
              entry.turnID,
              "retry"
            ).catch(onError)}>
              <RotateCcw size={13} /> Retry
            </button>
            <button onClick={() => {
              const guidance = window.prompt("Continue with guidance", "") ?? "";
              void client.recoverTurn(
                entry.turnID,
                "continue",
                guidance
              ).catch(onError);
            }}>
              <Play size={13} /> Continue
            </button>
          </div>
        )}
      </div>
    );
  }
  if (entry.kind === "receipt") {
    return <ReceiptLine data={entry.data} />;
  }
  if (entry.kind === "context") {
    return (
      <div className="disclosure contextDisclosure">
        <button onClick={() => setOpen((value) => !value)} aria-expanded={open}>
          {open ? <ChevronDown size={15} /> : <ChevronRight size={15} />}
          <FileCode2 size={15} />
          <span className="disclosureTitle">{entry.title}</span>
          <small>{entry.summary}</small>
        </button>
        {open && <pre>{pretty(entry.data)}</pre>}
      </div>
    );
  }
  if (entry.kind === "reasoning") {
    return (
      <div className="disclosure reasoningDisclosure" data-running={entry.running || undefined}>
        <button onClick={() => setOpen((value) => !value)} aria-expanded={open}>
          {open ? <ChevronDown size={15} /> : <ChevronRight size={15} />}
          <FileCode2 size={15} />
          <span className="disclosureTitle">Reasoning</span>
          <small>{entry.summary}</small>
        </button>
        {open && <pre>{entry.text}</pre>}
      </div>
    );
  }
  return (
    <div
      className="disclosure toolDisclosure"
      data-failed={entry.state === "failed" || undefined}
      data-running={entry.state === "running" || undefined}
      data-call-id={entry.callID}
    >
      <button onClick={() => setOpen((value) => !value)} aria-expanded={open}>
        {open ? <ChevronDown size={15} /> : <ChevronRight size={15} />}
        <TerminalSquare size={15} />
        <span className="disclosureTitle">{entry.title}</span>
        <small>{entry.errorSummary || entry.summary}</small>
        <span className="disclosureState">{entry.state}</span>
      </button>
      {open && (
        <>
          <div className="toolBody">
            <section>
              <strong>Input</strong>
              <pre>{pretty(entry.arguments)}</pre>
            </section>
            {entry.output && (
              <section>
                <strong>Output</strong>
                <pre>{entry.output}</pre>
              </section>
            )}
          </div>
          {entry.contextText && (
            <div className="artifactActions">
              <button onClick={() => onInspect(entry.callID)}>
                <ScanSearch size={13} /> Inspect
              </button>
              <button
                onClick={() => void client.addTerminalContext(
                  entry.callID,
                  entry.contextText ?? ""
                ).catch(onError)}
              >
                <Plus size={13} /> Add tool output to prompt context
              </button>
            </div>
          )}
        </>
      )}
    </div>
  );
});

function ReceiptLine({data}: {data: Readonly<Record<string, unknown>>}) {
  const latency = isObject(data.latency) ? data.latency : undefined;
  const total = numberValue(latency?.total_ms ?? data.latency_ms);
  const tools = isObject(data.tool_execution)
    ? Object.values(data.tool_execution).reduce<number>(
      (sum, value) => sum + numberValue(value),
      0
    )
    : 0;
  return (
    <dl className="receiptLine" aria-label="Turn receipt">
      <div><dt>Result</dt><dd>{String(data.outcome ?? "recorded")}</dd></div>
      {total > 0 && <div><dt>Total</dt><dd>{formatDuration(total)}</dd></div>}
      {tools > 0 && <div><dt>Tools</dt><dd>{tools}</dd></div>}
      <div>
        <dt>Tokens</dt>
        <dd>{numberValue(data.input_tokens) + numberValue(data.output_tokens)}</dd>
      </div>
      <div>
        <dt>Cost</dt>
        <dd>{data.cost_known ? `${numberValue(data.cost_microunits)} µ` : "Unpriced"}</dd>
      </div>
    </dl>
  );
}

function TurnStatus({
  events,
  turnID
}: {
  events: readonly RuntimeEvent[];
  turnID: string;
}) {
  const startedAt = useMemo(() => {
    const value = events.find(
      (event) => event.turn_id === turnID && event.kind === "turn.started"
    )?.created_at;
    const parsed = value ? Date.parse(value) : Number.NaN;
    return Number.isFinite(parsed) ? parsed : Date.now();
  }, [events, turnID]);
  const [elapsed, setElapsed] = useState(() => Math.max(0, Date.now() - startedAt));
  useEffect(() => {
    const tick = () => setElapsed(Math.max(0, Date.now() - startedAt));
    tick();
    const interval = window.setInterval(tick, 1_000);
    return () => window.clearInterval(interval);
  }, [startedAt]);
  return (
    <div className="turnStatus" role="status" aria-live="polite">
      <span>Deep diving...</span>
      {elapsed >= 15_000 && <small>{formatDuration(elapsed)}</small>}
    </div>
  );
}

function ApprovalComposer({
  event,
  client,
  activeTurn
}: {
  event: RuntimeEvent;
  client: RuntimeClient;
  activeTurn: string;
}) {
  const [scope, setScope] = useState("");
  const [replacement, setReplacement] = useState("");
  const [replacementOpen, setReplacementOpen] = useState(false);
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState("");
  const data = event.data;
  const requestID = String(data.request_id ?? "");
  const planID = typeof data.edit_plan === "object" && data.edit_plan
    ? String((data.edit_plan as Record<string, unknown>).id ?? "")
    : "";
  const scopes = Array.isArray(data.allowed_scopes)
    ? data.allowed_scopes.map(String)
    : [];
  const replacementAllowed = Boolean(data.replacement_allowed);
  const replacementArguments = parseJSONObject(replacement);
  const replacementValid = !replacement.trim() || replacementArguments !== undefined;
  const decide = async (decision: "approve" | "deny" | "cancel") => {
    if (submitting || (decision === "approve" && !replacementValid)) return;
    setSubmitting(true);
    setError("");
    try {
      await client.decideApproval(
        requestID,
        decision,
        planID,
        scope,
        replacementArguments
      );
    } catch (value) {
      setError(value instanceof Error ? value.message : String(value));
      setSubmitting(false);
    }
  };
  return (
    <div className="pendingComposer">
      <div className="pendingText">
        <div className="pendingHeading">
          <strong>{String(data.tool ?? "Action")} requires approval</strong>
          <span>{String(data.effect ?? data.risk ?? "Review the requested effect.")}</span>
        </div>
        <div className="pendingMeta">
          {planID && <span>Plan {planID.slice(0, 12)}</span>}
          {typeof data.expires_at === "string" && (
            <span>Expires {new Date(data.expires_at).toLocaleString()}</span>
          )}
        </div>
        {error && <span className="composerError">{error}</span>}
      </div>
      <div className="pendingActions">
        <IconButton
          label="Stop turn"
          danger
          disabled={submitting}
          icon={<CircleStop size={17} />}
          onClick={() => void client.cancel(activeTurn || event.turn_id)}
        />
        <button disabled={submitting} onClick={() => void decide("cancel")}>
          Cancel
        </button>
        <button disabled={submitting} onClick={() => void decide("deny")}>
          Deny
        </button>
        {scopes.length > 0 && (
          <select
            aria-label="Approval scope"
            value={scope}
            disabled={submitting}
            onChange={(event) => setScope(event.target.value)}
          >
            <option value="">Once</option>
            {scopes.map((value) => (
              <option value={value} key={value}>{value}</option>
            ))}
          </select>
        )}
        {replacementAllowed && (
          <IconButton
            label={replacementOpen
              ? "Hide replacement arguments"
              : "Edit replacement arguments"}
            icon={<Braces size={15} />}
            expanded={replacementOpen}
            disabled={submitting}
            onClick={() => setReplacementOpen((value) => !value)}
          />
        )}
        <button
          className="primaryText"
          disabled={submitting || !replacementValid}
          onClick={() => void decide("approve")}
        >
          Approve
        </button>
      </div>
      {replacementAllowed && replacementOpen && (
        <label className="replacementEditor">
          <span>Replacement arguments (JSON)</span>
          <textarea
            aria-label="Replacement arguments"
            placeholder={'{"argument": "value"}'}
            value={replacement}
            aria-invalid={!replacementValid}
            disabled={submitting}
            onChange={(event) => setReplacement(event.target.value)}
          />
          {!replacementValid && (
            <span className="composerError">Enter a JSON object.</span>
          )}
        </label>
      )}
    </div>
  );
}

function InputComposer({
  event,
  client,
  activeTurn
}: {
  event: RuntimeEvent;
  client: RuntimeClient;
  activeTurn: string;
}) {
  const [answer, setAnswer] = useState("");
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState("");
  const requestID = String(event.data.request_id ?? "");
  const options = Array.isArray(event.data.options)
    ? event.data.options.map(String)
    : [];
  const submit = async () => {
    const value = answer.trim();
    if (!value || submitting) return;
    setSubmitting(true);
    setError("");
    try {
      await client.replyInput(
        requestID,
        value,
        options.includes(value) ? {selection: value} : undefined
      );
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : String(reason));
      setSubmitting(false);
    }
  };
  return (
    <div className="pendingComposer inputComposer">
      <strong>{String(event.data.prompt ?? "Input required")}</strong>
      {error && <span className="composerError">{error}</span>}
      {options.length > 0 && (
        <select
          aria-label="Input options"
          value={answer}
          disabled={submitting}
          onChange={(event) => setAnswer(event.target.value)}
        >
          <option value="">Select an option</option>
          {options.map((option) => (
            <option value={option} key={option}>{option}</option>
          ))}
        </select>
      )}
      <input
        aria-label="Input answer"
        value={answer}
        autoFocus
        disabled={submitting}
        onChange={(value) => setAnswer(value.target.value)}
      />
      <IconButton
        label="Stop turn"
        danger
        disabled={submitting}
        icon={<CircleStop size={17} />}
        onClick={() => void client.cancel(activeTurn || event.turn_id)}
      />
      <button
        className="primaryText"
        disabled={!answer.trim() || submitting}
        onClick={() => void submit()}
      >
        Submit
      </button>
    </div>
  );
}

function StartupSetup({
  snapshot,
  isolation,
  creating,
  error,
  credentialStatus,
  onIsolationChange,
  onCredentialStatus,
  onCreate,
  client
}: {
  snapshot: RuntimeSnapshot;
  isolation: "shared" | "worktree";
  creating: boolean;
  error: string;
  credentialStatus?: CredentialStatus;
  onIsolationChange: (value: "shared" | "worktree") => void;
  onCredentialStatus: (status: CredentialStatus) => void;
  onCreate: (profile: Record<string, unknown>) => void;
  client: RuntimeClient;
}) {
  const provider = snapshot.providers.find((entry) => entry.selected) ??
    snapshot.providers.find((entry) => entry.availability === "available");
  const models = useMemo(
    () => snapshot.models.filter(
      (entry) =>
        entry.provider === provider?.id &&
        entry.capabilities.availability === "available"
    ),
    [provider?.id, snapshot.models]
  );
  const defaultModel = models.find((entry) => entry.selected) ?? models[0];
  const [modelID, setModelID] = useState(defaultModel?.id ?? "");
  const [reasoning, setReasoning] = useState("");
  const [secret, setSecret] = useState("");
  const [credentialBusy, setCredentialBusy] = useState(false);
  const [credentialError, setCredentialError] = useState("");

  useEffect(() => {
    setModelID((current) =>
      models.some((entry) => entry.id === current)
        ? current
        : (defaultModel?.id ?? "")
    );
  }, [defaultModel?.id, models]);

  useEffect(() => {
    setReasoning("");
  }, [modelID]);

  useEffect(() => {
    let active = true;
    void client.credentialStatus().then(
      (status) => {
        if (active) onCredentialStatus(status);
      },
      (loadError) => {
        if (active) {
          setCredentialError(
            loadError instanceof Error ? loadError.message : String(loadError)
          );
        }
      }
    );
    return () => {
      active = false;
    };
  }, [client, onCredentialStatus]);

  const model = models.find((entry) => entry.id === modelID) ?? defaultModel;
  const reasoningOptions = model?.capabilities.reasoning_efforts ?? [];
  const keyError = apiKeyError(secret);
  const credentialRequired = Boolean(credentialStatus?.reference.kind);
  const credentialState = credentialStatus === undefined
    ? "Checking"
    : credentialStatus.configured
      ? credentialStatus.validation === "valid" ? "Validated" : "Configured"
      : credentialRequired ? "Missing" : "Not required";

  const saveCredential = async () => {
    if (!secret || keyError || credentialBusy) return;
    setCredentialBusy(true);
    setCredentialError("");
    try {
      const status = await client.setKeyringCredential(secret);
      onCredentialStatus(status);
      setSecret("");
    } catch (saveError) {
      setCredentialError(
        saveError instanceof Error ? saveError.message : String(saveError)
      );
    } finally {
      setCredentialBusy(false);
    }
  };

  const validateCredential = async () => {
    if (credentialBusy) return;
    setCredentialBusy(true);
    setCredentialError("");
    try {
      onCredentialStatus(await client.validateCredential());
    } catch (validationError) {
      setCredentialError(
        validationError instanceof Error
          ? validationError.message
          : String(validationError)
      );
    } finally {
      setCredentialBusy(false);
    }
  };

  const create = () => {
    if (!provider || !model) return;
    const profile: Record<string, unknown> = {};
    if (models.length > 1 && model.id !== defaultModel?.id) {
      profile.model = model.id;
      profile.reasoning_effort = reasoning;
    } else if (reasoning) {
      profile.reasoning_effort = reasoning;
    }
    onCreate(profile);
  };

  return (
    <section className="startupSetup" aria-labelledby="startup-title">
      <div className="startupHeading">
        <div className="emptyMark"><CapybaraMark size="hero" /></div>
        <div>
          <h2 id="startup-title">Start a new session</h2>
          <p>Confirm the model route and credential before you begin.</p>
        </div>
      </div>

      <div className="startupSection">
        <div className="startupSectionHeading">
          <span>1</span>
          <strong>Model</strong>
        </div>
        <div className="startupFields">
          <ReadOnlyField
            label="Provider"
            value={provider?.display_name || "Unavailable"}
            detail="Runtime provider"
          />
          {models.length > 1 ? (
            <CatalogSelectField
              label="Model"
              value={modelID}
              options={models.map((entry) => ({
                value: entry.id,
                label: entry.capabilities.display_name || entry.id,
                detail: modelCapabilityLabel(entry.capabilities)
              }))}
              onChange={setModelID}
            />
          ) : (
            <ReadOnlyField
              label="Model"
              value={model?.capabilities.display_name || model?.id || "Unavailable"}
              detail={models.length === 1
                ? "Only model available"
                : "No routable model"}
            />
          )}
          {reasoningOptions.length > 0 ? (
            <SelectField
              label="Reasoning"
              value={reasoning}
              values={["", ...reasoningOptions]}
              onChange={setReasoning}
            />
          ) : (
            <ReadOnlyField
              label="Reasoning"
              value="Not supported"
            />
          )}
        </div>
      </div>

      <div className="startupSection">
        <div className="startupSectionHeading">
          <span>2</span>
          <strong>API credential</strong>
          <small data-ready={credentialStatus?.configured || !credentialRequired}>
            {credentialState}
          </small>
        </div>
        {credentialRequired ? (
          <>
            <div className="startupCredential">
              <KeyRound size={16} aria-hidden="true" />
              <input
                type="password"
                autoComplete="off"
                aria-label="API key"
                autoFocus={!credentialStatus?.configured}
                placeholder={credentialStatus?.configured
                  ? "Enter a new API key"
                  : "Enter API key"}
                value={secret}
                disabled={credentialBusy}
                onChange={(event) => setSecret(event.target.value)}
              />
              <button
                disabled={!secret || Boolean(keyError) || credentialBusy}
                onClick={() => void saveCredential()}
              >
                {credentialBusy ? "Saving..." : "Save key"}
              </button>
              <button
                disabled={!credentialStatus?.configured || credentialBusy}
                onClick={() => void validateCredential()}
              >
                Validate
              </button>
            </div>
            {keyError && <p className="startupError">{keyError}</p>}
            {credentialError && <p className="startupError">{credentialError}</p>}
            {credentialStatus?.validation_detail && (
              <p className="startupError">
                {credentialStatus.validation_detail}
              </p>
            )}
          </>
        ) : (
          <p className="startupNote">This provider does not require an API key.</p>
        )}
      </div>

      <div className="startupFooter">
        <label>
          <span>Session workspace</span>
          <select
            aria-label="Session workspace isolation"
            value={isolation}
            onChange={(event) => onIsolationChange(
              event.target.value as "shared" | "worktree"
            )}
          >
            <option value="shared">Shared</option>
            <option value="worktree">Worktree</option>
          </select>
        </label>
        <button
          className="startupCreate"
          disabled={!provider || !model || creating}
          onClick={create}
        >
          {creating
            ? <LoaderCircle className="spin" size={17} />
            : <Plus size={17} />}
          <span>{creating ? "Creating..." : "Create session"}</span>
        </button>
      </div>
      {error && <p className="startupError">{error}</p>}
    </section>
  );
}

function apiKeyError(value: string): string {
  if (!value) return "";
  if (value.trim() !== value || !/^[\x21-\x7e]+$/.test(value)) {
    return "Enter the API key only, without spaces or quotes.";
  }
  if (/^[A-Z][A-Z0-9_]*=[^=]/.test(value) ||
      (/^([\"'`]).*\1$/.test(value))) {
    return "Enter the API key value, not an environment assignment.";
  }
  return "";
}

function CredentialSettings({
  status,
  onLoad,
  onSet,
  onClear,
  onValidate
}: {
  status?: CredentialStatus;
  onLoad: () => void;
  onSet: (secret: string) => Promise<unknown>;
  onClear: () => Promise<unknown>;
  onValidate: () => Promise<unknown>;
}) {
  const [secret, setSecret] = useState("");
  useEffect(() => onLoad(), []);
  const keyring = status?.reference.kind === "keyring";
  return (
    <section className="credentialSettings">
      <div className="credentialHeading">
        <KeyRound size={15} />
        <strong>Credential</strong>
        <span>{status?.configured ? "Configured" : "Missing"}</span>
      </div>
      <small>{status?.reference.kind || "none"} · {status?.validation ?? "not_validated"}</small>
      {status?.validation_detail && <small>{status.validation_detail}</small>}
      {status?.restart_required && <small>Restart required</small>}
      <input
        type="password"
        autoComplete="off"
        aria-label="Provider credential"
        placeholder="API key"
        value={secret}
        onChange={(event) => setSecret(event.target.value)}
      />
      <div className="credentialActions">
        <button
          disabled={!secret.trim()}
          onClick={() => void onSet(secret).then(() => setSecret(""))}
        >
          Set key
        </button>
        <button disabled={!status?.configured} onClick={() => void onValidate()}>
          Validate
        </button>
        {keyring && (
          <button disabled={!status?.configured} onClick={() => void onClear()}>
            Clear
          </button>
        )}
      </div>
    </section>
  );
}

function Metric({icon, label, value}: {icon: React.ReactNode; label: string; value: string}) {
  return <div className="metric">{icon}<span>{label}</span><strong>{value}</strong></div>;
}

function CompactSelect({
  label,
  value,
  values,
  disabled,
  onChange
}: {
  label: string;
  value: string;
  values: string[];
  disabled?: boolean;
  onChange: (value: string) => void;
}) {
  return (
    <label className="compactSelect" title={`${label}: ${value || "Default"}`}>
      <span className="srOnly">{label}</span>
      <select
        aria-label={label}
        value={value}
        disabled={disabled}
        onChange={(event) => onChange(event.target.value)}
      >
        {values.map((item) => (
          <option value={item} key={item}>{item || "Default"}</option>
        ))}
      </select>
    </label>
  );
}

function CompactCatalogSelect({
  label,
  value,
  options,
  disabled,
  onChange
}: {
  label: string;
  value: string;
  options: CatalogOption[];
  disabled?: boolean;
  onChange: (value: string) => void;
}) {
  return (
    <label className="compactSelect" title={`${label}: ${value}`}>
      <span className="srOnly">{label}</span>
      <select
        aria-label={label}
        value={value}
        disabled={disabled}
        onChange={(event) => onChange(event.target.value)}
      >
        {options.map((option) => (
          <option
            value={option.value}
            key={option.value}
            disabled={option.disabled}
          >
            {option.label}
          </option>
        ))}
      </select>
    </label>
  );
}

function ContextMeter({
  receipt,
  capacity
}: {
  receipt?: Readonly<Record<string, unknown>>;
  capacity?: number;
}) {
  const budget = isObject(receipt?.context_budget)
    ? receipt.context_budget
    : undefined;
  const used = numberValue(budget?.active_tokens);
  const maximum = numberValue(budget?.max_context_tokens) || capacity || 0;
  if (used <= 0 || maximum <= 0) return null;
  const share = Math.min(1, used / maximum);
  return (
    <span
      className="contextMeter"
      title={`Context ${used.toLocaleString()} / ${maximum.toLocaleString()} tokens`}
      aria-label={`Context ${Math.round(share * 100)} percent`}
    >
      <span style={{"--ch-context-share": share} as React.CSSProperties} />
    </span>
  );
}

function ComposerStats({
  receipt,
  usage,
  toolCalls
}: {
  receipt?: Readonly<Record<string, unknown>>;
  usage?: RuntimeSnapshot["usage"];
  toolCalls: number;
}) {
  if (!receipt && (!usage || usage.turns === 0)) {
    return <div className="composerMeta" />;
  }
  const latency = isObject(receipt?.latency) ? receipt.latency : undefined;
  const input = numberValue(receipt?.input_tokens);
  const output = numberValue(receipt?.output_tokens);
  const reasoning = numberValue(receipt?.reasoning_tokens);
  const cached = numberValue(receipt?.cached_tokens);
  const totalTokens = input + output + reasoning || usage?.total_tokens || 0;
  const cacheShare = input > 0 && cached > 0
    ? `${Math.round(cached / input * 100)}% cache`
    : "";
  const values = [
    `${numberValue(usage?.turns) || (receipt ? 1 : 0)} turn`,
    `${toolCalls} tools`,
    numberValue(latency?.total_ms) > 0
      ? formatDuration(numberValue(latency?.total_ms))
      : "",
    numberValue(latency?.provider_ms) > 0
      ? `${formatDuration(numberValue(latency?.provider_ms))} model`
      : "",
    numberValue(latency?.tool_ms) > 0
      ? `${formatDuration(numberValue(latency?.tool_ms))} tools`
      : "",
    latency?.first_token_ms !== undefined
      ? `${formatDuration(numberValue(latency.first_token_ms))} TTFT`
      : "",
    input > 0 ? `${input.toLocaleString()} in` : "",
    output > 0 ? `${output.toLocaleString()} out` : "",
    reasoning > 0 ? `${reasoning.toLocaleString()} reasoning` : "",
    cached > 0 ? `${cached.toLocaleString()} cached` : "",
    totalTokens > 0 ? `${totalTokens.toLocaleString()} tokens` : "",
    cacheShare,
    receipt?.cost_known === false || usage?.cost_known === false
      ? "Unpriced"
      : `${numberValue(receipt?.cost_microunits ?? usage?.cost_microunits)} µ`
  ].filter(Boolean);
  return (
    <div className="composerMeta" aria-label="Run statistics" title={values.join(" · ")}>
      {values.map((value) => <span key={value}>{value}</span>)}
    </div>
  );
}

function SelectField({
  label,
  value,
  values,
  disabled,
  onChange
}: {
  label: string;
  value: string;
  values: string[];
  disabled?: boolean;
  onChange: (value: string) => void;
}) {
  return (
    <label className="selectField">
      <span>{label}</span>
      <select
        value={value}
        disabled={disabled}
        onChange={(event) => onChange(event.target.value)}
      >
        {values.map((item) => <option value={item} key={item}>{item || "Default"}</option>)}
      </select>
    </label>
  );
}

function ReadOnlyField({
  label,
  value,
  detail
}: {
  label: string;
  value: string;
  detail?: string;
}) {
  return (
    <div className="selectField readOnlyField">
      <span>{label}</span>
      <output aria-label={label}>{value}</output>
      {detail && <small>{detail}</small>}
    </div>
  );
}

interface CatalogOption {
  value: string;
  label: string;
  disabled?: boolean;
  reason?: string;
  detail?: string;
}

function CatalogSelectField({
  label,
  value,
  options,
  disabled,
  onChange
}: {
  label: string;
  value: string;
  options: CatalogOption[];
  disabled?: boolean;
  onChange: (value: string) => void;
}) {
  return (
    <label className="selectField">
      <span>{label}</span>
      <select
        value={value}
        disabled={disabled || options.length === 0}
        onChange={(event) => onChange(event.target.value)}
      >
        {options.map((option) => (
          <option
            value={option.value}
            key={option.value}
            disabled={option.disabled}
            title={option.reason}
          >
            {option.label}
            {option.detail ? ` - ${option.detail}` : ""}
            {option.reason ? ` (${option.reason})` : ""}
          </option>
        ))}
      </select>
    </label>
  );
}

function NumberField({
  label,
  value,
  disabled,
  onCommit
}: {
  label: string;
  value: number;
  disabled?: boolean;
  onCommit: (value: number) => Promise<unknown>;
}) {
  return (
    <label className="selectField">
      <span>{label}</span>
      <input
        type="number"
        min={1}
        value={value}
        disabled={disabled}
        onChange={(event) => void onCommit(Number(event.target.value))}
      />
    </label>
  );
}

function profileMutable(snapshot: RuntimeSnapshot, field: string): boolean {
  return snapshot.profile?.capabilities.mutable_fields.includes(field) ?? false;
}

function modelCapabilityLabel(
  capabilities: RuntimeSnapshot["models"][number]["capabilities"]
): string {
  const values = [];
  if (capabilities.reasoning) values.push("Reasoning");
  if (capabilities.tool_calls) values.push("Tools");
  if (capabilities.image_input || capabilities.vision) values.push("Vision");
  return values.join(", ");
}

function contextResourceLabel(
  resource: RuntimeSnapshot["contextResources"][number]
): string {
  if (resource.kind === "git_diff") {
    return resource.label ?? "Workspace diff";
  }
  if (resource.kind === "symbol" && resource.symbol) {
    return `${resource.symbol.name} · ${resource.path ?? "symbol"}:${
      (resource.range?.start.line ?? 0) + 1
    }`;
  }
  if (resource.kind === "diagnostics") {
    return `${resource.path ?? "Diagnostics"} · ${
      resource.diagnostics?.length ?? 0
    } diagnostics`;
  }
  if (resource.kind === "image") {
    return resource.label ?? resource.path ?? "Image";
  }
  if (resource.kind !== "selection" || !resource.range) {
    return resource.path ?? resource.label ?? resource.kind;
  }
  return `${resource.path}:${resource.range.start.line + 1}:` +
    `${resource.range.start.character + 1}-${resource.range.end.line + 1}:` +
    `${resource.range.end.character + 1}`;
}

function isWorkspaceImagePath(path: string): boolean {
  return /\.(?:png|jpe?g|gif|webp)$/i.test(path);
}

export function selectionRange(
  content: string,
  startOffset: number,
  endOffset: number
): EditorRange | undefined {
  const start = Math.max(0, Math.min(startOffset, content.length));
  const end = Math.max(start, Math.min(endOffset, content.length));
  if (start === end) return undefined;
  return {
    start: positionAt(content, start),
    end: positionAt(content, end)
  };
}

function positionAt(content: string, offset: number) {
  const prefix = content.slice(0, offset);
  const lastNewline = prefix.lastIndexOf("\n");
  return {
    line: prefix.split("\n").length - 1,
    character: lastNewline < 0 ? prefix.length : prefix.length - lastNewline - 1
  };
}

function parseJSONObject(value: string): Record<string, unknown> | undefined {
  if (!value.trim()) return undefined;
  try {
    const parsed = JSON.parse(value) as unknown;
    if (parsed && typeof parsed === "object" && !Array.isArray(parsed)) {
      return parsed as Record<string, unknown>;
    }
  } catch {
    return undefined;
  }
  return undefined;
}

function ResizeHandle({
  label,
  edge = "end",
  value,
  minimum,
  maximum,
  onDelta
}: {
  label: string;
  edge?: "start" | "end";
  value: number;
  minimum: number;
  maximum: number;
  onDelta: (delta: number) => void;
}) {
  const lastX = useRef(0);
  const pendingDelta = useRef(0);
  const frame = useRef<number>();
  const flush = () => {
    frame.current = undefined;
    if (pendingDelta.current === 0) return;
    const delta = pendingDelta.current;
    pendingDelta.current = 0;
    onDelta(delta);
  };
  return (
    <div
      className="resizeHandle"
      data-edge={edge}
      role="separator"
      aria-label={label}
      aria-orientation="vertical"
      aria-valuemin={minimum}
      aria-valuemax={maximum}
      aria-valuenow={value}
      tabIndex={0}
      onKeyDown={(event) => {
        if (event.key === "ArrowLeft") onDelta(-16);
        if (event.key === "ArrowRight") onDelta(16);
      }}
      onPointerDown={(event) => {
        lastX.current = event.clientX;
        event.currentTarget.setPointerCapture(event.pointerId);
      }}
      onPointerMove={(event) => {
        if (!event.currentTarget.hasPointerCapture(event.pointerId)) return;
        pendingDelta.current += event.clientX - lastX.current;
        lastX.current = event.clientX;
        if (frame.current === undefined) {
          frame.current = requestAnimationFrame(flush);
        }
      }}
      onPointerUp={(event) => {
        if (event.currentTarget.hasPointerCapture(event.pointerId)) {
          event.currentTarget.releasePointerCapture(event.pointerId);
        }
        if (frame.current !== undefined) cancelAnimationFrame(frame.current);
        flush();
      }}
    />
  );
}

function clamp(value: number, minimum: number, maximum: number): number {
  return Math.min(maximum, Math.max(minimum, value));
}

function IconButton({
  label,
  icon,
  onClick,
  disabled,
  primary,
  danger,
  expanded
}: {
  label: string;
  icon: React.ReactNode;
  onClick: () => void;
  disabled?: boolean;
  primary?: boolean;
  danger?: boolean;
  expanded?: boolean;
}) {
  return (
    <button
      className="iconButton"
      data-primary={primary || undefined}
      data-danger={danger || undefined}
      aria-label={label}
      aria-expanded={expanded}
      title={label}
      disabled={disabled}
      onClick={onClick}
    >
      {icon}
    </button>
  );
}

function BootState({title, detail, failed}: {title: string; detail?: string; failed?: boolean}) {
  return (
    <main className="bootState" data-failed={failed || undefined}>
      <div className="bootBrand">
        <CapybaraMark size="hero" />
        <span className="bootSignal">
          {failed
            ? <AlertTriangle size={14} />
            : <LoaderCircle className="spin" size={14} />}
        </span>
      </div>
      <h1>{title}</h1>
      {detail && <p>{detail}</p>}
    </main>
  );
}

export function projectTranscript(events: readonly RuntimeEvent[]): ConversationNode[] {
  const projection = projectConversation(events);
  return projection.order.flatMap((id) => {
    const node = projection.nodes.get(id);
    return node ? [node] : [];
  });
}

function pendingRequestKey(sessionID: string, event?: RuntimeEvent): string {
  return `${sessionID}:${String(event?.data.request_id ?? "")}`;
}

function pretty(value: unknown): string {
  if (value === undefined || value === null) return "";
  if (typeof value === "string") return value;
  return JSON.stringify(value, null, 2);
}

function isObject(value: unknown): value is Record<string, unknown> {
  return Boolean(value) && typeof value === "object" && !Array.isArray(value);
}

function numberValue(value: unknown): number {
  return typeof value === "number" && Number.isFinite(value) ? value : 0;
}

function formatDuration(milliseconds: number): string {
  return milliseconds < 1_000
    ? `${Math.round(milliseconds)} ms`
    : `${(milliseconds / 1_000).toFixed(milliseconds < 10_000 ? 2 : 1)} s`;
}

function relativeTime(value: string): string {
  const delta = Date.now() - new Date(value).getTime();
  if (!Number.isFinite(delta) || delta < 60_000) return "now";
  if (delta < 3_600_000) return `${Math.floor(delta / 60_000)}m`;
  if (delta < 86_400_000) return `${Math.floor(delta / 3_600_000)}h`;
  return `${Math.floor(delta / 86_400_000)}d`;
}

function setTheme(theme: "light" | "dark") {
  localStorage.setItem("ch.theme", theme);
  document.documentElement.dataset.theme = theme;
  document.documentElement.style.colorScheme = theme;
}

function safeFilename(value: string): string {
  const safe = value.trim().replace(/[^A-Za-z0-9._-]+/g, "-").replace(/^-+|-+$/g, "");
  return safe || "codehelper-session";
}

function isExternalURL(value: string | undefined): boolean {
  return value?.startsWith("https://") === true ||
    value?.startsWith("http://") === true;
}
