import {
  Check,
  Copy,
  ThumbsDown,
  ThumbsUp
} from "lucide-react";
import {
  useEffect,
  useRef,
  useState,
  type CSSProperties
} from "react";

export type MessageFeedbackRating = "positive" | "negative";

export interface MessageChrome {
  completedAt?: string;
  totalMS?: number;
  firstTokenMS?: number;
  tokensPerSecond?: number;
}

const messageTimeFormat = new Intl.DateTimeFormat(undefined, {
  hour: "2-digit",
  minute: "2-digit",
  hour12: false
});

export function MessageActions({
  text,
  chrome,
  feedback,
  onFeedback
}: {
  text: string;
  chrome?: MessageChrome;
  feedback?: MessageFeedbackRating;
  onFeedback: (rating: MessageFeedbackRating) => void;
}) {
  const [copied, setCopied] = useState(false);
  const copyTimer = useRef<number>();

  useEffect(() => () => {
    if (copyTimer.current !== undefined) {
      window.clearTimeout(copyTimer.current);
    }
  }, []);

  const copy = async () => {
    if (copied) return;
    try {
      await navigator.clipboard.writeText(text);
    } catch {
      return;
    }
    setCopied(true);
    copyTimer.current = window.setTimeout(() => setCopied(false), 1_000);
  };

  const facts = [
    chrome?.completedAt ? formatMessageTime(chrome.completedAt) : "",
    chrome?.totalMS !== undefined
      ? `Ran for ${formatRunDuration(chrome.totalMS)}`
      : "",
    chrome?.firstTokenMS !== undefined
      ? `${formatLatency(chrome.firstTokenMS)} TTFT`
      : "",
    chrome?.tokensPerSecond !== undefined
      ? `~${formatThroughput(chrome.tokensPerSecond)} tok/s`
      : ""
  ].filter(Boolean);

  return (
    <div className="messageActions" aria-label="Message actions">
      <button
        type="button"
        aria-label={copied ? "Copied" : "Copy response"}
        title={copied ? "Copied" : "Copy response"}
        onClick={() => void copy()}
      >
        {copied ? <Check size={16} /> : <Copy size={16} />}
      </button>
      <button
        type="button"
        aria-label={feedback === "positive" ? "Remove like" : "Like response"}
        aria-pressed={feedback === "positive"}
        title={feedback === "positive" ? "Remove like" : "Like response"}
        onClick={() => onFeedback("positive")}
      >
        <ThumbsUp size={16} />
      </button>
      <button
        type="button"
        aria-label={feedback === "negative" ? "Remove dislike" : "Dislike response"}
        aria-pressed={feedback === "negative"}
        title={feedback === "negative" ? "Remove dislike" : "Dislike response"}
        onClick={() => onFeedback("negative")}
      >
        <ThumbsDown size={16} />
      </button>
      {facts.length > 0 && (
        <span className="messageFacts">{facts.join("  ·  ")}</span>
      )}
    </div>
  );
}

export interface ContextAttribution {
  estimatedTokens: number;
  stableTokens: number;
  toolTokens: number;
  messageTokens: number;
  framingTokens: number;
}

