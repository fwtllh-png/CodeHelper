import {
  isUnknownEvent,
  type DecodedEvent,
} from "../protocol/decode.js";
import type { ApprovalRequiredData } from "../protocol/generated.js";

export interface EditPlanCard {
  readonly id: string;
  readonly diff: string;
  readonly files: readonly EditPlanFileCard[];
}

export interface EditPlanFileCard {
  readonly path: string;
  readonly kind: "created" | "modified" | "deleted";
  readonly before: string;
  readonly after: string;
  readonly beforeExists: boolean;
  readonly afterExists: boolean;
  readonly beforeDigest: string;
  readonly afterDigest: string;
  readonly resourceId?: string;
}

export interface PlanAnnotation {
  readonly kind: "diagnostics" | "verification";
  readonly status: string;
  readonly detail: string;
}

export interface EditPlanReview {
  readonly plan: EditPlanCard;
  readonly requestId: string;
  readonly turnId: string;
  readonly tool: string;
  readonly allowedScopes: readonly string[];
  readonly expiresAt: string;
  readonly status: "pending" | "approve" | "deny" | "cancel";
  readonly sequence: number;
  readonly annotations: Readonly<Record<string, readonly PlanAnnotation[]>>;
}

interface MutableReview {
  plan: EditPlanCard;
  requestId: string;
  turnId: string;
  tool: string;
  allowedScopes: string[];
  expiresAt: string;
  status: EditPlanReview["status"];
  sequence: number;
  annotations: Map<string, PlanAnnotation[]>;
}

const maxPlanFiles = 128;
const maxPlanHistory = 16;
const maxPlanContentBytes = 1 << 20;
const maxPlanTotalBytes = 8 << 20;
const maxPlanDiffBytes = 4 << 20;
const maxPlanPathBytes = 4096;
const maxAnnotationsPerFile = 32;
const maxAnnotationText = 4096;

type WireEditPlan = NonNullable<ApprovalRequiredData["edit_plan"]>;

export class EditPlanProjector {
  readonly #reviews = new Map<string, MutableReview>();
  #lastSequence = 0;

