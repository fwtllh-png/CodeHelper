import {
  AlertTriangle,
  Archive,
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
  PanelRightClose,
  PanelRightOpen,
  Pencil,
  Pin,
  PinOff,
  Plus,
  Play,
  RefreshCw,
  RotateCcw,
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
import {useEffect, useMemo, useRef, useState, useSyncExternalStore} from "react";
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
import {isTerminal, RuntimeClient, type RuntimeSnapshot} from "../runtime/client";

interface Props {
  client: RuntimeClient;
}

type TranscriptEntry =
  | {id: string; type: "user" | "assistant" | "reasoning"; text: string}
  | {
      id: string;
      type: "tool";
      title: string;
      text: string;
      failed: boolean;
      callID: string;
      contextText?: string;
    }
  | {
      id: string;
      type: "status";
      title: string;
      text: string;
      failed: boolean;
      turnID?: string;
    };

const transcriptPageSize = 200;

function initialDetailOpen(): boolean {
  return typeof window.matchMedia !== "function" ||
    window.matchMedia("(min-width: 1051px)").matches;
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
  const [settingsOpen, setSettingsOpen] = useState(false);
  const [submitting, setSubmitting] = useState(false);
  const [localError, setLocalError] = useState("");
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
  const selected = snapshot.sessions.find(
    (item) => item.session_id === snapshot.selectedSessionID
  );
  const entries = useMemo(() => projectTranscript(snapshot.events), [snapshot.events]);
  const transcriptEnd = Math.max(0, entries.length - transcriptPage * transcriptPageSize);
  const transcriptStart = Math.max(0, transcriptEnd - transcriptPageSize);
  const visibleEntries = entries.slice(transcriptStart, transcriptEnd);
  const pendingApproval = latestPending(snapshot.events, "approval");
  const pendingInput = latestPending(snapshot.events, "input");
  const activeTurn = latestActiveTurn(snapshot.events);
  const selectedProvider = snapshot.profile?.profile.provider ?? "";
  const selectedModel = snapshot.profile?.profile.model ?? "";
  const providerOptions = snapshot.providers.map((provider) => {
    const hasAvailableModel = snapshot.models.some(
      (model) =>
        model.provider === provider.id &&
        model.capabilities.availability === "available"
    );
    const reason = provider.reason || (!hasAvailableModel ? "No available models" : "");
    return {
      value: provider.id,
      label: provider.display_name,
      disabled: provider.availability !== "available" || !hasAvailableModel,
      reason
    };
  });
  const modelOptions = snapshot.models
    .filter((model) => model.provider === selectedProvider)
    .map((model) => ({
      value: model.id,
      label: model.capabilities.display_name || model.id,
      disabled: model.capabilities.availability !== "available",
      reason: model.capabilities.unavailable_reason,
      detail: modelCapabilityLabel(model.capabilities)
    }));

  useEffect(() => {
    setWorkspaceSelection(undefined);
  }, [workspaceResource?.digest]);

  useEffect(() => {
    setTranscriptPage(0);
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

  useEffect(() => {
    const node = transcriptRef.current;
    if (!node) return;
    const nearBottom = node.scrollHeight - node.scrollTop - node.clientHeight < 120;
    if (nearBottom && transcriptPage === 0) {
      requestAnimationFrame(() => node.scrollTo({top: node.scrollHeight}));
    }
  }, [entries.length, transcriptPage]);

  const submit = async () => {
    const prompt = draft.trim();
    if (!prompt || submitting) return;
    setSubmitting(true);
    setLocalError("");
    try {
      await client.submitPrompt(prompt);
      setDraft("");
      client.saveDraft("", snapshot.selectedSessionID);
    } catch (error) {
      setLocalError(error instanceof Error ? error.message : String(error));
    } finally {
      setSubmitting(false);
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
    <div className="app" data-detail-open={detailOpen || undefined}>
      <aside className="sessionRail" aria-label="Sessions">
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
        <div className="brandRow">
          <div className="brandMark" aria-hidden="true"><TerminalSquare size={17} /></div>
          <strong>CodeHelper</strong>
          <IconButton
            label="New chat"
            icon={<Plus size={17} />}
            onClick={() => void client.createSession(newIsolation).catch(reportLocalError)}
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
                  void client.updateSession(
                    session.session_id,
                    session.revision,
                    {title}
                  ).catch(reportLocalError);
                }
              }}
              onPin={() => void client.updateSession(
                session.session_id,
                session.revision,
                {pinned: !session.pinned}
              ).catch(reportLocalError)}
              onArchive={() => {
                if (session.archived || window.confirm(`Archive "${session.title}"?`)) {
                  void client.updateSession(
                    session.session_id,
                    session.revision,
                    {archived: !session.archived}
                  ).catch(reportLocalError);
                }
              }}
              onDelete={() => {
                if (window.confirm(`Delete "${session.title}"?`)) {
                  void client.deleteSession(
                    session.session_id,
                    session.revision
                  ).catch(reportLocalError);
                }
              }}
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
      </aside>

      <main className="conversation">
        <header className="conversationHeader">
          <div>
            <h1>{selected?.title ?? "New Chat"}</h1>
            <p>{snapshot.workspaceRoot}</p>
          </div>
          <div className="headerActions">
            {activeTurn && <span className="workingLabel">Working</span>}
            <IconButton
              label="Export session"
              disabled={!selected}
              icon={<Download size={17} />}
              onClick={() => void exportSession()}
            />
            <IconButton
              label={detailOpen ? "Close detail panel" : "Open detail panel"}
              icon={detailOpen ? <PanelRightClose size={17} /> : <PanelRightOpen size={17} />}
              onClick={() => setDetailOpen((value) => !value)}
            />
          </div>
        </header>

        <div className="transcript" ref={transcriptRef} aria-live="polite">
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
          {entries.length === 0 ? (
            <div className="emptyConversation">
              <div className="emptyMark"><TerminalSquare size={22} /></div>
              <h2>{selected ? selected.title : "What are we working on?"}</h2>
              <p>{snapshot.workspaceRoot}</p>
            </div>
          ) : (
            visibleEntries.map((entry) => (
              <TranscriptItem
                key={entry.id}
                entry={entry}
                client={client}
                onError={reportLocalError}
              />
            ))
          )}
        </div>

        <div className="composerSeat">
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
            <ApprovalComposer event={pendingApproval} client={client} activeTurn={activeTurn} />
          ) : pendingInput ? (
            <InputComposer event={pendingInput} client={client} activeTurn={activeTurn} />
          ) : (
            <div className="composer">
              <textarea
                value={draft}
                rows={1}
                placeholder={selected ? "Ask CodeHelper" : "Create a chat to begin"}
                disabled={!selected || submitting}
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
                  disabled={!selected || !draft.trim() || submitting}
                  icon={submitting ? <LoaderCircle className="spin" size={19} /> : <Send size={19} />}
                  onClick={() => void submit()}
                />
              )}
            </div>
          )}
          <div className="composerMeta">
            <span>{snapshot.profile?.profile.mode ?? "act"}</span>
            <span>{snapshot.profile?.profile.model ?? selected?.model ?? ""}</span>
          </div>
        </div>
      </main>

      {detailOpen && (
        <aside className="detailPanel" aria-label="Session details">
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
                  onClick={() => {
                    const title = window.prompt("Rename session", selected.title)?.trim();
                    if (title && title !== selected.title) {
                      void client.updateSession(
                        selected.session_id,
                        selected.revision,
                        {title}
                      ).catch(reportLocalError);
                    }
                  }}
                />
                <IconButton
                  label={selected.pinned ? "Unpin session" : "Pin session"}
                  icon={selected.pinned ? <PinOff size={15} /> : <Pin size={15} />}
                  onClick={() => void client.updateSession(
                    selected.session_id,
                    selected.revision,
                    {pinned: !selected.pinned}
                  ).catch(reportLocalError)}
                />
                <IconButton
                  label={selected.archived ? "Restore session" : "Archive session"}
                  icon={<Archive size={15} />}
                  onClick={() => {
                    if (selected.archived || window.confirm(`Archive "${selected.title}"?`)) {
                      void client.updateSession(
                        selected.session_id,
                        selected.revision,
                        {archived: !selected.archived}
                      ).catch(reportLocalError);
                    }
                  }}
                />
                <IconButton
                  label="Delete session"
                  danger
                  icon={<Trash2 size={15} />}
                  onClick={() => {
                    if (window.confirm(`Delete "${selected.title}"?`)) {
                      void client.deleteSession(
                        selected.session_id,
                        selected.revision
                      ).catch(reportLocalError);
                    }
                  }}
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
            <SelectField
              label="Mode"
              value={snapshot.profile?.profile.mode ?? "act"}
              values={["plan", "act", "operate"]}
              disabled={!profileMutable(snapshot, "mode")}
              onChange={(value) => void client.updateProfile({mode: value}).catch(reportLocalError)}
            />
            <SelectField
              label="Reasoning"
              value={snapshot.profile?.profile.reasoning_effort ?? ""}
              values={["", "minimal", "low", "medium", "high"]}
              disabled={!profileMutable(snapshot, "reasoning_effort")}
              onChange={(value) => void client.updateProfile({
                reasoning_effort: value
              }).catch(reportLocalError)}
            />
            <CatalogSelectField
              label="Provider"
              value={selectedProvider}
              options={providerOptions}
              disabled={!profileMutable(snapshot, "provider")}
              onChange={(provider) => {
                const models = snapshot.models.filter(
                  (model) =>
                    model.provider === provider &&
                    model.capabilities.availability === "available"
                );
                const model = models.find((item) => item.selected) ?? models[0];
                if (model) {
                  void client.updateProfile({
                    provider,
                    model: model.id
                  }).catch(reportLocalError);
                }
              }}
            />
            <CatalogSelectField
              label="Model"
              value={selectedModel}
              options={modelOptions}
              disabled={
                !profileMutable(snapshot, "model") ||
                modelOptions.length === 0
              }
              onChange={(model) => void client.updateProfile({
                model
              }).catch(reportLocalError)}
            />
            <SelectField
              label="Approval"
              value={snapshot.profile?.profile.approval_posture ?? "suggest"}
              values={["suggest", "auto", "never"]}
              disabled={!profileMutable(snapshot, "approval_posture")}
              onChange={(value) => void client.updateProfile({
                approval_posture: value
              }).catch(reportLocalError)}
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

  function reportLocalError(error: unknown) {
    setLocalError(error instanceof Error ? error.message : String(error));
  }
}

function SessionRow({
  session,
  active,
  onClick,
  onRename,
  onPin,
  onArchive,
  onDelete
}: {
  session: SessionSummary;
  active: boolean;
  onClick: () => void;
  onRename: () => void;
  onPin: () => void;
  onArchive: () => void;
  onDelete: () => void;
}) {
  return (
    <div className="sessionRow" data-active={active || undefined}>
      <button className="sessionSelect" onClick={onClick}>
        <span className="sessionTitle">{session.title}</span>
        <span className="sessionMeta">
          <span>{session.status.replaceAll("_", " ")}</span>
          <span>{relativeTime(session.updated_at)}</span>
        </span>
      </button>
      <div className="sessionActions">
        <IconButton label="Rename session" icon={<Pencil size={13} />} onClick={onRename} />
        <IconButton
          label={session.pinned ? "Unpin session" : "Pin session"}
          icon={session.pinned ? <PinOff size={13} /> : <Pin size={13} />}
          onClick={onPin}
        />
        <IconButton
          label={session.archived ? "Restore session" : "Archive session"}
          icon={<Archive size={13} />}
          onClick={onArchive}
        />
        <IconButton
          label="Delete session"
          danger
          icon={<Trash2 size={13} />}
          onClick={onDelete}
        />
      </div>
    </div>
  );
}

function TranscriptItem({
  entry,
  client,
  onError
}: {
  entry: TranscriptEntry;
  client: RuntimeClient;
  onError: (error: unknown) => void;
}) {
  const [open, setOpen] = useState(entry.type !== "reasoning");
  if (entry.type === "user") {
    return <div className="userMessage">{entry.text}</div>;
  }
  if (entry.type === "assistant") {
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
  if (entry.type === "status") {
    return (
      <div className="terminalState" data-failed={entry.failed || undefined}>
        {entry.failed ? <AlertTriangle size={16} /> : <Check size={16} />}
        <div><strong>{entry.title}</strong><span>{entry.text}</span></div>
        {entry.failed && entry.turnID && (
          <div className="artifactActions">
            <button onClick={() => void client.recoverTurn(
              entry.turnID ?? "",
              "retry"
            )}>
              <RotateCcw size={13} /> Retry
            </button>
            <button onClick={() => {
              const guidance = window.prompt("Continue with guidance", "") ?? "";
              void client.recoverTurn(entry.turnID ?? "", "continue", guidance);
            }}>
              <Play size={13} /> Continue
            </button>
          </div>
        )}
      </div>
    );
  }
  return (
    <div className="disclosure" data-failed={entry.type === "tool" && entry.failed || undefined}>
      <button onClick={() => setOpen((value) => !value)} aria-expanded={open}>
        {open ? <ChevronDown size={15} /> : <ChevronRight size={15} />}
        {entry.type === "tool" ? <TerminalSquare size={15} /> : <FileCode2 size={15} />}
        <span>{entry.type === "tool" ? entry.title : "Reasoning"}</span>
      </button>
      {open && (
        <>
          <pre>{entry.text}</pre>
          {entry.type === "tool" && entry.contextText && (
            <div className="artifactActions">
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
        <strong>{String(data.tool ?? "Action")} requires approval</strong>
        <span>{String(data.effect ?? data.risk ?? "Review the requested effect.")}</span>
        {planID && <span>Plan {planID.slice(0, 12)}</span>}
        {typeof data.expires_at === "string" && (
          <span>Expires {new Date(data.expires_at).toLocaleString()}</span>
        )}
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
        <button
          className="primaryText"
          disabled={submitting || !replacementValid}
          onClick={() => void decide("approve")}
        >
          Approve
        </button>
      </div>
      {replacementAllowed && (
        <textarea
          aria-label="Replacement arguments"
          placeholder="Optional replacement arguments (JSON)"
          value={replacement}
          aria-invalid={!replacementValid}
          disabled={submitting}
          onChange={(event) => setReplacement(event.target.value)}
        />
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

function TextField({
  label,
  value,
  disabled,
  onCommit
}: {
  label: string;
  value: string;
  disabled?: boolean;
  onCommit: (value: string) => Promise<unknown>;
}) {
  const [draft, setDraft] = useState(value);
  useEffect(() => setDraft(value), [value]);
  return (
    <label className="selectField">
      <span>{label}</span>
      <input
        value={draft}
        disabled={disabled}
        onChange={(event) => setDraft(event.target.value)}
        onBlur={() => {
          if (draft.trim() && draft !== value) void onCommit(draft.trim());
        }}
      />
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

function IconButton({
  label,
  icon,
  onClick,
  disabled,
  primary,
  danger
}: {
  label: string;
  icon: React.ReactNode;
  onClick: () => void;
  disabled?: boolean;
  primary?: boolean;
  danger?: boolean;
}) {
  return (
    <button
      className="iconButton"
      data-primary={primary || undefined}
      data-danger={danger || undefined}
      aria-label={label}
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
      <div className="bootMark">{failed ? <AlertTriangle size={22} /> : <LoaderCircle className="spin" size={22} />}</div>
      <h1>{title}</h1>
      {detail && <p>{detail}</p>}
    </main>
  );
}

export function projectTranscript(events: readonly RuntimeEvent[]): TranscriptEntry[] {
  const entries: TranscriptEntry[] = [];
  const output = new Map<string, TranscriptEntry & {type: "assistant"}>();
  const reasoning = new Map<string, TranscriptEntry & {type: "reasoning"}>();
  const tools = new Map<string, TranscriptEntry & {type: "tool"}>();
  for (const event of events) {
    const data = event.data;
    switch (event.kind) {
      case "turn.started":
        entries.push({
          id: event.id,
          type: "user",
          text: String(data.display_prompt ?? data.prompt ?? "")
        });
        break;
      case "output.delta": {
        let entry = output.get(event.turn_id);
        if (!entry) {
          entry = {id: `output-${event.turn_id}`, type: "assistant", text: ""};
          output.set(event.turn_id, entry);
          entries.push(entry);
        }
        entry.text += String(data.text ?? "");
        break;
      }
      case "reasoning.delta": {
        let entry = reasoning.get(event.turn_id);
        if (!entry) {
          entry = {id: `reasoning-${event.turn_id}`, type: "reasoning", text: ""};
          reasoning.set(event.turn_id, entry);
          entries.push(entry);
        }
        entry.text += String(data.text ?? "");
        break;
      }
      case "tool.start": {
        if (data.tool === "turn_complete" || data.tool === "request_user_input") {
          break;
        }
        const callID = String(data.call_id ?? event.id);
        const entry: TranscriptEntry & {type: "tool"} = {
          id: `tool-${callID}`,
          type: "tool",
          title: String(data.tool ?? "Tool"),
          text: pretty(data.arguments),
          failed: false,
          callID
        };
        tools.set(callID, entry);
        entries.push(entry);
        break;
      }
      case "tool.output": {
        const callID = String(data.call_id ?? "");
        const entry = tools.get(callID);
        if (entry) entry.text += String(data.chunk ?? "");
        break;
      }
      case "tool.result": {
        const callID = String(data.call_id ?? "");
        const entry = tools.get(callID);
        if (entry) {
          const finalOutput = String(data.output ?? "");
          if (finalOutput) entry.text = finalOutput;
          if (finalOutput) entry.contextText = finalOutput;
          entry.failed = Boolean(data.is_error);
        }
        break;
      }
      case "turn.completed": {
        const text = String(data.text ?? data.summary ?? "");
        const streamed = output.get(event.turn_id);
        if (streamed && text) {
          streamed.text = text;
        } else if (!streamed) {
          if (text) {
            entries.push({id: event.id, type: "assistant", text});
          }
        }
        entries.push({
          id: `${event.id}-status`,
          type: "status",
          title: "Completed",
          text: String(data.outcome ?? "Turn completed"),
          failed: false
        });
        break;
      }
      case "turn.verification":
        entries.push({
          id: event.id,
          type: "status",
          title: "Verification",
          text: String(data.verdict ?? data.status ?? pretty(data)),
          failed: data.verdict === "failed" || data.status === "failed"
        });
        break;
      case "turn.receipt":
        entries.push({
          id: event.id,
          type: "status",
          title: "Receipt",
          text: String(data.outcome ?? data.status ?? "Execution receipt recorded"),
          failed: false
        });
        break;
      case "operation.rejected":
        entries.push({
          id: event.id,
          type: "status",
          title: "Rejected",
          text: String(data.message ?? data.code ?? "Operation rejected"),
          failed: true
        });
        break;
      case "turn.failed":
        entries.push({
          id: event.id,
          type: "status",
          title: "Failed",
          text: String(data.message ?? data.code ?? "Turn failed"),
          failed: true,
          turnID: event.turn_id
        });
        break;
      case "turn.canceled":
        entries.push({
          id: event.id,
          type: "status",
          title: "Canceled",
          text: String(data.reason ?? "Canceled"),
          failed: true,
          turnID: event.turn_id
        });
        break;
    }
  }
  return entries;
}

function latestActiveTurn(events: readonly RuntimeEvent[]): string {
  const active = new Set<string>();
  for (const event of events) {
    if (event.kind === "turn.started") active.add(event.turn_id);
    if (isTerminal(event.kind)) active.delete(event.turn_id);
  }
  return [...active].at(-1) ?? "";
}

function latestPending(events: readonly RuntimeEvent[], kind: "approval" | "input"): RuntimeEvent | undefined {
  const pending = new Map<string, RuntimeEvent>();
  for (const event of events) {
    if (event.kind === `${kind}.required`) {
      pending.set(String(event.data.request_id ?? ""), event);
    }
    if (event.kind === `${kind}.resolved`) {
      pending.delete(String(event.data.request_id ?? ""));
    }
  }
  return [...pending.values()].at(-1);
}

function pretty(value: unknown): string {
  if (value === undefined || value === null) return "";
  if (typeof value === "string") return value;
  return JSON.stringify(value, null, 2);
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
