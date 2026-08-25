import {
  FileCode2,
  ListTree,
  MessageSquareText,
  Search,
  Wrench,
  X
} from "lucide-react";
import {
  useEffect,
  useMemo,
  useRef,
  useState,
  type KeyboardEvent
} from "react";

import {
  searchConversationNavigation,
  type ConversationNavigationItem,
  type ConversationNavigationKind
} from "./conversationNavigation";
import "./ConversationNavigator.css";

const filters: ReadonlyArray<{
  kind: ConversationNavigationKind | "all";
  label: string;
}> = [
  {kind: "all", label: "All"},
  {kind: "turn", label: "Turns"},
  {kind: "question", label: "Questions"},
  {kind: "tool", label: "Tools"},
  {kind: "file", label: "Files"}
];

export function ConversationNavigator({
  items,
  currentEntryID,
  hasEarlier,
  onClose,
  onSelect,
  onLoadEarlier
}: {
  items: readonly ConversationNavigationItem[];
  currentEntryID?: string;
  hasEarlier: boolean;
  onClose: () => void;
  onSelect: (item: ConversationNavigationItem) => void;
  onLoadEarlier: () => Promise<number>;
}) {
  const [query, setQuery] = useState("");
  const [filter, setFilter] =
    useState<ConversationNavigationKind | "all">("all");
  const [activeID, setActiveID] = useState("");
  const [loadingEarlier, setLoadingEarlier] = useState(false);
  const dialogRef = useRef<HTMLDivElement>(null);
  const searchRef = useRef<HTMLInputElement>(null);
  const results = useMemo(
    () => searchConversationNavigation(items, query, filter),
    [filter, items, query]
  );

  useEffect(() => {
    searchRef.current?.focus();
  }, []);

  useEffect(() => {
    if (results.some((item) => item.id === activeID)) return;
    const current = results.find((item) => item.entryID === currentEntryID);
    setActiveID(current?.id ?? results[0]?.id ?? "");
  }, [activeID, currentEntryID, results]);

  const move = (direction: number) => {
    if (results.length === 0) return;
    const current = results.findIndex((item) => item.id === activeID);
    const next = current < 0
      ? 0
      : (current + direction + results.length) % results.length;
    const item = results[next] ?? results[0];
    setActiveID(item?.id ?? "");
    requestAnimationFrame(() => {
      document.getElementById(navigationResultDOMID(item?.id ?? ""))
        ?.scrollIntoView({block: "nearest"});
    });
  };

  const selectActive = () => {
    const item = results.find((candidate) => candidate.id === activeID) ??
      results[0];
    if (item) onSelect(item);
  };

  const onKeyDown = (event: KeyboardEvent<HTMLDivElement>) => {
    if (event.key === "Escape") {
      event.preventDefault();
      onClose();
      return;
    }
    if (event.key === "ArrowDown" || event.key === "ArrowUp") {
      event.preventDefault();
      move(event.key === "ArrowDown" ? 1 : -1);
      return;
    }
    if (event.key === "Enter") {
      event.preventDefault();
      selectActive();
      return;
    }
    if (event.key !== "Tab") return;
    const focusable = Array.from(
      dialogRef.current?.querySelectorAll<HTMLElement>(
        "button:not(:disabled), input:not(:disabled)"
      ) ?? []
    );
    if (focusable.length === 0) return;
    const current = focusable.indexOf(document.activeElement as HTMLElement);
    const next = event.shiftKey
      ? (current <= 0 ? focusable.length - 1 : current - 1)
      : (current >= focusable.length - 1 ? 0 : current + 1);
    event.preventDefault();
    focusable[next]?.focus();
  };

  return (
    <div
      className="conversationNavigatorBackdrop"
      onPointerDown={(event) => {
        if (event.target === event.currentTarget) onClose();
      }}
    >
      <div
        ref={dialogRef}
        className="conversationNavigator"
        role="dialog"
        aria-modal="true"
        aria-label="Search conversation"
        onKeyDown={onKeyDown}
      >
        <header className="conversationNavigatorHeader">
          <label>
            <Search size={16} aria-hidden="true" />
            <span className="srOnly">Search conversation</span>
            <input
              ref={searchRef}
              role="combobox"
              aria-label="Search conversation"
              aria-controls="conversation-navigation-results"
              aria-expanded="true"
              aria-activedescendant={
                activeID ? navigationResultDOMID(activeID) : undefined
              }
              value={query}
              placeholder="Search turns, questions, tools, and files"
              autoComplete="off"
              spellCheck={false}
              onChange={(event) => setQuery(event.target.value)}
            />
          </label>
          <button
            type="button"
            className="conversationNavigatorClose"
            aria-label="Close conversation search"
            title="Close"
            onClick={onClose}
          >
            <X size={16} />
          </button>
        </header>
        <div
          className="conversationNavigatorFilters"
          role="tablist"
          aria-label="Conversation result type"
        >
          {filters.map((item) => (
            <button
              key={item.kind}
              type="button"
              role="tab"
              aria-selected={filter === item.kind}
              onClick={() => setFilter(item.kind)}
            >
              {item.label}
            </button>
          ))}
        </div>
        <div
          id="conversation-navigation-results"
          className="conversationNavigatorResults"
          role="listbox"
          aria-label="Conversation search results"
        >
          {results.map((item) => (
            <button
              id={navigationResultDOMID(item.id)}
              key={item.id}
              type="button"
              role="option"
              aria-selected={item.id === activeID}
              data-active={item.id === activeID || undefined}
              onMouseEnter={() => setActiveID(item.id)}
              onClick={() => onSelect(item)}
            >
              <span className="conversationNavigatorIcon" data-kind={item.kind}>
                <NavigationIcon kind={item.kind} />
              </span>
              <span className="conversationNavigatorText">
                <strong>{item.label}</strong>
                <small>{item.detail}</small>
              </span>
              <span className="conversationNavigatorTurn">
                T{item.turnNumber}
              </span>
            </button>
          ))}
          {results.length === 0 && (
            <span className="conversationNavigatorEmpty">
              No matching conversation items
            </span>
          )}
        </div>
        <footer className="conversationNavigatorFooter">
          <span>{results.length} results</span>
          {hasEarlier && (
            <button
              type="button"
              disabled={loadingEarlier}
              onClick={() => {
                setLoadingEarlier(true);
                void onLoadEarlier().finally(() => {
                  setLoadingEarlier(false);
                  searchRef.current?.focus();
                });
              }}
            >
              {loadingEarlier ? "Loading history..." : "Load earlier history"}
            </button>
          )}
        </footer>
      </div>
    </div>
  );
}

function NavigationIcon({kind}: {kind: ConversationNavigationKind}) {
  if (kind === "question") return <MessageSquareText size={15} />;
  if (kind === "tool") return <Wrench size={15} />;
  if (kind === "file") return <FileCode2 size={15} />;
  return <ListTree size={15} />;
}

function navigationResultDOMID(id: string): string {
  let hash = 2166136261;
  for (let index = 0; index < id.length; index += 1) {
    hash ^= id.charCodeAt(index);
    hash = Math.imul(hash, 16777619);
  }
  return `conversation-navigation-${(hash >>> 0).toString(36)}`;
}
