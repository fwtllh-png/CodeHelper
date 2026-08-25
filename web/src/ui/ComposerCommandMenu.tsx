import {
  Check,
  Plus,
  type LucideIcon
} from "lucide-react";
import {
  useEffect,
  useMemo,
  useRef,
  useState,
  type KeyboardEvent
} from "react";
import "./ComposerCommandMenu.css";

export interface ComposerCommand {
  id: string;
  label: string;
  description: string;
  argumentHint?: string;
  icon: LucideIcon;
  active?: boolean;
  disabled?: boolean;
  run: () => void | Promise<unknown>;
}

export function ComposerCommandMenu({
  commands,
  disabled,
  open,
  query,
  onOpenChange,
  onQueryChange,
  onSelect,
  onRequestComposerFocus
}: {
  commands: readonly ComposerCommand[];
  disabled?: boolean;
  open: boolean;
  query: string;
  onOpenChange: (open: boolean) => void;
  onQueryChange: (query: string) => void;
  onSelect?: (command: ComposerCommand) => void;
  onRequestComposerFocus?: () => void;
}) {
  const [activeID, setActiveID] = useState("");
  const [recentIDs, setRecentIDs] = useState(readRecentCommandIDs);
  const rootRef = useRef<HTMLSpanElement>(null);
  const triggerRef = useRef<HTMLButtonElement>(null);
  const searchRef = useRef<HTMLInputElement>(null);
  const normalizedQuery = normalizeCommandQuery(query);
  const matching = useMemo(
    () => commands.filter((command) => commandMatches(command, normalizedQuery)),
    [commands, normalizedQuery]
  );
  const recent = normalizedQuery
    ? []
    : recentIDs.flatMap((id) => {
        const command = matching.find((value) => value.id === id);
        return command ? [command] : [];
      });
  const recentSet = new Set(recent.map((command) => command.id));
  const remaining = matching.filter((command) => !recentSet.has(command.id));
  const visible = [...recent, ...remaining];
  const enabled = visible.filter((command) => !command.disabled);

  useEffect(() => {
    if (!open) return;
    searchRef.current?.focus();
    const onPointerDown = (event: PointerEvent) => {
      if (event.target instanceof Node &&
          rootRef.current?.contains(event.target)) {
        return;
      }
      onOpenChange(false);
    };
    document.addEventListener("pointerdown", onPointerDown);
    return () => document.removeEventListener("pointerdown", onPointerDown);
  }, [onOpenChange, open]);

  useEffect(() => {
    if (!open) return;
    if (!enabled.some((command) => command.id === activeID)) {
      setActiveID(enabled[0]?.id ?? "");
    }
  }, [activeID, enabled, open]);

  const select = (command: ComposerCommand) => {
    if (command.disabled) return;
    const nextRecent = [
      command.id,
      ...recentIDs.filter((id) => id !== command.id)
    ].slice(0, 4);
    setRecentIDs(nextRecent);
    writeRecentCommandIDs(nextRecent);
    onOpenChange(false);
    onSelect?.(command);
    void command.run();
  };
  const move = (direction: number) => {
    if (enabled.length === 0) return;
    const current = enabled.findIndex((command) => command.id === activeID);
    const next = current < 0
      ? 0
      : (current + direction + enabled.length) % enabled.length;
    setActiveID(enabled[next]?.id ?? enabled[0]?.id ?? "");
  };
  const onKeyDown = (event: KeyboardEvent<HTMLDivElement>) => {
    if (event.key === "Escape") {
      event.preventDefault();
      onOpenChange(false);
      if (onRequestComposerFocus) {
        onRequestComposerFocus();
      } else {
        triggerRef.current?.focus();
      }
    } else if (event.key === "ArrowDown" || event.key === "ArrowUp") {
      event.preventDefault();
      move(event.key === "ArrowDown" ? 1 : -1);
    } else if (event.key === "Home" || event.key === "End") {
      event.preventDefault();
      setActiveID(
        (event.key === "Home" ? enabled[0] : enabled.at(-1))?.id ?? ""
      );
    } else if (event.key === "Enter") {
      event.preventDefault();
      const command = enabled.find((value) => value.id === activeID) ?? enabled[0];
      if (command) select(command);
    }
  };

  const renderCommands = (values: readonly ComposerCommand[]) =>
    values.map((command) => {
      const Icon = command.icon;
      return (
        <button
          id={`composer-command-${command.id}`}
          key={command.id}
          type="button"
          role="menuitem"
          disabled={command.disabled}
          data-active={command.id === activeID || undefined}
          aria-current={command.active ? "true" : undefined}
          onMouseEnter={() => {
            if (!command.disabled) setActiveID(command.id);
          }}
          onClick={() => select(command)}
        >
          <Icon size={15} />
          <span className="commandMenuName">
            <kbd>/{command.label}</kbd>
            {command.argumentHint && <small>{command.argumentHint}</small>}
          </span>
          <small className="commandMenuDescription">{command.description}</small>
          {command.active && <Check size={14} aria-hidden="true" />}
        </button>
      );
    });

  return (
    <span className="commandMenuRoot" ref={rootRef}>
      <button
        ref={triggerRef}
        type="button"
        className="commandMenuTrigger"
        aria-label="Commands"
        aria-haspopup="menu"
        aria-expanded={open}
        disabled={disabled}
        onClick={() => onOpenChange(!open)}
      >
        <Plus size={15} />
      </button>
      {open && (
        <div className="commandMenu" onKeyDown={onKeyDown}>
          <label className="commandMenuSearch">
            <span className="srOnly">Search commands</span>
            <input
              ref={searchRef}
              role="searchbox"
              aria-label="Search commands"
              aria-controls="composer-command-results"
              aria-activedescendant={
                activeID ? `composer-command-${activeID}` : undefined
              }
              value={query}
              placeholder="Search commands"
              autoComplete="off"
              spellCheck={false}
              onChange={(event) => onQueryChange(event.target.value)}
            />
          </label>
          <div
            id="composer-command-results"
            className="commandMenuResults"
            role="menu"
            aria-label="Commands"
          >
            {recent.length > 0 && (
              <div role="group" aria-label="Recent commands">
                <span className="commandMenuTitle" role="presentation">Recent</span>
                {renderCommands(recent)}
              </div>
            )}
            {remaining.length > 0 && (
              <div role="group" aria-label="Commands">
                <span className="commandMenuTitle" role="presentation">Commands</span>
                {renderCommands(remaining)}
              </div>
            )}
            {visible.length === 0 && (
              <span className="commandMenuEmpty" role="presentation">
                No matching commands
              </span>
            )}
          </div>
        </div>
      )}
    </span>
  );
}

const recentCommandKey = "ch.composer.recent_commands";

function normalizeCommandQuery(value: string): string {
  return value.trim().replace(/^\/+/, "").toLocaleLowerCase();
}

function commandMatches(
  command: ComposerCommand,
  query: string
): boolean {
  if (!query) return true;
  return [
    command.label,
    command.description,
    command.argumentHint ?? ""
  ].some((value) => value.toLocaleLowerCase().includes(query));
}

function readRecentCommandIDs(): string[] {
  try {
    const value = JSON.parse(
      window.localStorage?.getItem(recentCommandKey) ?? "[]"
    );
    return Array.isArray(value)
      ? value.filter((id): id is string => typeof id === "string").slice(0, 4)
      : [];
  } catch {
    return [];
  }
}

function writeRecentCommandIDs(ids: readonly string[]): void {
  try {
    window.localStorage?.setItem(recentCommandKey, JSON.stringify(ids));
  } catch {
    // Command history is an optional browser preference.
  }
}
