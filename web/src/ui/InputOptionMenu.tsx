import {Check, ChevronDown, ChevronUp} from "lucide-react";
import {
  useEffect,
  useRef,
  useState,
  type KeyboardEvent
} from "react";

interface Props {
  value: string;
  options: readonly string[];
  disabled?: boolean;
  onChange: (value: string) => void;
}

export function InputOptionMenu({
  value,
  options,
  disabled,
  onChange
}: Props) {
  const [open, setOpen] = useState(false);
  const rootRef = useRef<HTMLDivElement>(null);
  const triggerRef = useRef<HTMLButtonElement>(null);
  const listRef = useRef<HTMLDivElement>(null);
  const selectedIndex = options.indexOf(value);

  useEffect(() => {
    if (!open) return;
    const onPointerDown = (event: PointerEvent) => {
      if (event.target instanceof Node && rootRef.current?.contains(event.target)) {
        return;
      }
      setOpen(false);
    };
    document.addEventListener("pointerdown", onPointerDown);
    return () => document.removeEventListener("pointerdown", onPointerDown);
  }, [open]);

  useEffect(() => {
    if (!open) return;
    const frame = requestAnimationFrame(() => {
      const options = listRef.current?.querySelectorAll<HTMLButtonElement>(
        '[role="option"]'
      );
      options?.[selectedIndex >= 0 ? selectedIndex : 0]?.focus();
    });
    return () => cancelAnimationFrame(frame);
  }, [open, selectedIndex]);

  const close = () => {
    setOpen(false);
    triggerRef.current?.focus();
  };

  const onListKeyDown = (event: KeyboardEvent<HTMLDivElement>) => {
    if (event.key === "Escape") {
      event.preventDefault();
      close();
      return;
    }
    if (!["ArrowDown", "ArrowUp", "Home", "End"].includes(event.key)) return;
    event.preventDefault();
    const items = Array.from(
      listRef.current?.querySelectorAll<HTMLButtonElement>('[role="option"]') ?? []
    );
    if (items.length === 0) return;
    const current = items.indexOf(document.activeElement as HTMLButtonElement);
    let next = event.key === "Home" ? 0 : items.length - 1;
    if (event.key === "ArrowDown") next = (current + 1) % items.length;
    if (event.key === "ArrowUp") next = (current - 1 + items.length) % items.length;
    items[next]?.focus();
  };

  return (
    <div
      className="reasoningMenuRoot inputOptionRoot"
      ref={rootRef}
    >
      <button
        ref={triggerRef}
        type="button"
        className="reasoningMenuTrigger"
        aria-label="Input options"
        aria-haspopup="listbox"
        aria-expanded={open}
        disabled={disabled}
        style={{
          width: "100%",
          height: 34,
          justifyContent: "space-between",
          gap: 8,
          padding: "0 10px",
          border: "1px solid var(--ch-border)",
          background: "var(--ch-panel)"
        }}
        onClick={() => setOpen((current) => !current)}
        onKeyDown={(event) => {
          if (event.key !== "ArrowDown" && event.key !== "ArrowUp") return;
          event.preventDefault();
          setOpen(true);
        }}
      >
        <span
          data-placeholder={selectedIndex < 0 || undefined}
          style={{
            minWidth: 0,
            overflow: "hidden",
            color: selectedIndex < 0 ? "var(--ch-text-muted)" : undefined,
            textOverflow: "ellipsis",
            whiteSpace: "nowrap"
          }}
        >
          {selectedIndex >= 0 ? value : "Choose an option"}
        </span>
        {open
          ? <ChevronUp size={16} aria-hidden="true" />
          : <ChevronDown size={16} aria-hidden="true" />}
      </button>
      {open && (
        <div
          ref={listRef}
          className="reasoningMenu inputOptionList"
          role="listbox"
          aria-label="Input options"
          style={{
            right: "auto",
            left: 0,
            width: "min(720px, calc(100vw - 32px))",
            maxHeight: "min(360px, 50vh)",
            overflowY: "auto"
          }}
          onKeyDown={onListKeyDown}
        >
          {options.map((option) => (
            <button
              key={option}
              type="button"
              role="option"
              aria-selected={option === value}
              data-selected={option === value || undefined}
              style={{
                minHeight: 40,
                height: "auto",
                gap: 10,
                padding: "8px 10px",
                lineHeight: "18px",
                whiteSpace: "normal",
                overflowWrap: "anywhere"
              }}
              onClick={() => {
                onChange(option);
                close();
              }}
            >
              <span>{option}</span>
              {option === value && <Check size={16} aria-hidden="true" />}
            </button>
          ))}
        </div>
      )}
    </div>
  );
}
