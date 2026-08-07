import type { ChatSessionView } from "./contract.js";

export type SessionStatusFilter =
  | "all"
  | "active"
  | "attention"
  | "completed"
  | "failed"
  | "archived";

export interface SessionGroup {
  readonly id: "pinned" | "today" | "yesterday" | "week" | "older" | "archived";
  readonly label: string;
  readonly sessions: readonly ChatSessionView[];
}

export function filterChatSessions(
  sessions: readonly ChatSessionView[],
  query: string,
  status: SessionStatusFilter = "all",
): readonly ChatSessionView[] {
  const normalized = query.trim().toLocaleLowerCase();
  if (normalized.length === 0 && status === "all") return sessions;
  return sessions.filter((session) => {
    if (!matchesStatus(session, status)) return false;
    if (normalized.length === 0) return true;
    return [
      session.title,
      session.workspaceLabel,
      session.provider,
      session.model,
      session.mode,
      session.status,
    ].some((value) => value?.toLocaleLowerCase().includes(normalized));
  });
}

export function groupChatSessions(
  sessions: readonly ChatSessionView[],
  now = new Date(),
): readonly SessionGroup[] {
  const buckets = new Map<SessionGroup["id"], ChatSessionView[]>();
  const startToday = startOfDay(now);
  const startYesterday = new Date(startToday.getTime() - 24 * 60 * 60 * 1000);
  const startWeek = new Date(startToday.getTime() - 7 * 24 * 60 * 60 * 1000);
  for (const session of sessions) {
    let id: SessionGroup["id"];
    const updated = new Date(session.updatedAt);
    switch (true) {
      case session.archived:
        id = "archived";
        break;
      case session.pinned:
        id = "pinned";
        break;
      case updated >= startToday:
        id = "today";
        break;
      case updated >= startYesterday:
        id = "yesterday";
        break;
      case updated >= startWeek:
        id = "week";
        break;
      default:
        id = "older";
    }
    const bucket = buckets.get(id) ?? [];
    bucket.push(session);
    buckets.set(id, bucket);
  }
  const labels: Readonly<Record<SessionGroup["id"], string>> = {
    pinned: "Pinned",
    today: "Today",
    yesterday: "Yesterday",
    week: "Previous 7 Days",
    older: "Older",
    archived: "Archived",
  };
  return ([
    "pinned", "today", "yesterday", "week", "older", "archived",
  ] as const).flatMap((id) => {
    const values = buckets.get(id);
    return values === undefined || values.length === 0
      ? []
      : [{ id, label: labels[id], sessions: values }];
  });
}

export function sessionStatusLabel(session: ChatSessionView): string {
  const label: Readonly<Record<ChatSessionView["status"], string>> = {
    idle: "Idle",
    running: "Running",
    awaiting_approval: "Approval needed",
    awaiting_input: "Input needed",
    completed: "Completed",
    failed: "Failed",
    interrupted: "Interrupted",
  };
  const model = [session.provider, session.model].filter(
    (value) => value !== undefined && value.length > 0,
  ).join("/");
  const status = label[session.status] ?? "Unknown";
  return `${status} · ${model || session.isolation}`;
}

function matchesStatus(
  session: ChatSessionView,
  filter: SessionStatusFilter,
): boolean {
  switch (filter) {
    case "all":
      return true;
    case "active":
      return session.status === "running";
    case "attention":
      return session.status === "awaiting_approval" ||
        session.status === "awaiting_input";
    case "completed":
      return session.status === "completed";
    case "failed":
      return session.status === "failed" ||
        session.status === "interrupted";
    case "archived":
      return session.archived;
  }
}

function startOfDay(value: Date): Date {
  return new Date(
    value.getFullYear(),
    value.getMonth(),
    value.getDate(),
  );
}
