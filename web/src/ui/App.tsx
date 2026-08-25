import {
  AlertTriangle,
  ArrowDown,
  Archive,
  Braces,
  Check,
  ChevronDown,
  ChevronRight,
  ChevronUp,
  CircleStop,
  Download,
  FileCode2,
  FolderOpen,
  GitFork,
  LoaderCircle,
  ListPlus,
  KeyRound,
  MoreHorizontal,
  Paperclip,
  PanelLeftClose,
  PanelLeftOpen,
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
  TextSelect,
  Trash2,
  Zap,
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
  useSyncExternalStore,
  type ReactNode
} from "react";
import type {
  CredentialStatus,
  RuntimeEvent,
  SessionCheckpoint,
  SessionSummary
} from "../protocol";
import {
  projectEditPlan,
  projectConversation,
  type ConversationNode
} from "../projection/conversation";
import {RuntimeClient, type RuntimeSnapshot} from "../runtime/client";
import {CapybaraMark} from "./brand/CapybaraMark";
import {CodeHelperWordmark} from "./brand/CodeHelperWordmark";
import {
  compactSelectWidth,
  ContextMeter,
  MessageActions,
  type ContextAttribution,
  type MessageChrome,
  type MessageFeedbackRating
} from "./ConversationChrome";
import type {ComposerCommand} from "./ComposerCommandMenu";
import {experience} from "./experience";
import type {ThemeMode} from "./SettingsDialog";
import type {BackgroundActivityTarget} from "./backgroundActivity";
import {
  EditPlanPreview,
  ReasoningDisclosure,
  ToolDisclosure
} from "./TranscriptCards";
import {
  maxComposerAttachmentBytes,
  maxComposerAttachments,
  composerAttachmentAccept
} from "./attachmentLimits";
import type {
  ComposerAttachment,
  ComposerAttachmentSource
} from "./attachmentPipeline";
import {
  adjacentQuestion,
  projectConversationNavigation,
  questionPosition,
  transcriptPageForEntry,
  type ConversationNavigationItem
} from "./conversationNavigation";

export {selectionRange} from "./WorkspaceContextDialog";

interface Props {
  client: RuntimeClient;
}

const transcriptPageSize = 200;
const transcriptPageOverlap = 32;
const transcriptPageStep = transcriptPageSize - transcriptPageOverlap;
const compactCountFormat = new Intl.NumberFormat("en", {
  notation: "compact",
  maximumFractionDigits: 1
});
const Trajectory = lazy(async () => ({
  default: (await import("./Trajectory")).Trajectory
}));
const SettingsDialog = lazy(async () => ({
  default: (await import("./SettingsDialog")).SettingsDialog
}));
const WorkspaceContextDialog = lazy(async () => ({
  default: (await import("./WorkspaceContextDialog")).WorkspaceContextDialog
}));
const ComposerAttachments = lazy(async () => ({
  default: (await import("./ComposerAttachments")).ComposerAttachments
}));
const ComposerCommandMenu = lazy(async () => ({
  default: (await import("./ComposerCommandMenu")).ComposerCommandMenu
}));
const ProducedFiles = lazy(async () => ({
  default: (await import("./ProducedFiles")).ProducedFiles
}));
const SessionProgress = lazy(async () => ({
  default: (await import("./SessionProgress")).SessionProgress
}));
const TurnQueue = lazy(async () => ({
  default: (await import("./TurnQueue")).TurnQueue
}));
const BackgroundActivityMonitor = lazy(async () => ({
  default: (await import("./BackgroundActivityMonitor")).BackgroundActivityMonitor
}));
const ConversationNavigator = lazy(async () => ({
  default: (await import("./ConversationNavigator")).ConversationNavigator
}));
const MarkdownMessage = lazy(async () => ({
  default: (await import("./MarkdownMessage")).MarkdownMessage
}));

interface TranscriptReadingPosition {
  readonly entryID: string;
  readonly top: number;
  readonly scrollTop: number;
  readonly page: number;
  readonly atBottom: boolean;
}

interface TranscriptNavigationTarget {
  readonly entryID: string;
  readonly path?: string;
}

function initialRailCollapsed(): boolean {
  return readPreference("ch.sidebar.collapsed") === "true";
}