export function ContextMeter({
  attribution,
  fallbackUsed,
  capacity
}: {
  attribution?: ContextAttribution;
  fallbackUsed: number;
  capacity?: number;
}) {
  const [open, setOpen] = useState(false);
  const rootRef = useRef<HTMLSpanElement>(null);
  const used = attribution?.estimatedTokens || fallbackUsed;
  const maximum = capacity || 0;
  const share = maximum > 0 ? Math.min(1, used / maximum) : 0;
  const percent = Math.round(share * 100);

  useEffect(() => {
    if (!open) return;
    const onPointerDown = (event: PointerEvent) => {
      if (event.target instanceof Node &&
          rootRef.current?.contains(event.target)) {
        return;
      }
      setOpen(false);
    };
    const onKeyDown = (event: globalThis.KeyboardEvent) => {
      if (event.key === "Escape") setOpen(false);
    };
    document.addEventListener("pointerdown", onPointerDown);
    document.addEventListener("keydown", onKeyDown);
    return () => {
      document.removeEventListener("pointerdown", onPointerDown);
      document.removeEventListener("keydown", onKeyDown);
    };
  }, [open]);

  if (used <= 0 || maximum <= 0) return null;
  const rows = attribution ? [
    {label: "Stable / system", value: attribution.stableTokens, tone: "stable"},
    {label: "Tools", value: attribution.toolTokens, tone: "tools"},
    {label: "Messages", value: attribution.messageTokens, tone: "messages"},
    {label: "Provider framing", value: attribution.framingTokens, tone: "framing"}
  ] : [];
  return (
    <span className="contextMeterRoot" ref={rootRef}>
      <button
        type="button"
        className="contextMeter"
        title={`${percent}% of context used`}
        aria-label={`${percent}% of context used`}
        aria-haspopup="dialog"
        aria-expanded={open}
        onClick={() => setOpen((value) => !value)}
      >
        <svg viewBox="0 0 16 16" aria-hidden="true">
          <circle className="contextTrack" cx="8" cy="8" r="6" />
          <circle
            className="contextFill"
            cx="8"
            cy="8"
            r="6"
            pathLength="100"
            strokeDasharray={`${percent} 100`}
            transform="rotate(-90 8 8)"
          />
        </svg>
      </button>
      {open && (
        <div
          className="contextPanel"
          role="dialog"
          aria-label="Context usage"
        >
          <div className="contextPanelHeader">
            <span>Context used</span>
            <strong>{percent}%</strong>
            <b>
              {attribution ? "~" : ""}{formatTokenCount(used)} / {formatTokenCount(maximum)}
            </b>
          </div>
          <div className="contextBar" aria-hidden="true">
            {rows.length === 0 ? (
              <span style={{width: `${percent}%`}} />
            ) : rows.filter((row) => row.value > 0).map((row) => (
              <span
                key={row.tone}
                data-tone={row.tone}
                style={{
                  width: `${share * row.value / used * 100}%`
                }}
              />
            ))}
          </div>
          {rows.length > 0 && (
            <dl className="contextRows">
              {rows.map((row) => (
                <div key={row.tone}>
                  <dt>
                    <span data-tone={row.tone} aria-hidden="true" />
                    {row.label}
                  </dt>
                  <dd>~{formatTokenCount(row.value)}</dd>
                </div>
              ))}
            </dl>
          )}
          <small>Last model sample · provider-safe token attribution</small>
        </div>
      )}
    </span>
  );
}

function formatMessageTime(value: string): string {
  const parsed = Date.parse(value);
  if (!Number.isFinite(parsed)) return "";
  return messageTimeFormat.format(parsed);
}

function formatRunDuration(value: number): string {
  const seconds = Math.max(0, Math.round(value / 1_000));
  if (seconds < 60) return `${seconds}s`;
  return `${Math.floor(seconds / 60)}m ${String(seconds % 60).padStart(2, "0")}s`;
}

function formatLatency(value: number): string {
  const seconds = Math.max(0, value) / 1_000;
  return `${seconds < 10 ? Math.round(seconds * 10) / 10 : Math.round(seconds)}s`;
}

function formatThroughput(value: number): string {
  return value >= 10 ? String(Math.round(value)) : String(Math.round(value * 10) / 10);
}

function formatTokenCount(value: number): string {
  if (value < 1_000) return String(Math.round(value));
  if (value < 1_000_000) return `${Math.round(value / 100) / 10}K`;
  return `${Math.round(value / 100_000) / 10}M`;
}

export function compactSelectWidth(value: string): CSSProperties {
  const characters = Math.max(3, Math.min(18, [...value].length));
  return {width: `calc(${characters}ch + 24px)`};
}
