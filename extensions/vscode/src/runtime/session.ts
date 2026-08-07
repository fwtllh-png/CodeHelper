import { randomUUID } from "node:crypto";

import type { TurnStartPayload } from "../protocol/generated.js";
import type {
  SessionProfilePatch,
  SessionProfileSnapshot,
  SessionProfileUpdate,
} from "../protocol/generated.js";
import type { WorkspaceIdentity } from "../workspace/identity.js";
import {
  decodeSessionProfileSnapshot,
  decodeSessionProfileUpdate,
  validateSessionProfilePatch,
} from "./profile.js";

export type ApprovalDecision = "approve" | "deny" | "cancel";
export type ApprovalScope = "once" | "session" | "always";
export type EditorContextReference = NonNullable<TurnStartPayload["context"]>[number];
export type {
  SessionProfilePatch,
  SessionProfileSnapshot,
  SessionProfileUpdate,
} from "../protocol/generated.js";

export interface SubmitReceipt {
  readonly operationId: string;
  readonly turnId: string;
  readonly itemId: string;
}

export interface SessionTransport {
  request(method: string, params?: unknown): Promise<unknown>;
}

export class SessionCommands {
  readonly #transport: SessionTransport;
  readonly #sessionId: string;
  readonly #trusted: () => boolean;
  readonly #workspaceIdentity: WorkspaceIdentity | undefined;

  public constructor(
    transport: SessionTransport,
    sessionId: string,
    trusted: () => boolean,
    workspaceIdentity?: WorkspaceIdentity,
  ) {
    this.#transport = transport;
    this.#sessionId = sessionId;
    this.#trusted = trusted;
    this.#workspaceIdentity = workspaceIdentity;
  }

  public async submitPrompt(
    prompt: string,
    context: readonly EditorContextReference[],
  ): Promise<SubmitReceipt> {
    const trimmed = prompt.trim();
    if (trimmed.length === 0 || trimmed.length > 64 << 10) {
      throw new Error("prompt must contain between 1 and 65536 characters");
    }
    return this.#submit("turn.start", {
      prompt: trimmed,
      ...(this.#workspaceIdentity === undefined
        ? {}
        : { workspace_identity: this.#workspaceIdentity }),
      ...(context.length === 0 ? {} : { context }),
    });
  }

  public async cancelTurn(turnId: string): Promise<SubmitReceipt> {
    return this.#submit("turn.cancel", {
      turn_id: requireIdentifier(turnId, "turn id"),
      reason: "user_interrupted",
    });
  }

  public async decideApproval(
    turnId: string,
    requestId: string,
    decision: ApprovalDecision,
    scope: ApprovalScope,
    expiresAt: string,
    planId?: string,
  ): Promise<SubmitReceipt> {
    if (!this.#trusted() && decision === "approve") {
      throw new Error("approve is unavailable in an untrusted workspace");
    }
    return this.#submit("approval.decision", {
      turn_id: requireIdentifier(turnId, "turn id"),
      request_id: requireIdentifier(requestId, "approval request id"),
      decision,
      scope,
      expires_at: requireTimestamp(expiresAt),
      ...(planId === undefined ? {} : { plan_id: requirePlanID(planId) }),
    });
  }

  public async replyInput(
    turnId: string,
    requestId: string,
    answer: string,
  ): Promise<SubmitReceipt> {
    if (answer.length > 64 << 10) {
      throw new Error("input answer exceeds 65536 characters");
    }
    return this.#submit("input.reply", {
      turn_id: requireIdentifier(turnId, "turn id"),
      request_id: requireIdentifier(requestId, "input request id"),
      answer,
    });
  }

  public async profile(): Promise<SessionProfileSnapshot> {
    const raw = await this.#transport.request("session/profile/get", {
      sessionId: this.#sessionId,
    });
    return decodeSessionProfileSnapshot(raw);
  }

  public async updateProfile(
    expectedRevision: number,
    patch: SessionProfilePatch,
  ): Promise<SessionProfileUpdate> {
    if (!Number.isSafeInteger(expectedRevision) || expectedRevision <= 0) {
      throw new Error("expected profile revision is invalid");
    }
    if (!this.#trusted() &&
      (patch.approval_posture === "auto" ||
        patch.approval_posture === "bypass")) {
      throw new Error(
        "approval escalation is unavailable in an untrusted workspace",
      );
    }
    const raw = await this.#transport.request("session/profile/update", {
      sessionId: this.#sessionId,
      expectedRevision,
      patch: validateSessionProfilePatch(patch),
    });
    return decodeSessionProfileUpdate(raw);
  }

  async #submit(kind: string, payload: unknown): Promise<SubmitReceipt> {
    const raw = await this.#transport.request("session/submit", {
      sessionId: this.#sessionId,
      operation: { kind, payload },
      idempotencyKey: randomUUID(),
    });
    return decodeSubmitReceipt(raw);
  }
}

function requirePlanID(value: string): string {
  if (!/^[0-9a-f]{64}$/u.test(value)) {
    throw new Error("edit plan id is invalid");
  }
  return value;
}

function decodeSubmitReceipt(value: unknown): SubmitReceipt {
  if (!isObject(value) ||
    value["accepted"] !== true ||
    typeof value["operationId"] !== "string" ||
    typeof value["turnId"] !== "string" ||
    typeof value["itemId"] !== "string") {
    throw new Error("Runtime returned an invalid submit receipt");
  }
  return {
    operationId: value["operationId"],
    turnId: value["turnId"],
    itemId: value["itemId"],
  };
}

function requireIdentifier(value: string, name: string): string {
  if (value.length === 0 || value.length > 256) {
    throw new Error(`${name} is invalid`);
  }
  return value;
}

function requireTimestamp(value: string): string {
  if (value.length === 0 || !Number.isFinite(Date.parse(value))) {
    throw new Error("approval expiry is invalid");
  }
  return value;
}

function isObject(value: unknown): value is Readonly<Record<string, unknown>> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}