function initialSessionIsolation(): "shared" | "worktree" {
  return readPreference("ch.session.isolation") === "worktree"
    ? "worktree"
    : "shared";
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
  const [sessionSearchOpen, setSessionSearchOpen] = useState(false);
  const [workspaceExpanded, setWorkspaceExpanded] = useState(true);
  const [draft, setDraft] = useState("");
  const [draftOwner, setDraftOwner] = useState("");
  const [contextOpen, setContextOpen] = useState(false);
  const [railCollapsed, setRailCollapsed] = useState(initialRailCollapsed);
  const [railWidth, setRailWidth] = useState(
    () => storedPanelWidth(
      "ch.sidebar.width",
      experience.layout.sidebarDefault
    )
  );
  const [activeView, setActiveView] = useState<"chat" | "trajectory">("chat");
  const [inspectCallID, setInspectCallID] = useState("");
  const [settingsOpen, setSettingsOpen] = useState(false);
  const [themeMode, setThemeMode] = useState<ThemeMode>(readThemeMode);
  const [activityTarget, setActivityTarget] =
    useState<BackgroundActivityTarget>();
  const [submitting, setSubmitting] = useState(false);
  const [creatingSession, setCreatingSession] = useState(false);
  const [localError, setLocalError] = useState("");
  const [composerAttachments, setComposerAttachments] =
    useState<ComposerAttachment[]>([]);
  const [draggingAttachment, setDraggingAttachment] = useState(false);
  const [commandMenuOpen, setCommandMenuOpen] = useState(false);
  const [commandQuery, setCommandQuery] = useState("");
  const [commandMenuSource, setCommandMenuSource] =
    useState<"button" | "slash">("button");
  const [sessionAction, setSessionAction] = useState<{
    sessionID: string;
    pending: boolean;
    error: string;
  }>();
  const [newIsolation, setNewIsolation] =
    useState<"shared" | "worktree">(initialSessionIsolation);
  const [credentialStatus, setCredentialStatus] = useState<CredentialStatus>();
  const [transcriptPage, setTranscriptPage] = useState(0);
  const [conversationNavigatorOpen, setConversationNavigatorOpen] =
    useState(false);
  const [readerEntryID, setReaderEntryID] = useState("");
  const [navigationHighlightID, setNavigationHighlightID] = useState("");
  const [navigationTarget, setNavigationTarget] =
    useState<TranscriptNavigationTarget>();
  const transcriptRef = useRef<HTMLDivElement>(null);
  const transcriptContentRef = useRef<HTMLDivElement>(null);
  const readingPositionsRef =
    useRef(new Map<string, TranscriptReadingPosition>());
  const pendingReadingRestoreRef = useRef<TranscriptReadingPosition>();
  const readerFrameRef = useRef<number>();
  const navigationReaderLockRef = useRef("");
  const navigationReaderLockTimerRef = useRef<number>();
  const navigationHighlightTimerRef = useRef<number>();
  const atBottomRef = useRef(true);
  const [atBottom, setAtBottom] = useState(true);
  const textareaRef = useRef<HTMLTextAreaElement>(null);
  const attachmentInputRef = useRef<HTMLInputElement>(null);
  const composingRef = useRef(false);
  const attachmentGenerationRef = useRef(0);
  const removedAttachmentIDs = useRef(new Set<string>());
  const attachmentsRef = useRef(composerAttachments);
  const selectedSessionRef = useRef(snapshot.selectedSessionID);
  const draftRef = useRef(draft);
  selectedSessionRef.current = snapshot.selectedSessionID;
  draftRef.current = draft;
  attachmentsRef.current = composerAttachments;
  const selected = snapshot.sessions.find(
    (item) => item.session_id === snapshot.selectedSessionID
  );
  const workspaceLabel = selected?.workspace_label ||
    snapshot.sessions[0]?.workspace_label ||
    snapshot.workspaceRoot.split(/[\\/]/).filter(Boolean).at(-1) ||
    "Workspace";
  const projectedEntries = useMemo(
    () => snapshot.conversation.order.flatMap((id) => {
      const node = snapshot.conversation.nodes.get(id);
      return node ? [node] : [];
    }),
    [snapshot.conversation]
  );
  const entries = useMemo(
    () => projectedEntries.filter((entry) => entry.kind !== "receipt"),
    [projectedEntries]
  );
  const transcriptEnd = Math.max(
    0,
    entries.length - transcriptPage * transcriptPageStep
  );
  const transcriptStart = Math.max(0, transcriptEnd - transcriptPageSize);
  const visibleEntries = entries.slice(transcriptStart, transcriptEnd);
  const conversationNavigation = useMemo(
    () => projectConversationNavigation(entries),
    [entries]
  );
  const readerEntryIndex = useMemo(
    () => entries.findIndex((entry) => entry.id === readerEntryID),
    [entries, readerEntryID]
  );
  const currentQuestion = useMemo(
    () => questionPosition(
      conversationNavigation,
      readerEntryID,
      readerEntryIndex < 0 ? undefined : readerEntryIndex
    ),
    [conversationNavigation, readerEntryID, readerEntryIndex]
  );
  const previousQuestion = useMemo(
    () => adjacentQuestion(
      conversationNavigation,
      readerEntryID,
      -1,
      readerEntryIndex < 0 ? undefined : readerEntryIndex
    ),
    [conversationNavigation, readerEntryID, readerEntryIndex]
  );
  const nextQuestion = useMemo(
    () => adjacentQuestion(
      conversationNavigation,
      readerEntryID,
      1,
      readerEntryIndex < 0 ? undefined : readerEntryIndex
    ),
    [conversationNavigation, readerEntryID, readerEntryIndex]
  );
  const pendingApproval = snapshot.conversation.pendingApproval;
  const pendingInput = snapshot.conversation.pendingInput;
  const pendingApprovalKey = pendingRequestKey(snapshot.selectedSessionID, pendingApproval);
  const pendingInputKey = pendingRequestKey(snapshot.selectedSessionID, pendingInput);
  const activeTurn = snapshot.conversation.activeTurnID;
  const selectedProvider = snapshot.profile?.profile.provider ?? "";
  const selectedModel = snapshot.profile?.profile.model ?? "";
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
  const latestReceipt = [...projectedEntries].reverse().find(
    (entry): entry is Extract<ConversationNode, {kind: "receipt"}> =>
      entry.kind === "receipt"
  );
  const turnChrome = useMemo(
    () => projectMessageChrome(snapshot.events),
    [snapshot.events]
  );
  const contextAttribution = useMemo(
    () => latestContextAttribution(snapshot.events),
    [snapshot.events]
  );
  const blankSession = Boolean(
    selected && entries.length === 0 && !snapshot.hydratingSessionID
  );
  const reportLocalError = useCallback((error: unknown) => {
    setLocalError(error instanceof Error ? error.message : String(error));
  }, []);
  const captureReadingPosition = useCallback((includeNavigationLock = false) => {
    if (activeView !== "chat" || !snapshot.selectedSessionID) return undefined;
    if (navigationReaderLockRef.current && !includeNavigationLock) {
      setReaderEntryID(navigationReaderLockRef.current);
      return undefined;
    }
    const node = transcriptRef.current;
    if (!node) return undefined;
    const position = readTranscriptPosition(
      node,
      transcriptPage,
      atBottomRef.current
    );
    if (!position) return undefined;
    readingPositionsRef.current.set(snapshot.selectedSessionID, position);
    setReaderEntryID(transcriptFocusEntryID(node) ?? position.entryID);
    return position;
  }, [activeView, snapshot.selectedSessionID, transcriptPage]);
  const scheduleReadingPositionCapture = useCallback(() => {
    if (readerFrameRef.current !== undefined) return;
    readerFrameRef.current = window.requestAnimationFrame(() => {
      readerFrameRef.current = undefined;
      captureReadingPosition();
    });
  }, [captureReadingPosition]);
  const switchConversationView = useCallback(
    (view: "chat" | "trajectory") => {
      if (view === activeView) return;
      if (activeView === "chat") captureReadingPosition(true);
      if (view === "chat") {
        pendingReadingRestoreRef.current = readingPositionsRef.current.get(
          snapshot.selectedSessionID
        );
      }
      setActiveView(view);
      if (view === "trajectory") {
        requestAnimationFrame(() => {
          if (transcriptRef.current) transcriptRef.current.scrollTop = 0;
        });
      }
    },
    [
      activeView,
      captureReadingPosition,
      snapshot.selectedSessionID
    ]
  );
  const jumpToNavigationItem = useCallback(
    (item: ConversationNavigationItem) => {
      const page = transcriptPageForEntry(
        entries,
        item.entryID,
        transcriptPageSize,
        transcriptPageStep
      );
      if (page === undefined) return;
      setNavigationTarget({
        entryID: item.entryID,
        path: item.path
      });
      setConversationNavigatorOpen(false);
      setTranscriptPage(page);
      setActiveView("chat");
      setNavigationHighlightID(item.entryID);
      setReaderEntryID(item.entryID);
      navigationReaderLockRef.current = item.entryID;
      atBottomRef.current = false;
      setAtBottom(false);
    },
    [entries]
  );
  const jumpToQuestion = useCallback(
    (item?: ConversationNavigationItem) => {
      if (item) jumpToNavigationItem(item);
    },
    [jumpToNavigationItem]
  );
  const openChatFromTrajectory = useCallback(
    (turnID: string, callID?: string) => {
      const item = conversationNavigation.find(
        (candidate) =>
          candidate.kind === "tool" &&
          candidate.turnID === turnID &&
          candidate.callID === callID
      ) ?? conversationNavigation.find(
        (candidate) =>
          candidate.kind === "question" && candidate.turnID === turnID
      ) ?? conversationNavigation.find(
        (candidate) => candidate.turnID === turnID
      );
      if (item) jumpToNavigationItem(item);
    },
    [conversationNavigation, jumpToNavigationItem]
  );
  const openBackgroundActivity = useCallback(
    (target: BackgroundActivityTarget) => {
      window.focus();
      setActivityTarget(target);
      setTranscriptPage(0);
      setActiveView("chat");
      setConversationNavigatorOpen(false);
      void client.selectSession(target.sessionID).catch((error) => {
        setActivityTarget(undefined);
        reportLocalError(error);
      });
    },
    [client, reportLocalError]
  );
  const closeContext = useCallback(() => setContextOpen(false), []);
  const closeSettings = useCallback(() => setSettingsOpen(false), []);
  const inspectTool = useCallback((callID: string) => {
    setInspectCallID(callID);
    switchConversationView("trajectory");
    void client.refreshTrace();
  }, [client, switchConversationView]);
  const attachmentBusy = composerAttachments.some(
    (attachment) => attachment.status === "processing"
  );
  const attachmentFailed = composerAttachments.some(
    (attachment) => attachment.status === "error"
  );
  const visibleContextResources = snapshot.contextResources.filter(
    (resource) =>
      resource.kind !== "attachment" &&
      !(resource.kind === "image" && !resource.path)
  );

  const attachFiles = (
    values: FileList | readonly File[],
    source: ComposerAttachmentSource
  ) => {
    const files = Array.from(values);
    if (
      files.length === 0 ||
      !snapshot.selectedSessionID ||
      snapshot.hydratingSessionID ||
      submitting
    ) {
      return;
    }
    setLocalError("");
    const processing = composerAttachments.filter(
      (attachment) => attachment.status === "processing"
    ).length;
    const available = Math.max(
      0,
      maxComposerAttachments - snapshot.contextResources.length - processing
    );
    const countAccepted = files.slice(0, available);
    let reservedBytes = composerAttachments
      .filter((attachment) => attachment.status !== "error")
      .reduce((total, attachment) => total + attachment.bytes, 0);
    const accepted = countAccepted.filter((file) => {
      if (reservedBytes + file.size > maxComposerAttachmentBytes) return false;
      reservedBytes += file.size;
      return true;
    });
    if (countAccepted.length < files.length) {
      setLocalError(`A prompt accepts at most ${maxComposerAttachments} context items`);
    } else if (accepted.length < countAccepted.length) {
      setLocalError("Attachments exceed the 5 MiB total prompt limit");
    }
    const generation = attachmentGenerationRef.current;
    const sessionID = snapshot.selectedSessionID;
    const pending = accepted.map((file) => ({
      file,
      attachment: {
        id: crypto.randomUUID(),
        name: file.name || "Pasted image",
        mediaType: file.type || "application/octet-stream",
        bytes: file.size,
        source,
        status: "processing" as const
      }
    }));
    setComposerAttachments((current) => [
      ...current,
      ...pending.map(({attachment}) => attachment)
    ]);
    const pipeline = import("./attachmentPipeline");
    for (const {file, attachment} of pending) {
      void pipeline.then(({prepareComposerAttachment}) =>
        prepareComposerAttachment(file)
      ).then((context) => {
        if (
          generation !== attachmentGenerationRef.current ||
          sessionID !== selectedSessionRef.current ||
          removedAttachmentIDs.current.has(attachment.id)
        ) {
          return;
        }
        if (client.getSnapshot().contextResources.some(
          (resource) => resource.digest === context.digest
        )) {
          throw new Error(`${context.label || attachment.name} is already attached`);
        }
        client.addAttachmentContext(context);
        setComposerAttachments((current) => current.map((value) =>
          value.id === attachment.id
            ? {
                ...value,
                name: context.label || value.name,
                mediaType: context.media_type || value.mediaType,
                digest: context.digest,
                status: "ready",
                error: undefined
              }
            : value
        ));
      }).catch((error) => {
        if (generation !== attachmentGenerationRef.current) return;
        setComposerAttachments((current) => current.map((value) =>
          value.id === attachment.id
            ? {
                ...value,
                status: "error",
                error: error instanceof Error ? error.message : String(error)
              }
            : value
        ));
      });
    }
  };

  const removeAttachment = (id: string) => {
    removedAttachmentIDs.current.add(id);
    const attachment = attachmentsRef.current.find((value) => value.id === id);
    if (attachment?.digest) client.removeAttachmentContext(attachment.digest);
    setComposerAttachments((current) => current.filter((value) => value.id !== id));
  };

  useEffect(() => {
    writePreference("ch.sidebar.collapsed", String(railCollapsed));
  }, [railCollapsed]);

  useEffect(() => {
    writePreference("ch.sidebar.width", String(railWidth));
  }, [railWidth]);

  useEffect(() => {
    writePreference("ch.session.isolation", newIsolation);
  }, [newIsolation]);

  useEffect(() => {
    if (
      !activityTarget ||
      snapshot.selectedSessionID !== activityTarget.sessionID ||
      snapshot.hydratingSessionID
    ) {
      return;
    }
    setActiveView("chat");
    const frame = window.requestAnimationFrame(() => {
      const pending = activityTarget.status === "awaiting_approval" ||
        activityTarget.status === "awaiting_input"
        ? document.querySelector<HTMLElement>(".pendingComposer")
        : undefined;
      const anchors = Array.from(
        document.querySelectorAll<HTMLElement>("[data-turn-id]")
      ).filter((node) => node.dataset.turnId === activityTarget.turnID);
      const target = pending ?? anchors.at(-1)?.firstElementChild ??
        transcriptRef.current;
      if (target instanceof HTMLElement) {
        target.scrollIntoView?.({block: "center"});
      }
      if (pending) {
        const focusTarget = activityTarget.status === "awaiting_approval"
          ? pending.querySelector<HTMLElement>(".approvalBody")
          : pending.querySelector<HTMLElement>(
            "textarea:not(:disabled), button:not(:disabled)"
          );
        focusTarget?.focus();
      }
      setActivityTarget(undefined);
    });
    return () => window.cancelAnimationFrame(frame);
  }, [
    activityTarget,
    snapshot.conversation.revision,
    snapshot.hydratingSessionID,
    snapshot.selectedSessionID
  ]);

  useLayoutEffect(() => {
    const saved = readingPositionsRef.current.get(snapshot.selectedSessionID);
    pendingReadingRestoreRef.current = saved;
    setTranscriptPage(saved?.page ?? 0);
    setActiveView("chat");
    setInspectCallID("");
    setConversationNavigatorOpen(false);
    setReaderEntryID(saved?.entryID ?? "");
    setContextOpen(false);
    attachmentGenerationRef.current += 1;
    removedAttachmentIDs.current.clear();
    setComposerAttachments([]);
    setDraggingAttachment(false);
    setCommandMenuOpen(false);
    setCommandQuery("");
    setCommandMenuSource("button");
    atBottomRef.current = saved?.atBottom ?? true;
    setAtBottom(saved?.atBottom ?? true);
  }, [snapshot.selectedSessionID]);

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
    if (!node || activeView !== "chat") return;
    const navigation = navigationTarget;
    if (navigation) {
      const anchor = transcriptAnchor(node, navigation.entryID);
      if (!anchor) return;
      setNavigationTarget(undefined);
      const target = navigation.path
        ? fileTarget(anchor, navigation.path) ?? anchorContent(anchor)
        : anchorContent(anchor);
      centerTranscriptTarget(node, target);
      atBottomRef.current = false;
      setAtBottom(false);
      setReaderEntryID(navigation.entryID);
      setNavigationHighlightID(navigation.entryID);
      if (navigationReaderLockTimerRef.current !== undefined) {
        window.clearTimeout(navigationReaderLockTimerRef.current);
      }
      navigationReaderLockTimerRef.current = window.setTimeout(() => {
        navigationReaderLockRef.current = "";
      }, 250);
      if (navigationHighlightTimerRef.current !== undefined) {
        window.clearTimeout(navigationHighlightTimerRef.current);
      }
      navigationHighlightTimerRef.current = window.setTimeout(
        () => setNavigationHighlightID(""),
        1_400
      );
      return;
    }
    const saved = pendingReadingRestoreRef.current;
    if (saved && saved.page === transcriptPage) {
      pendingReadingRestoreRef.current = undefined;
      restoreTranscriptPosition(node, saved);
      atBottomRef.current = saved.atBottom;
      setAtBottom(saved.atBottom);
      setReaderEntryID(saved.entryID);
      return;
    }
    if (atBottomRef.current && transcriptPage === 0) {
      node.scrollTop = node.scrollHeight;
    }
  }, [
    activeView,
    navigationTarget,
    snapshot.conversation.revision,
    transcriptPage
  ]);

  useEffect(() => {
    const content = transcriptContentRef.current;
    if (
      !content ||
      activeView !== "chat" ||
      typeof ResizeObserver === "undefined"
    ) {
      return;
    }
    const observer = new ResizeObserver(() => {
      const node = transcriptRef.current;
      if (!node) return;
      if (atBottomRef.current && transcriptPage === 0) {
        node.scrollTop = node.scrollHeight;
        return;
      }
      const saved = readingPositionsRef.current.get(snapshot.selectedSessionID);
      if (saved?.page === transcriptPage) {
        restoreTranscriptPosition(node, saved);
      }
    });
    observer.observe(content);
    return () => observer.disconnect();
  }, [activeView, snapshot.selectedSessionID, transcriptPage]);

  useEffect(() => {
    const onKeyDown = (event: KeyboardEvent) => {
      const editable = isEditableElement(event.target);
      if (
        (event.metaKey || event.ctrlKey) &&
        event.key.toLocaleLowerCase() === "f" &&
        !editable &&
        !settingsOpen &&
        !contextOpen &&
        !commandMenuOpen &&
        selected &&
        entries.length > 0
      ) {
        event.preventDefault();
        setConversationNavigatorOpen(true);
        return;
      }
      if (editable || !event.altKey || event.metaKey || event.ctrlKey) return;
      if (event.key === "ArrowUp" && previousQuestion) {
        event.preventDefault();
        jumpToQuestion(previousQuestion);
      } else if (event.key === "ArrowDown" && nextQuestion) {
        event.preventDefault();
        jumpToQuestion(nextQuestion);
      }
    };
    document.addEventListener("keydown", onKeyDown);
    return () => document.removeEventListener("keydown", onKeyDown);
  }, [
    commandMenuOpen,
    contextOpen,
    entries.length,
    jumpToQuestion,
    nextQuestion,
    previousQuestion,
    selected,
    settingsOpen
  ]);

  useEffect(() => () => {
    if (readerFrameRef.current !== undefined) {
      window.cancelAnimationFrame(readerFrameRef.current);
    }
    if (navigationHighlightTimerRef.current !== undefined) {
      window.clearTimeout(navigationHighlightTimerRef.current);
    }
    if (navigationReaderLockTimerRef.current !== undefined) {
      window.clearTimeout(navigationReaderLockTimerRef.current);
    }
  }, []);

  useEffect(() => {
    const textarea = textareaRef.current;
    if (!textarea) return;
    textarea.style.height = "0";
    textarea.style.height = `${Math.min(textarea.scrollHeight, 336)}px`;
  }, [draft]);

  useEffect(() => {
    const viewport = window.visualViewport;
    if (!viewport) return;
    const keepComposerVisible = () => {
      if (document.activeElement !== textareaRef.current) return;
      requestAnimationFrame(() => textareaRef.current?.scrollIntoView({
        block: "nearest"
      }));
    };
    viewport.addEventListener("resize", keepComposerVisible);
    viewport.addEventListener("scroll", keepComposerVisible);
    return () => {
      viewport.removeEventListener("resize", keepComposerVisible);
      viewport.removeEventListener("scroll", keepComposerVisible);
    };
  }, []);

  useEffect(() => {
    if (activeView !== "trajectory" || !activeTurn) return;
    const interval = window.setInterval(() => {
      void client.refreshTrace();
    }, 1_000);
    return () => window.clearInterval(interval);
  }, [activeTurn, activeView, client]);

  useEffect(() => {
    const media = typeof window.matchMedia === "function"
      ? window.matchMedia("(prefers-color-scheme: dark)")
      : undefined;
    const apply = () => applyThemeMode(themeMode, media?.matches ?? false);
    apply();
    if (themeMode !== "system" || !media) return;
    media.addEventListener("change", apply);
    return () => media.removeEventListener("change", apply);
  }, [themeMode]);

  const submit = async (activeAction: "queue" | "steer" = "queue") => {
    const prompt = draft.trim();
    if (!prompt || submitting || attachmentBusy || attachmentFailed) return;
    const submittedSessionID = snapshot.selectedSessionID;
    const submittedTurnID = activeTurn;
    setSubmitting(true);
    setLocalError("");
    try {
      if (submittedTurnID && activeAction === "steer") {
        await client.steer(submittedTurnID, prompt);
      } else if (submittedTurnID) {
        await client.enqueue(submittedTurnID, prompt);
      } else {
        await client.submitPrompt(prompt);
      }
      if (selectedSessionRef.current === submittedSessionID) {
        setComposerAttachments([]);
        removedAttachmentIDs.current.clear();
        if (draftRef.current.trim() === prompt) {
          setDraft("");
          client.saveDraft("", submittedSessionID);
        }
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

  const composerCommands: ComposerCommand[] = [
    {
      id: "attach",
      label: "attach",
      description: "Attach local text files or images",
      argumentHint: "file",
      icon: Paperclip,
      run: () => attachmentInputRef.current?.click()
    },
    {
      id: "context",
      label: "context",
      description: "Browse files, symbols, diagnostics, and diffs",
      argumentHint: "file, symbol, or diff",
      icon: FileCode2,
      run: () => setContextOpen(true)
    },
    {
      id: "compact",
      label: "compact",
      description: "Compact older conversation history",
      icon: Braces,
      disabled: Boolean(activeTurn) || !selected?.latest_turn_id,
      run: async () => {
        try {
          await client.compactThread();
        } catch (error) {
          reportLocalError(error);
        }
      }
    },
    {
      id: "export",
      label: "export",
      description: "Download this Session log as JSON",
      icon: Download,
      run: exportSession
    },
    {
      id: "plan",
      label: "plan",
      description: "Analyze and propose a plan before implementation",
      icon: TextSelect,
      active: snapshot.profile?.profile.mode === "plan",
      disabled: !profileMutable(snapshot, "mode"),
      run: () => client.updateProfile({mode: "plan"}).catch(reportLocalError)
    },
    {
      id: "act",
      label: "act",
      description: "Execute the requested coding task",
      icon: Wrench,
      active: snapshot.profile?.profile.mode === "act",
      disabled: !profileMutable(snapshot, "mode"),
      run: () => client.updateProfile({mode: "act"}).catch(reportLocalError)
    },
    {
      id: "suggest",
      label: "suggest",
      description: "Ask before consequential tool actions",
      icon: AlertTriangle,
      active: snapshot.profile?.profile.approval_posture === "suggest",
      disabled: !profileMutable(snapshot, "approval_posture"),
      run: () => client.updateProfile({
        approval_posture: "suggest"
      }).catch(reportLocalError)
    },
    {
      id: "auto",
      label: "auto",
      description: "Approve actions allowed by the current policy",
      icon: Check,
      active: snapshot.profile?.profile.approval_posture === "auto",
      disabled: !profileMutable(snapshot, "approval_posture"),
      run: () => client.updateProfile({
        approval_posture: "auto"
      }).catch(reportLocalError)
    }
  ];

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
      data-rail-collapsed={railCollapsed || undefined}
      style={{
        "--ch-rail-width": `${railWidth}px`
      } as React.CSSProperties}
    >
      <Suspense fallback={null}>
        <BackgroundActivityMonitor
          sessions={snapshot.sessions}
          selectedSessionID={snapshot.selectedSessionID}
          onOpen={openBackgroundActivity}
        />
      </Suspense>
      <aside className="sessionRail" aria-label="Sessions">
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
        </div>
        <div className="newSessionRow">
          <button
            className="newSessionButton"
            aria-label="New chat"
            disabled={creatingSession}
            onClick={() => void createSession()}
          >
            {creatingSession
              ? <LoaderCircle className="spin" size={16} />
              : <Plus size={16} />}
            <span>New session</span>
          </button>
        </div>
        <div className="sessionSectionHeader">
          <span className="sessionSectionTitle">Workspace</span>
          <div className="sessionSectionActions">
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
            <IconButton
              label="Search sessions"
              icon={<Search size={15} />}
              onClick={() => setSessionSearchOpen((value) => !value)}
            />
            <IconButton
              label={snapshot.includeArchived
                ? "Hide archived sessions"
                : "Show archived sessions"}
              icon={<Archive size={15} />}
              onClick={() => void client.setArchivedVisible(
                !snapshot.includeArchived
              ).catch(reportLocalError)}
            />
          </div>
        </div>
        {(sessionSearchOpen || query) && (
          <label className="searchBox">
            <Search size={14} aria-hidden="true" />
            <span className="srOnly">Search sessions</span>
            <input
              autoFocus
              value={query}
              placeholder="Search sessions"
              onChange={(event) => {
                const value = event.target.value;
                setQuery(value);
                void client.refreshSessions(value);
              }}
              onKeyDown={(event) => {
                if (event.key !== "Escape") return;
                setQuery("");
                setSessionSearchOpen(false);
                void client.refreshSessions();
              }}
            />
            <button
              className="clearSearch"
              aria-label="Close session search"
              onClick={() => {
                setQuery("");
                setSessionSearchOpen(false);
                void client.refreshSessions();
              }}
            >
              <X size={14} />
            </button>
          </label>
        )}
        <div className="sessionList" role="tree" aria-label="Workspace sessions">
          {!query && (
            <button
              className="workspaceRow"
              role="treeitem"
              aria-expanded={workspaceExpanded}
              onClick={() => setWorkspaceExpanded((value) => !value)}
            >
              <DisclosureLeading
                open={workspaceExpanded}
                icon={<FolderOpen size={16} />}
              />
              <span>{workspaceLabel}</span>
            </button>
          )}
          {(query || workspaceExpanded) && (
            <div className="sessionGroup" role="group">
              {snapshot.sessions.map((session) => (
                <SessionRow
                  key={session.session_id}
                  session={session}
                  active={session.session_id === snapshot.selectedSessionID}
                  onClick={() => {
                    captureReadingPosition(true);
                    void client.selectSession(session.session_id);
                  }}
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
          )}
        </div>
        <div className="railFooter">
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
              <p>{selected?.workspace_label || snapshot.workspaceRoot}</p>
            </div>
            {selected && entries.length > 0 && (
              <nav className="viewTabs" aria-label="Conversation views">
                <button
                  aria-current={activeView === "chat" ? "page" : undefined}
                  onClick={() => switchConversationView("chat")}
                >
                  Chat
                </button>
                <button
                  aria-current={activeView === "trajectory" ? "page" : undefined}
                  onClick={() => {
                    switchConversationView("trajectory");
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
            {selected && currentQuestion.total > 0 && (
              <div
                className="conversationNavigationControls"
                aria-label="Question navigation"
              >
                <IconButton
                  label="Previous user question"
                  disabled={!previousQuestion}
                  icon={<ChevronUp size={16} />}
                  onClick={() => jumpToQuestion(previousQuestion)}
                />
                <button
                  type="button"
                  className="conversationNavigationPosition"
                  aria-label="Search conversation"
                  title="Search conversation"
                  data-current-entry={readerEntryID || undefined}
                  onClick={() => setConversationNavigatorOpen(true)}
                >
                  <Search size={14} />
                  <span>
                    {currentQuestion.index + 1}/{currentQuestion.total}
                  </span>
                </button>
                <IconButton
                  label="Next user question"
                  disabled={!nextQuestion}
                  icon={<ChevronDown size={16} />}
                  onClick={() => jumpToQuestion(nextQuestion)}
                />
              </div>
            )}
            {selected && (
              <>
                <IconButton
                  label="Export session"
                  disabled={Boolean(snapshot.hydratingSessionID)}
                  icon={<Download size={17} />}
                  onClick={() => void exportSession()}
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
            scheduleReadingPositionCapture();
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
                onOpenChat={openChatFromTrajectory}
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
                      const anchor = captureReadingPosition(true);
                      pendingReadingRestoreRef.current = anchor;
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
                <div
                  className="transcriptEntryAnchor"
                  data-entry-id={entry.id}
                  data-entry-kind={entry.kind}
                  data-turn-id={entry.turnID || undefined}
                  data-navigation-current={
                    navigationHighlightID === entry.id || undefined
                  }
                  key={entry.id}
                >
                  <TranscriptItem
                    entry={entry}
                    client={client}
                    onError={reportLocalError}
                    onInspect={inspectTool}
                    canOpenPath={snapshot.canOpenPath}
                    checkpoint={checkpointForTurn(
                      snapshot.checkpoints,
                      entry.turnID
                    )}
                    chrome={turnChrome.get(entry.turnID)}
                    feedback={snapshot.messageFeedback[
                      `${snapshot.selectedSessionID}:${entry.id}`
                    ]}
                    onFeedback={(rating) => client.toggleMessageFeedback(
                      entry.id,
                      rating
                    )}
                  />
                </div>
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
                  readingPositionsRef.current.delete(snapshot.selectedSessionID);
                  setReaderEntryID(
                    conversationNavigation
                      .filter((item) => item.kind === "question")
                      .at(-1)?.entryID ?? ""
                  );
                }}
              />
            </div>
          )}

          {selected && <div className="composerSeat" data-composer-seat>
            {localError && <div className="composerError">{localError}</div>}
            {visibleContextResources.length > 0 && (
              <div className="contextTray" aria-label="Prompt context">
                {visibleContextResources.map((resource) => (
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
            {(snapshot.plan || snapshot.tasks.length > 0 ||
              snapshot.agents.length > 0) && (
              <Suspense fallback={null}>
                <SessionProgress
                  plan={snapshot.plan}
                  tasks={snapshot.tasks}
                  agents={snapshot.agents}
                  onOpenTrajectory={() => {
                    switchConversationView("trajectory");
                    void client.refreshTrace();
                  }}
                />
              </Suspense>
            )}
            {snapshot.queuedTurns.length > 0 && (
              <Suspense fallback={null}>
                <TurnQueue
                  items={snapshot.queuedTurns}
                  activeTurnID={activeTurn}
                  onUpdate={(queueID, prompt) =>
                    client.updateQueuedTurn(queueID, prompt).then(() => undefined)}
                  onRemove={(queueID) =>
                    client.removeQueuedTurn(queueID).then(() => undefined)}
                  onPromote={(queueID, turnID) =>
                    client.promoteQueuedTurn(queueID, turnID).then(() => undefined)}
                  onError={reportLocalError}
                />
              </Suspense>
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
              <div
                className="composer"
                data-dragging={draggingAttachment || undefined}
                onDragEnter={(event) => {
                  if (!event.dataTransfer.types.includes("Files")) return;
                  event.preventDefault();
                  setDraggingAttachment(true);
                }}
                onDragOver={(event) => {
                  if (!event.dataTransfer.types.includes("Files")) return;
                  event.preventDefault();
                  event.dataTransfer.dropEffect = "copy";
                }}
                onDragLeave={(event) => {
                  if (event.currentTarget.contains(event.relatedTarget as Node)) return;
                  setDraggingAttachment(false);
                }}
                onDrop={(event) => {
                  event.preventDefault();
                  setDraggingAttachment(false);
                  attachFiles(event.dataTransfer.files, "drop");
                }}
              >
                <input
                  ref={attachmentInputRef}
                  className="srOnly"
                  type="file"
                  multiple
                  accept={composerAttachmentAccept}
                  aria-label="Attach files"
                  disabled={Boolean(snapshot.hydratingSessionID) || submitting}
                  onChange={(event) => {
                    if (event.target.files) {
                      attachFiles(event.target.files, "picker");
                    }
                    event.target.value = "";
                  }}
                />
                {composerAttachments.length > 0 && (
                  <Suspense fallback={null}>
                    <ComposerAttachments
                      attachments={composerAttachments}
                      onRemove={removeAttachment}
                    />
                  </Suspense>
                )}
                <div className="composerInputRow">
                  <textarea
                    ref={textareaRef}
                    value={draft}
                    rows={1}
                    placeholder="Ask CodeHelper"
                    enterKeyHint="send"
                    disabled={Boolean(snapshot.hydratingSessionID) || submitting}
                    onChange={(event) => {
                      const value = event.target.value;
                      setDraft(value);
                      const slashQuery = composerSlashQuery(value);
                      if (slashQuery !== undefined) {
                        setCommandMenuSource("slash");
                        setCommandQuery(slashQuery);
                        setCommandMenuOpen(true);
                      } else if (commandMenuSource === "slash") {
                        setCommandMenuOpen(false);
                        setCommandQuery("");
                      }
                    }}
                    onCompositionStart={() => {
                      composingRef.current = true;
                    }}
                    onCompositionEnd={() => {
                      composingRef.current = false;
                    }}
                    onPaste={(event) => {
                      const files = Array.from(event.clipboardData.files);
                      if (files.length === 0) return;
                      event.preventDefault();
                      attachFiles(files, "paste");
                    }}
                    onKeyDown={(event) => {
                      if (event.nativeEvent.isComposing || composingRef.current) return;
                      if (
                        commandMenuOpen &&
                        commandMenuSource === "slash" &&
                        composerSlashQuery(draft) !== undefined
                      ) {
                        if (event.key === "Enter") event.preventDefault();
                        return;
                      }
                      if (event.key === "Enter" && !event.shiftKey) {
                        event.preventDefault();
                        void submit(
                          activeTurn && (event.metaKey || event.ctrlKey)
                            ? "steer"
                            : "queue"
                        );
                      }
                    }}
                  />
                  <ContextMeter
                    attribution={contextAttribution}
                    fallbackUsed={numberValue(
                      isObject(latestReceipt?.data.context_budget)
                        ? latestReceipt.data.context_budget.active_tokens
                        : 0
                    )}
                    capacity={numberValue(
                      isObject(latestReceipt?.data.context_budget)
                        ? latestReceipt.data.context_budget.max_context_tokens
                        : 0
                    ) || selectedModelEntry?.capabilities.context_window}
                  />
                  <div className="composerActions">
                    {activeTurn && (
                      <IconButton
                        label="Stop turn"
                        danger
                        icon={<CircleStop size={19} />}
                        onClick={() => void client.cancel(activeTurn)}
                      />
                    )}
                    {(!activeTurn || Boolean(draft.trim())) && (
                      <>
                        {activeTurn &&
                          snapshot.contextResources.length === 0 &&
                          composerAttachments.length === 0 && (
                          <IconButton
                            label="Steer current turn"
                            disabled={submitting}
                            icon={<Zap size={18} />}
                            onClick={() => void submit("steer")}
                          />
                        )}
                        <IconButton
                          label={activeTurn ? "Queue next" : "Send"}
                          primary
                          disabled={
                            Boolean(snapshot.hydratingSessionID) ||
                            !draft.trim() ||
                            submitting ||
                            attachmentBusy ||
                            attachmentFailed
                          }
                          icon={submitting
                            ? <LoaderCircle className="spin" size={19} />
                            : activeTurn
                              ? <ListPlus size={19} />
                              : <Send size={19} />}
                          onClick={() => void submit("queue")}
                        />
                      </>
                    )}
                  </div>
                </div>
                <div className="composerControls">
                  <div>
                    <IconButton
                      label="Attach files"
                      icon={<Paperclip size={15} />}
                      disabled={
                        Boolean(snapshot.hydratingSessionID) ||
                        submitting ||
                        snapshot.contextResources.length >= maxComposerAttachments
                      }
                      onClick={() => attachmentInputRef.current?.click()}
                    />
                    <Suspense fallback={null}>
                      <ComposerCommandMenu
                        commands={composerCommands}
                        disabled={Boolean(snapshot.hydratingSessionID) || submitting}
                        open={commandMenuOpen}
                        query={commandQuery}
                        onOpenChange={(open) => {
                          setCommandMenuOpen(open);
                          if (open) {
                            setCommandMenuSource("button");
                            setCommandQuery("");
                          } else if (commandMenuSource === "slash") {
                            setDraft("");
                            setCommandQuery("");
                            setCommandMenuSource("button");
                          }
                        }}
                        onQueryChange={setCommandQuery}
                        onSelect={() => {
                          setCommandQuery("");
                          if (commandMenuSource === "slash") {
                            setDraft("");
                            requestAnimationFrame(() => textareaRef.current?.focus());
                          }
                          setCommandMenuSource("button");
                        }}
                        onRequestComposerFocus={
                          commandMenuSource === "slash"
                            ? () => textareaRef.current?.focus()
                            : undefined
                        }
                      />
                    </Suspense>
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

      {conversationNavigatorOpen && (
        <Suspense fallback={null}>
          <ConversationNavigator
            items={conversationNavigation}
            currentEntryID={readerEntryID}
            hasEarlier={snapshot.historyMoreBefore}
            onClose={() => setConversationNavigatorOpen(false)}
            onSelect={jumpToNavigationItem}
            onLoadEarlier={async () => {
              pendingReadingRestoreRef.current = captureReadingPosition(true);
              return client.loadEarlierHistory();
            }}
          />
        </Suspense>
      )}
      {contextOpen && (
        <Suspense fallback={null}>
          <WorkspaceContextDialog
            snapshot={snapshot}
            client={client}
            onClose={closeContext}
            onError={reportLocalError}
          />
        </Suspense>
      )}
      {settingsOpen && (
        <Suspense fallback={null}>
          <SettingsDialog
            snapshot={snapshot}
            client={client}
            newIsolation={newIsolation}
            theme={themeMode}
            onIsolationChange={setNewIsolation}
            onThemeChange={setThemeMode}
            onClose={closeSettings}
            onError={reportLocalError}
          />
        </Suspense>
      )}
    </div>
  );

}

function readTranscriptPosition(
  scrollport: HTMLElement,
  page: number,
  atBottom: boolean
): TranscriptReadingPosition | undefined {
  const anchors = Array.from(
    scrollport.querySelectorAll<HTMLElement>("[data-entry-id]")
  );
  if (anchors.length === 0) return undefined;
  const viewport = scrollport.getBoundingClientRect();
  const composer = scrollport.querySelector<HTMLElement>("[data-composer-seat]");
  const visibleBottom = composer?.getBoundingClientRect().top ?? viewport.bottom;
  const visible = anchors.filter((anchor) => {
    const box = anchorContent(anchor).getBoundingClientRect();
    return box.bottom > viewport.top && box.top < visibleBottom;
  });
  const anchor = (atBottom ? visible.at(-1) : visible[0]) ??
    (atBottom ? anchors.at(-1) : anchors[0]);
  const entryID = anchor?.dataset.entryId;
  if (!anchor || !entryID) return undefined;
  return {
    entryID,
    top: anchorContent(anchor).getBoundingClientRect().top - viewport.top,
    scrollTop: scrollport.scrollTop,
    page,
    atBottom
  };
}

function transcriptAnchor(
  scrollport: HTMLElement,
  entryID: string
): HTMLElement | undefined {
  return Array.from(
    scrollport.querySelectorAll<HTMLElement>("[data-entry-id]")
  ).find((node) => node.dataset.entryId === entryID);
}

function transcriptFocusEntryID(scrollport: HTMLElement): string | undefined {
  const viewport = scrollport.getBoundingClientRect();
  const composer = scrollport.querySelector<HTMLElement>("[data-composer-seat]");
  const visibleBottom = composer?.getBoundingClientRect().top ?? viewport.bottom;
  const focusLine = viewport.top + (visibleBottom - viewport.top) * 0.42;
  let nearest: {id: string; distance: number} | undefined;
  for (const anchor of scrollport.querySelectorAll<HTMLElement>(
    "[data-entry-id]"
  )) {
    const id = anchor.dataset.entryId;
    if (!id) continue;
    const box = anchorContent(anchor).getBoundingClientRect();
    if (box.bottom <= viewport.top || box.top >= visibleBottom) continue;
    const center = box.top + Math.min(box.height, visibleBottom - box.top) / 2;
    const distance = Math.abs(center - focusLine);
    if (!nearest || distance < nearest.distance) nearest = {id, distance};
  }
  return nearest?.id;
}

function anchorContent(anchor: HTMLElement): HTMLElement {
  return anchor.firstElementChild instanceof HTMLElement
    ? anchor.firstElementChild
    : anchor;
}

function fileTarget(
  anchor: HTMLElement,
  path: string
): HTMLElement | undefined {
  return Array.from(
    anchor.querySelectorAll<HTMLElement>("[data-file-path]")
  ).find((node) => node.dataset.filePath === path);
}

function centerTranscriptTarget(
  scrollport: HTMLElement,
  target: HTMLElement
): void {
  const viewport = scrollport.getBoundingClientRect();
  const composer = scrollport.querySelector<HTMLElement>("[data-composer-seat]");
  const visibleBottom = composer?.getBoundingClientRect().top ?? viewport.bottom;
  const targetBox = target.getBoundingClientRect();
  const visibleHeight = Math.max(1, visibleBottom - viewport.top);
  const desiredTop = viewport.top +
    Math.max(16, (visibleHeight - Math.min(targetBox.height, visibleHeight)) / 2);
  const behavior = scrollport.style.scrollBehavior;
  scrollport.style.scrollBehavior = "auto";
  scrollport.scrollTop += targetBox.top - desiredTop;
  scrollport.style.scrollBehavior = behavior;
}

function restoreTranscriptPosition(
  scrollport: HTMLElement,
  position: TranscriptReadingPosition
): void {
  if (position.atBottom) {
    scrollport.scrollTop = scrollport.scrollHeight;
    return;
  }
  scrollport.scrollTop = position.scrollTop;
  const anchor = transcriptAnchor(scrollport, position.entryID);
  if (!anchor) return;
  const top = anchorContent(anchor).getBoundingClientRect().top -
    scrollport.getBoundingClientRect().top;
  scrollport.scrollTop += top - position.top;
}

function isEditableElement(target: EventTarget | null): boolean {
  if (!(target instanceof HTMLElement)) return false;
  return target.isContentEditable ||
    target instanceof HTMLInputElement ||
    target instanceof HTMLTextAreaElement ||
    target instanceof HTMLSelectElement;
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
  const [menuOpen, setMenuOpen] = useState(false);
  const showStatus = session.status !== "idle";
  const statusTone = session.status === "failed" || session.status === "interrupted"
    ? "error"
    : session.status === "awaiting_approval" || session.status === "awaiting_input"
      ? "warning"
      : session.status === "completed"
        ? "complete"
        : "active";
  const statusLabel = session.status === "awaiting_approval"
    ? "Approval required"
    : session.status === "awaiting_input"
      ? "Input required"
      : session.status === "interrupted"
        ? "Interrupted"
        : session.status === "failed"
          ? "Failed"
          : session.status === "completed"
            ? "Completed"
            : "Running";
  const StatusIcon = session.status === "running"
    ? LoaderCircle
    : session.status === "completed"
      ? Check
      : session.status === "interrupted"
        ? CircleStop
        : AlertTriangle;
  const run = (action: () => void) => {
    setMenuOpen(false);
    action();
  };
  return (
    <div
      className="sessionRow"
      data-active={active || undefined}
      data-menu-open={menuOpen || undefined}
      data-error={Boolean(actionError) || undefined}
      aria-busy={actionPending || undefined}
      role="treeitem"
      aria-selected={active}
      onMouseLeave={() => setMenuOpen(false)}
      onKeyDown={(event) => {
        if (event.key === "Escape") setMenuOpen(false);
      }}
    >
      <button className="sessionSelect" onClick={onClick}>
        <span className="sessionStatusSlot">
          {showStatus && (
            <span
              className="sessionStatusMark"
              data-tone={statusTone}
              title={statusLabel}
              role="img"
              aria-label={statusLabel}
            >
              <StatusIcon
                className={session.status === "running" ? "spin" : undefined}
                size={12}
              />
            </span>
          )}
        </span>
        <span className="sessionTitle">{session.title}</span>
        <span className="sessionAge">{relativeTime(session.updated_at)}</span>
      </button>
      <div className="sessionActions">
        <IconButton
          label={`Session actions for ${session.title}`}
          icon={actionPending
            ? <LoaderCircle className="spin" size={14} />
            : <MoreHorizontal size={15} />}
          disabled={actionPending}
          onClick={() => setMenuOpen((value) => !value)}
        />
        {menuOpen && (
          <div className="sessionMenu" role="menu">
            <button role="menuitem" onClick={() => run(onRename)}>
              <Pencil size={14} /> Rename
            </button>
            <button role="menuitem" onClick={() => run(onPin)}>
              {session.pinned ? <PinOff size={14} /> : <Pin size={14} />}
              {session.pinned ? "Unpin" : "Pin"}
            </button>
            <button role="menuitem" onClick={() => run(onArchive)}>
              <Archive size={14} />
              {session.archived ? "Restore" : "Archive"}
            </button>
            <button
              className="dangerMenuItem"
              role="menuitem"
              onClick={() => run(onDelete)}
            >
              <Trash2 size={14} /> Delete
            </button>
          </div>
        )}
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

function DisclosureLeading({
  open,
  icon
}: {
  open: boolean;
  icon: ReactNode;
}) {
  return (
    <span className="disclosureLeading" aria-hidden="true">
      <span className="disclosureIcon">{icon}</span>
      <span className="disclosureChevron">
        {open ? <ChevronDown size={14} /> : <ChevronRight size={14} />}
      </span>
    </span>
  );
}

const TranscriptItem = memo(function TranscriptItem({
  entry,
  client,
  onError,
  onInspect,
  canOpenPath,
  checkpoint,
  chrome,
  feedback,
  onFeedback
}: {
  entry: ConversationNode;
  client: RuntimeClient;
  onError: (error: unknown) => void;
  onInspect: (callID: string) => void;
  canOpenPath: boolean;
  checkpoint?: SessionCheckpoint;
  chrome?: MessageChrome;
  feedback?: MessageFeedbackRating;
  onFeedback: (rating: MessageFeedbackRating) => void;
}) {
  const [open, setOpen] = useState(false);
  const [guidanceOpen, setGuidanceOpen] = useState(false);
  const [guidance, setGuidance] = useState("");
  const [recoveryPending, setRecoveryPending] = useState("");
  if (entry.kind === "user") {
    return (
      <div
        className="userMessage"
        data-steering={entry.steering || undefined}
      >
        {entry.steering && <small>Steered</small>}
        {entry.text}
      </div>
    );
  }
  if (entry.kind === "assistant") {
    return (
      <article
        className="assistantMessage"
        data-superseded={entry.superseded || undefined}
        data-time-hover-root
      >
        <Suspense fallback={
          <div className="assistantMarkdownFallback">{entry.text}</div>
        }>
          <MarkdownMessage
            text={entry.text}
            settled={Boolean(chrome)}
            canOpenPath={canOpenPath}
            onOpenFile={(path) => void client.openWorkspacePath(path).catch(onError)}
          />
        </Suspense>
        {chrome && !entry.superseded && (
          <MessageActions
            text={entry.text}
            chrome={chrome}
            feedback={feedback}
            onFeedback={onFeedback}
          />
        )}
      </article>
    );
  }
  if (entry.kind === "status") {
    return (
      <div className="terminalState" data-failed={entry.failed || undefined}>
        {entry.failed ? <AlertTriangle size={16} /> : <Check size={16} />}
        <div><strong>{entry.title}</strong><span>{entry.text}</span></div>
        {entry.recoverable && entry.turnID && entry.recovery && (
          <div className="turnRecovery">
            <div className="turnRecoveryStatus">
              <span data-state={entry.recovery.sideEffects}>
                Side effects: {entry.recovery.sideEffects.replaceAll("_", " ")}
              </span>
              {entry.recovery.action && <small>{entry.recovery.action}</small>}
            </div>
            <div className="artifactActions">
              {entry.recovery.canRetry && (
                <button
                  disabled={Boolean(recoveryPending)}
                  onClick={() => {
                    setRecoveryPending("retry");
                    void client.recoverTurn(entry.turnID, "retry")
                      .catch(onError)
                      .finally(() => setRecoveryPending(""));
                  }}
                >
                  <RotateCcw size={13} /> Retry
                </button>
              )}
              {entry.recovery.canContinue && (
                <button
                  disabled={Boolean(recoveryPending)}
                  onClick={() => setGuidanceOpen((value) => !value)}
                >
                  <Play size={13} /> Continue
                </button>
              )}
              {checkpoint?.can_restore && (
                <button
                  disabled={Boolean(recoveryPending)}
                  onClick={() => {
                    setRecoveryPending("restore");
                    void client.restoreCheckpoint(checkpoint.id)
                      .catch(onError)
                      .finally(() => setRecoveryPending(""));
                  }}
                >
                  <RefreshCw size={13} /> Restore
                </button>
              )}
              {checkpoint?.can_fork && (
                <button
                  disabled={Boolean(recoveryPending)}
                  onClick={() => {
                    setRecoveryPending("fork");
                    void client.forkCheckpoint(checkpoint.id)
                      .catch(onError)
                      .finally(() => setRecoveryPending(""));
                  }}
                >
                  <GitFork size={13} /> Fork
                </button>
              )}
            </div>
            {guidanceOpen && (
              <div className="turnRecoveryGuidance">
                <textarea
                  aria-label="Continue guidance"
                  value={guidance}
                  autoFocus
                  onChange={(event) => setGuidance(event.target.value)}
                  placeholder="Additional guidance"
                />
                <button
                  disabled={Boolean(recoveryPending)}
                  onClick={() => {
                    setRecoveryPending("continue");
                    void client.recoverTurn(
                      entry.turnID,
                      "continue",
                      guidance
                    ).catch(onError).finally(() => {
                      setRecoveryPending("");
                      setGuidanceOpen(false);
                    });
                  }}
                >
                  <Play size={13} /> Continue
                </button>
              </div>
            )}
          </div>
        )}
      </div>
    );
  }
  if (entry.kind === "receipt") {
    return null;
  }
  if (entry.kind === "deliverables") {
    return (
      <Suspense fallback={null}>
        <ProducedFiles
          entry={entry}
          client={client}
          canOpenPath={canOpenPath}
          onInspect={onInspect}
          onError={onError}
        />
      </Suspense>
    );
  }
  if (entry.kind === "context") {
    return (
      <div className="disclosure contextDisclosure">
        <button onClick={() => setOpen((value) => !value)} aria-expanded={open}>
          <DisclosureLeading open={open} icon={<FileCode2 size={14} />} />
          <span className="disclosureTitle">{entry.title}</span>
          <span className="disclosureSeparator" aria-hidden="true" />
          <small>{entry.summary}</small>
        </button>
        {open && <pre>{pretty(entry.data)}</pre>}
      </div>
    );
  }
  if (entry.kind === "reasoning") {
    return <ReasoningDisclosure entry={entry} />;
  }
  return <ToolDisclosure
    entry={entry}
    onInspect={onInspect}
    onAddContext={(callID, text) => {
      void client.addTerminalContext(callID, text).catch(onError);
    }}
    {...(canOpenPath ? {
      onOpenFile: (path: string) => {
        void client.openWorkspacePath(path).catch(onError);
      }
    } : {})}
  />;
});

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
  const editPlan = projectEditPlan(data.edit_plan);
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
    <div className="pendingComposer approvalComposer" data-approval-key={requestID}>
      <div className="approvalStrip">
        <span className="approvalDot" />
        <strong>Waiting for approval</strong>
        <IconButton
          label="Stop turn"
          danger
          disabled={submitting}
          icon={<CircleStop size={16} />}
          onClick={() => void client.cancel(activeTurn || event.turn_id)}
        />
      </div>
      <div
        className="approvalBody"
        tabIndex={0}
        role="group"
        aria-label="Approval details"
      >
        <div className="approvalHeadline">
          {editPlan
            ? `Review ${editPlan.files.length} file ${
              editPlan.files.length === 1 ? "change" : "changes"
            }`
            : `${String(data.tool ?? "Action")} requires approval`}
        </div>
        <div className="approvalReason">
          {String(data.effect ?? data.risk ?? "Review the requested effect.")}
        </div>
        {approvalCommand(data.arguments) && (
          <code className="approvalCommand">{approvalCommand(data.arguments)}</code>
        )}
        {editPlan && (
          <EditPlanPreview files={editPlan.files} diff={editPlan.diff} />
        )}
        <div className="pendingMeta">
          {planID && <span>Plan {planID.slice(0, 12)}</span>}
          {Array.isArray(data.resources) && (
            <span>{data.resources.length} protected resources</span>
          )}
          {typeof data.expires_at === "string" && (
            <span>Expires {new Date(data.expires_at).toLocaleString()}</span>
          )}
        </div>
        {error && <span className="composerError">{error}</span>}
        {(scopes.length > 0 || replacementAllowed) && (
          <details className="approvalOptions">
            <summary>Approval options <ChevronDown size={13} /></summary>
            {scopes.length > 0 && (
              <label>
                <span>Scope</span>
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
              </label>
            )}
            {replacementAllowed && (
              <button
                type="button"
                onClick={() => setReplacementOpen((value) => !value)}
              >
                <Braces size={14} />
                {replacementOpen ? "Hide replacement arguments" : "Edit arguments"}
              </button>
            )}
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
          </details>
        )}
      </div>
      <div className="pendingActions approvalActions">
        <button type="button" disabled={submitting} onClick={() => void decide("deny")}>
          Deny
        </button>
        <button
          type="button"
          className="primaryText"
          disabled={submitting || !replacementValid}
          onClick={() => void decide("approve")}
        >
          Approve once
        </button>
      </div>
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
        style={compactSelectWidth(value || "Default")}
        disabled={disabled}
        onChange={(event) => onChange(event.target.value)}
      >
        {values.map((item) => (
          <option value={item} key={item}>{item || "Default"}</option>
        ))}
      </select>
      <ChevronDown size={13} aria-hidden="true" />
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
        style={compactSelectWidth(
          options.find((option) => option.value === value)?.label ?? value
        )}
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
      <ChevronDown size={13} aria-hidden="true" />
    </label>
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
  const turns = numberValue(usage?.turns) || (receipt ? 1 : 0);
  const cost = receipt?.cost_known === false || usage?.cost_known === false
    ? "Unpriced"
    : `${numberValue(receipt?.cost_microunits ?? usage?.cost_microunits)} µ`;
  const detailedValues = [
    `${turns} ${turns === 1 ? "turn" : "turns"}`,
    `${toolCalls} ${toolCalls === 1 ? "tool" : "tools"}`,
    numberValue(latency?.total_ms) > 0
      ? `${formatDuration(numberValue(latency?.total_ms))} total`
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
    cost
  ].filter(Boolean);
  const summary = [
    [
      `${turns} ${turns === 1 ? "turn" : "turns"}`,
      `${toolCalls} ${toolCalls === 1 ? "tool" : "tools"}`
    ].join(" · "),
    [
      numberValue(latency?.total_ms) > 0
        ? `${formatDuration(numberValue(latency?.total_ms))} total`
        : "",
      numberValue(latency?.provider_ms) > 0
        ? `${formatDuration(numberValue(latency?.provider_ms))} model`
        : "",
      numberValue(latency?.tool_ms) > 0
        ? `${formatDuration(numberValue(latency?.tool_ms))} tools`
        : ""
    ].filter(Boolean).join(" · "),
    latency?.first_token_ms !== undefined
      ? `${formatDuration(numberValue(latency.first_token_ms))} TTFT`
      : "",
    [
      totalTokens > 0 ? `${formatCompactCount(totalTokens)} tokens` : "",
      cacheShare
    ].filter(Boolean).join(" · "),
    cost
  ].filter(Boolean).join(" | ");
  return (
    <div
      className="composerMeta"
      aria-label={`Run statistics: ${summary}`}
      title={detailedValues.join(" · ")}
    >
      <span>{summary}</span>
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

function approvalCommand(value: unknown): string {
  const data = typeof value === "string"
    ? parseJSONObject(value)
    : isObject(value) ? value : undefined;
  const command = data?.command ?? data?.cmd;
  return typeof command === "string" ? command : "";
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

export function projectMessageChrome(
  events: readonly RuntimeEvent[]
): Map<string, MessageChrome> {
  const startedAt = new Map<string, number>();
  const receipts = new Map<string, Readonly<Record<string, unknown>>>();
  const completed = new Map<string, RuntimeEvent>();
  for (const event of events) {
    if (event.kind === "turn.started") {
      const timestamp = Date.parse(event.created_at);
      if (Number.isFinite(timestamp)) startedAt.set(event.turn_id, timestamp);
    } else if (event.kind === "turn.receipt") {
      receipts.set(event.turn_id, event.data);
    } else if (event.kind === "turn.completed") {
      completed.set(event.turn_id, event);
    }
  }
  const result = new Map<string, MessageChrome>();
  for (const [turnID, event] of completed) {
    const receipt = receipts.get(turnID);
    const latency = isObject(receipt?.latency) ? receipt.latency : undefined;
    const completedAt = Date.parse(event.created_at);
    const started = startedAt.get(turnID);
    const recordedTotal = optionalNumber(latency?.total_ms);
    const totalMS = recordedTotal ?? (
      started !== undefined && Number.isFinite(completedAt)
        ? Math.max(0, completedAt - started)
        : undefined
    );
    const firstTokenMS = optionalNumber(latency?.first_token_ms);
    const providerMS = optionalNumber(latency?.provider_ms);
    const outputTokens = optionalNumber(receipt?.output_tokens);
    const decodeMS = providerMS !== undefined && firstTokenMS !== undefined
      ? providerMS - firstTokenMS
      : undefined;
    const tokensPerSecond = decodeMS !== undefined && decodeMS > 0 &&
      outputTokens !== undefined && outputTokens > 0
      ? outputTokens / (decodeMS / 1_000)
      : undefined;
    result.set(turnID, {
      completedAt: event.created_at,
      ...(totalMS === undefined ? {} : {totalMS}),
      ...(firstTokenMS === undefined ? {} : {firstTokenMS}),
      ...(tokensPerSecond === undefined ? {} : {tokensPerSecond})
    });
  }
  return result;
}

export function latestContextAttribution(
  events: readonly RuntimeEvent[]
): ContextAttribution | undefined {
  for (let index = events.length - 1; index >= 0; index -= 1) {
    const event = events[index];
    if (event?.kind !== "usage" || !isObject(event.data.context)) continue;
    const context = event.data.context;
    return {
      estimatedTokens: numberValue(context.estimated_tokens),
      stableTokens:
        numberValue(context.stable_tokens) +
        numberValue(context.dynamic_tokens) +
        numberValue(context.continuation_tokens),
      toolTokens:
        numberValue(context.tool_definition_tokens) +
        numberValue(context.history_tool_tokens),
      messageTokens:
        numberValue(context.history_user_tokens) +
        numberValue(context.history_assistant_tokens) +
        numberValue(context.history_other_tokens),
      framingTokens: numberValue(context.provider_framing_tokens)
    };
  }
  return undefined;
}

function pendingRequestKey(sessionID: string, event?: RuntimeEvent): string {
  return `${sessionID}:${String(event?.data.request_id ?? "")}`;
}

function checkpointForTurn(
  checkpoints: readonly SessionCheckpoint[],
  turnID: string
): SessionCheckpoint | undefined {
  return checkpoints
    .filter((checkpoint) => checkpoint.turn_id === turnID)
    .reduce<SessionCheckpoint | undefined>(
      (latest, checkpoint) =>
        !latest || checkpoint.cursor > latest.cursor ? checkpoint : latest,
      undefined
    );
}

function composerSlashQuery(value: string): string | undefined {
  const match = /^\/([^\s]*)$/.exec(value);
  return match?.[1];
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

function optionalNumber(value: unknown): number | undefined {
  return typeof value === "number" && Number.isFinite(value) && value >= 0
    ? value
    : undefined;
}

function formatDuration(milliseconds: number): string {
  return milliseconds < 1_000
    ? `${Math.round(milliseconds)} ms`
    : `${(milliseconds / 1_000).toFixed(milliseconds < 10_000 ? 2 : 1)} s`;
}

function formatCompactCount(value: number): string {
  return compactCountFormat.format(value);
}

function relativeTime(value: string): string {
  const delta = Date.now() - new Date(value).getTime();
  if (!Number.isFinite(delta) || delta < 60_000) return "now";
  if (delta < 3_600_000) return `${Math.floor(delta / 60_000)}m`;
  if (delta < 86_400_000) return `${Math.floor(delta / 3_600_000)}h`;
  return `${Math.floor(delta / 86_400_000)}d`;
}

function readThemeMode(): ThemeMode {
  const value = readPreference("ch.theme");
  return value === "light" || value === "dark" ? value : "system";
}

function applyThemeMode(theme: ThemeMode, systemDark: boolean) {
  writePreference("ch.theme", theme);
  const resolved = theme === "system"
    ? systemDark ? "dark" : "light"
    : theme;
  document.documentElement.dataset.theme = resolved;
  document.documentElement.style.colorScheme = resolved;
}

function safeFilename(value: string): string {
  const safe = value.trim().replace(/[^A-Za-z0-9._-]+/g, "-").replace(/^-+|-+$/g, "");
  return safe || "codehelper-session";
}