  public apply(event: DecodedEvent): boolean {
    if (event.sequence <= this.#lastSequence) {
      return false;
    }
    this.#lastSequence = event.sequence;
    if (isUnknownEvent(event)) {
      return false;
    }
    switch (event.kind) {
      case "approval.required":
        if (event.data.edit_plan === undefined) {
          return false;
        }
        this.#add({
          plan: projectEditPlan(event.data.edit_plan),
          requestId: event.data.request_id,
          turnId: event.turn_id,
          tool: bounded(event.data.tool, 256),
          allowedScopes: event.data.allowed_scopes.slice(0, 8),
          expiresAt: event.data.expires_at,
          status: "pending",
          sequence: event.sequence,
          annotations: new Map(),
        });
        return true;
      case "approval.resolved": {
        const review = this.#reviews.get(event.data.request_id);
        if (review === undefined) {
          return false;
        }
        review.status = decision(event.data.decision);
        review.sequence = event.sequence;
        return true;
      }
      case "diagnostics.result":
        return this.#diagnostics(event.turn_id, event.data.receipts);
      case "turn.verification":
        return this.#verification(
          event.turn_id,
          event.data.paths ?? [],
          event.data.status,
          event.data.message ??
            (event.data.checks ?? []).map((check) =>
              `${check.name}=${check.status}`).join(", "),
        );
      default:
        return false;
    }
  }

  public snapshot(): readonly EditPlanReview[] {
    const all = [...this.#reviews.values()]
      .sort((left, right) => right.sequence - left.sequence);
    const pending = all.filter((review) => review.status === "pending");
    return (pending.length > 0 ? pending : all.slice(0, 1)).map(snapshotReview);
  }

  public find(planId: string): EditPlanReview | undefined {
    const review = [...this.#reviews.values()].find(
      (candidate) => candidate.plan.id === planId,
    );
    return review === undefined ? undefined : snapshotReview(review);
  }

  public findRequest(requestId: string): EditPlanReview | undefined {
    const review = this.#reviews.get(requestId);
    return review === undefined ? undefined : snapshotReview(review);
  }

  #add(review: MutableReview): void {
    this.#reviews.set(review.requestId, review);
    if (this.#reviews.size <= maxPlanHistory) {
      return;
    }
    const removable = [...this.#reviews.values()]
      .filter((candidate) => candidate.status !== "pending")
      .sort((left, right) => left.sequence - right.sequence)[0];
    if (removable === undefined) {
      this.#reviews.delete(review.requestId);
      throw new Error(`edit plan projection exceeds ${String(maxPlanHistory)} pending plans`);
    }
    this.#reviews.delete(removable.requestId);
  }

  #diagnostics(
    turnId: string,
    receipts: readonly {
      readonly path: string;
      readonly status: string;
      readonly message?: string;
      readonly diagnostics: readonly unknown[];
    }[],
  ): boolean {
    let changed = false;
    for (const receipt of receipts) {
      const detail = receipt.message ??
        `${String(receipt.diagnostics.length)} diagnostics`;
      changed = this.#annotate(turnId, [receipt.path], {
        kind: "diagnostics",
        status: bounded(receipt.status, 128),
        detail: bounded(detail, maxAnnotationText),
      }) || changed;
    }
    return changed;
  }

  #verification(
    turnId: string,
    paths: readonly string[],
    status: string,
    detail: string,
  ): boolean {
    const review = this.#reviewsForTurn(turnId)[0];
    if (review === undefined) {
      return false;
    }
    return this.#annotate(
      turnId,
      paths.length === 0
        ? review.plan.files.map((file) => file.path)
        : paths,
      {
        kind: "verification",
        status: bounded(status, 128),
        detail: bounded(detail, maxAnnotationText),
      },
    );
  }

  #annotate(
    turnId: string,
    paths: readonly string[],
    annotation: PlanAnnotation,
  ): boolean {
    let changed = false;
    const pathSet = new Set(paths);
    for (const review of this.#reviewsForTurn(turnId)) {
      for (const file of review.plan.files) {
        if (!pathSet.has(file.path)) {
          continue;
        }
        const annotations = review.annotations.get(file.path) ?? [];
        if (annotations.some((candidate) =>
          candidate.kind === annotation.kind &&
          candidate.status === annotation.status &&
          candidate.detail === annotation.detail)) {
          continue;
        }
        if (annotations.length >= maxAnnotationsPerFile) {
          continue;
        }
        annotations.push(annotation);
        review.annotations.set(file.path, annotations);
        changed = true;
      }
    }
    return changed;
  }

  #reviewsForTurn(turnId: string): MutableReview[] {
    return [...this.#reviews.values()].filter(
      (review) => review.turnId === turnId,
    );
  }
}

export function projectEditPlan(value: WireEditPlan): EditPlanCard {
  if (!/^[0-9a-f]{64}$/u.test(value.id) ||
    value.files.length === 0 ||
    value.files.length > maxPlanFiles ||
    Buffer.byteLength(value.diff, "utf8") > maxPlanDiffBytes) {
    throw new Error("edit plan identity, diff, or file count is invalid");
  }
  let totalBytes = Buffer.byteLength(value.diff, "utf8");
  const paths = new Set<string>();
  const files = value.files.map((file): EditPlanFileCard => {
    const kind = editKind(file.kind);
    const before = file.before ?? "";
    const after = file.after ?? "";
    const beforeBytes = Buffer.byteLength(before, "utf8");
    const afterBytes = Buffer.byteLength(after, "utf8");
    const contentBytes = beforeBytes + afterBytes;
    totalBytes += contentBytes;
    if (!validPlanPath(file.path) ||
      Buffer.byteLength(file.path, "utf8") > maxPlanPathBytes ||
      paths.has(file.path) ||
      beforeBytes > maxPlanContentBytes ||
      afterBytes > maxPlanContentBytes ||
      (!file.before_exists && before !== "") ||
      (!file.after_exists && after !== "") ||
      !consistentFileState(
        kind,
        file.before_exists,
        file.after_exists,
        file.before_digest,
        file.after_digest,
      )) {
      throw new Error("edit plan file path or content is invalid");
    }
    paths.add(file.path);
    return {
      path: file.path,
      kind,
      before,
      after,
      beforeExists: file.before_exists,
      afterExists: file.after_exists,
      beforeDigest: digest(file.before_digest),
      afterDigest: digest(file.after_digest),
    };
  });
  if (totalBytes > maxPlanTotalBytes) {
    throw new Error(`edit plan exceeds ${String(maxPlanTotalBytes)} projected bytes`);
  }
  return { id: value.id, diff: value.diff, files };
}

