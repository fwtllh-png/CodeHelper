import type {
  ApprovalDecision,
  ApprovalScope,
} from "../runtime/session.js";

export type WebviewMessage =
  | { readonly type: "ready" }
  | { readonly type: "submit"; readonly text: string }
  | { readonly type: "select-root"; readonly rootId: string }
  | { readonly type: "select-chat"; readonly sessionId: string }
  | { readonly type: "new-chat" }
  | { readonly type: "repair-runtime" }
  | { readonly type: "run-setup" }
  | { readonly type: "merge-chat"; readonly planId?: string }
  | { readonly type: "stop" }
  | {
      readonly type: "approval";
      readonly requestId: string;
      readonly decision: ApprovalDecision;
      readonly scope: ApprovalScope;
      readonly planId?: string;
    }
  | { readonly type: "preview"; readonly requestId: string }
  | { readonly type: "input"; readonly requestId: string; readonly answer: string };

export function decodeWebviewMessage(value: unknown): WebviewMessage {
  if (!isObject(value) || typeof value["type"] !== "string") {
    throw new Error("invalid Webview message");
  }
  switch (value["type"]) {
    case "ready":
      requireKeys(value, ["type"]);
      return { type: "ready" };
    case "submit":
      requireKeys(value, ["type", "text"]);
      return { type: "submit", text: requireBoundedString(value["text"], "text", 64 << 10) };
    case "select-root":
      requireKeys(value, ["type", "rootId"]);
      return {
        type: "select-root",
        rootId: requireRootID(value["rootId"]),
      };
    case "select-chat":
      requireKeys(value, ["type", "sessionId"]);
      return {
        type: "select-chat",
        sessionId: requireBoundedString(value["sessionId"], "sessionId", 256),
      };
    case "new-chat":
      requireKeys(value, ["type"]);
      return { type: "new-chat" };
    case "repair-runtime":
      requireKeys(value, ["type"]);
      return { type: "repair-runtime" };
    case "run-setup":
      requireKeys(value, ["type"]);
      return { type: "run-setup" };
    case "merge-chat":
      requireAllowedMergeKeys(value);
      return {
        type: "merge-chat",
        ...(value["planId"] === undefined
          ? {}
          : { planId: requirePlanID(value["planId"]) }),
      };
    case "stop":
      requireKeys(value, ["type"]);
      return { type: "stop" };
    case "approval":
      requireAllowedKeys(value, ["type", "requestId", "decision", "scope", "planId"]);
      return {
        type: "approval",
        requestId: requireBoundedString(value["requestId"], "requestId", 256),
        decision: requireDecision(value["decision"]),
        scope: requireScope(value["scope"]),
        ...(value["planId"] === undefined
          ? {}
          : { planId: requirePlanID(value["planId"]) }),
      };
    case "preview":
      requireKeys(value, ["type", "requestId"]);
      return {
        type: "preview",
        requestId: requireBoundedString(value["requestId"], "requestId", 256),
      };
    case "input":
      requireKeys(value, ["type", "requestId", "answer"]);
      return {
        type: "input",
        requestId: requireBoundedString(value["requestId"], "requestId", 256),
        answer: requireBoundedString(value["answer"], "answer", 64 << 10, true),
      };
    default:
      throw new Error("unknown Webview message type");
  }
}

function requireAllowedMergeKeys(
  value: Readonly<Record<string, unknown>>,
): void {
  const keys = Object.keys(value);
  if (keys.some((key) => key !== "type" && key !== "planId")) {
    throw new Error("Webview message contains unexpected fields");
  }
}

function requireAllowedKeys(
  value: Readonly<Record<string, unknown>>,
  allowed: readonly string[],
): void {
  if (Object.keys(value).some((key) => !allowed.includes(key))) {
    throw new Error("Webview message contains unexpected fields");
  }
  for (const required of ["type", "requestId", "decision", "scope"]) {
    if (!Object.hasOwn(value, required)) {
      throw new Error(`Webview message is missing ${required}`);
    }
  }
}

function requireKeys(value: Readonly<Record<string, unknown>>, allowed: readonly string[]): void {
  const keys = Object.keys(value);
  if (keys.length !== allowed.length || keys.some((key) => !allowed.includes(key))) {
    throw new Error("Webview message contains unexpected fields");
  }
}

function requireBoundedString(
  value: unknown,
  name: string,
  maximum: number,
  allowEmpty = false,
): string {
  if (typeof value !== "string" ||
    value.length > maximum ||
    (!allowEmpty && value.trim().length === 0)) {
    throw new Error(`${name} is invalid`);
  }
  return value;
}

function requireDecision(value: unknown): ApprovalDecision {
  if (value !== "approve" && value !== "deny" && value !== "cancel") {
    throw new Error("approval decision is invalid");
  }
  return value;
}

function requireScope(value: unknown): ApprovalScope {
  if (value !== "once" && value !== "session" && value !== "always") {
    throw new Error("approval scope is invalid");
  }
  return value;
}

function requirePlanID(value: unknown): string {
  if (typeof value !== "string" || !/^[0-9a-f]{64}$/u.test(value)) {
    throw new Error("edit plan id is invalid");
  }
  return value;
}

function requireRootID(value: unknown): string {
  if (typeof value !== "string" || !/^[0-9a-f]{64}$/u.test(value)) {
    throw new Error("workspace root id is invalid");
  }
  return value;
}

function isObject(value: unknown): value is Readonly<Record<string, unknown>> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}
