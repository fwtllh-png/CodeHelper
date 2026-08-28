import type {SessionSummary} from "../protocol";

export type BrowserNotificationPermission =
  | NotificationPermission
  | "unsupported";

export interface BrowserNotificationSettings {
  enabled: boolean;
  permission: BrowserNotificationPermission;
}

export interface BackgroundActivityTarget {
  sessionID: string;
  turnID: string;
  status: string;
}

export interface BackgroundNotice {
  title: string;
  body: string;
  tag: string;
  target: BackgroundActivityTarget;
}

export interface SessionStatusPresentation {
  label: string;
  tone: "active" | "warning" | "error" | "complete" | "idle";
}

export function activityDocumentTitle(
  sessions: readonly SessionSummary[]
): string {
  const approvals = sessions.filter(
    (session) => session.status === "awaiting_approval"
  ).length;
  const inputs = sessions.filter(
    (session) => session.status === "awaiting_input"
  ).length;
  const failed = sessions.filter(
    (session) => session.status === "failed"
  ).length;
  const blocked = sessions.filter(
    (session) => session.status === "blocked"
  ).length;
  const paused = sessions.filter(
    (session) => session.status === "interrupted"
  ).length;
  const running = sessions.filter(
    (session) => session.status === "running"
  ).length;
  const attention = approvals + inputs;
  if (attention > 0) return `(${attention}) Action required · CodeHelper`;
  if (blocked > 0) return `(${blocked}) Blocked · CodeHelper`;
  if (failed > 0) return `(${failed}) Failed · CodeHelper`;
  if (running > 0) return `(${running}) Working · CodeHelper`;
  if (paused > 0) return `(${paused}) Paused · CodeHelper`;
  return "CodeHelper";
}

export function sessionStatusPresentation(
  status: string
): SessionStatusPresentation {
  switch (status) {
  case "running":
    return {label: "Running", tone: "active"};
  case "awaiting_approval":
    return {label: "Approval required", tone: "warning"};
  case "awaiting_input":
    return {label: "Input required", tone: "warning"};
  case "failed":
    return {label: "Failed", tone: "error"};
  case "blocked":
    return {label: "Blocked", tone: "warning"};
  case "interrupted":
    return {label: "Paused", tone: "warning"};
  case "completed":
    return {label: "Completed", tone: "complete"};
  default:
    return {label: "Idle", tone: "idle"};
  }
}

export function backgroundNoticeForTransition(
  previousStatus: string | undefined,
  session: SessionSummary,
  selectedSessionID: string,
  pageHidden: boolean
): BackgroundNotice | undefined {
  if (
    previousStatus === undefined ||
    previousStatus === session.status ||
    (!pageHidden && session.session_id === selectedSessionID) ||
    !session.latest_turn_id
  ) {
    return undefined;
  }
  const message = notificationMessage(session.status);
  if (!message) return undefined;
  return {
    ...message,
    tag: [
      "codehelper",
      session.session_id,
      session.latest_turn_id,
      session.status
    ].join(":"),
    target: {
      sessionID: session.session_id,
      turnID: session.latest_turn_id,
      status: session.status
    }
  };
}

function notificationMessage(
  status: string
): Pick<BackgroundNotice, "title" | "body"> | undefined {
  switch (status) {
  case "awaiting_approval":
    return {
      title: "CodeHelper needs approval",
      body: "A background Session is waiting for approval."
    };
  case "awaiting_input":
    return {
      title: "CodeHelper needs input",
      body: "A background Session is waiting for input."
    };
  case "failed":
    return {
      title: "CodeHelper task failed",
      body: "A background Session failed. Open CodeHelper to review it."
    };
  case "blocked":
    return {
      title: "CodeHelper task blocked",
      body: "A background Session has resumable pending work."
    };
  case "interrupted":
    return {
      title: "CodeHelper task interrupted",
      body: "A background Session was interrupted. Open CodeHelper to review it."
    };
  case "completed":
    return {
      title: "CodeHelper task completed",
      body: "A background Session completed."
    };
  default:
    return undefined;
  }
}