export function decodePlanFileTarget(value: unknown): {
  readonly rootId: string;
  readonly planId: string;
  readonly fileIndex: number;
} {
  const object = strictObject(
    value,
    ["rootId", "planId", "fileIndex"],
    "plan file target",
  );
  const rootId = rootID(object["rootId"]);
  const planId = object["planId"];
  const fileIndex = object["fileIndex"];
  if (typeof planId !== "string" || !/^[0-9a-f]{64}$/u.test(planId) ||
    !Number.isSafeInteger(fileIndex) || Number(fileIndex) < 0 ||
    Number(fileIndex) >= maxPlanFiles) {
    throw new Error("plan file target is invalid");
  }
  return { rootId, planId, fileIndex: Number(fileIndex) };
}

export function decodePlanDecisionTarget(value: unknown): {
  readonly rootId: string;
  readonly requestId: string;
  readonly decision: "approve" | "deny";
} {
  const object = strictObject(
    value,
    ["rootId", "requestId", "decision"],
    "plan decision target",
  );
  const rootId = rootID(object["rootId"]);
  const requestId = object["requestId"];
  const valueDecision = object["decision"];
  if (typeof requestId !== "string" ||
    requestId.length === 0 || requestId.length > 256 ||
    (valueDecision !== "approve" && valueDecision !== "deny")) {
    throw new Error("plan decision target is invalid");
  }
  return { rootId, requestId, decision: valueDecision };
}

function rootID(value: unknown): string {
  if (typeof value !== "string" || !/^[0-9a-f]{64}$/u.test(value)) {
    throw new Error("plan target workspace root is invalid");
  }
  return value;
}

function snapshotReview(review: MutableReview): EditPlanReview {
  return {
    plan: review.plan,
    requestId: review.requestId,
    turnId: review.turnId,
    tool: review.tool,
    allowedScopes: [...review.allowedScopes],
    expiresAt: review.expiresAt,
    status: review.status,
    sequence: review.sequence,
    annotations: Object.fromEntries(
      [...review.annotations].map(([path, annotations]) =>
        [path, annotations.map((annotation) => ({ ...annotation }))]),
    ),
  };
}

function decision(value: string): EditPlanReview["status"] {
  switch (value) {
    case "approve":
    case "deny":
    case "cancel":
      return value;
    default:
      throw new Error("edit plan approval decision is invalid");
  }
}

function editKind(value: string): EditPlanFileCard["kind"] {
  switch (value) {
    case "created":
    case "modified":
    case "deleted":
      return value;
    default:
      throw new Error("edit plan file kind is invalid");
  }
}

function digest(value: string): string {
  if (value !== "missing" && !/^[0-9a-f]{64}$/u.test(value)) {
    throw new Error("edit plan file digest is invalid");
  }
  return value;
}

function validPlanPath(value: string): boolean {
  if (value.length === 0 || value.startsWith("/") ||
    value.includes("\\") || value.includes("\0")) {
    return false;
  }
  return value.split("/").every(
    (segment) => segment !== "" && segment !== "." && segment !== "..",
  );
}

function consistentFileState(
  kind: EditPlanFileCard["kind"],
  beforeExists: boolean,
  afterExists: boolean,
  beforeDigest: string,
  afterDigest: string,
): boolean {
  const shape = kind === "created"
    ? !beforeExists && afterExists
    : kind === "deleted"
      ? beforeExists && !afterExists
      : beforeExists && afterExists;
  return shape &&
    (beforeExists ? beforeDigest !== "missing" : beforeDigest === "missing") &&
    (afterExists ? afterDigest !== "missing" : afterDigest === "missing");
}

function bounded(value: string, maximum: number): string {
  return value.length <= maximum ? value : `${value.slice(0, maximum - 1)}…`;
}

function strictObject(
  value: unknown,
  allowed: readonly string[],
  label: string,
): Readonly<Record<string, unknown>> {
  if (typeof value !== "object" || value === null || Array.isArray(value) ||
    Object.getPrototypeOf(value) !== Object.prototype) {
    throw new Error(`${label} must be a plain object`);
  }
  const object = value as Readonly<Record<string, unknown>>;
  if (Object.keys(object).some((key) => !allowed.includes(key))) {
    throw new Error(`${label} contains unknown fields`);
  }
  return object;
}
