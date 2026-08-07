import type { ChatSessionView } from "./contract.js";

export function filterChatSessions(
  sessions: readonly ChatSessionView[],
  query: string,
): readonly ChatSessionView[] {
  const normalized = query.trim().toLocaleLowerCase();
  if (normalized.length === 0) return sessions;
  return sessions.filter((session) =>
    session.title.toLocaleLowerCase().includes(normalized));
}

export function sessionStatusLabel(session: ChatSessionView): string {
  return session.active
    ? "Running"
    : `${session.isolation} · ${String(session.replayedEvents)} events`;
}
