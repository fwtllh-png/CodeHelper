import { createHash } from "node:crypto";
import { posix } from "node:path";

import type { EditPlanCard } from "../edits/model.js";
import type { MarkdownNode } from "./markdown.js";
import type {
  ChatSnapshot,
  ContextReceiptCard,
  ContextSelectionCard,
} from "./projector.js";

export type ResourceKind =
  | "file"
  | "range"
  | "directory"
  | "symbol"
  | "diagnostic"
  | "diff";

export interface ResourcePosition {
  readonly line: number;
  readonly character: number;
}

export interface ResourceRange {
  readonly start: ResourcePosition;
  readonly end: ResourcePosition;
}

export interface ResourceReference {
  readonly id: string;
  readonly rootId: string;
  readonly kind: ResourceKind;
  readonly path: string;
  readonly range?: ResourceRange;
  readonly symbol?: string;
  readonly plan?: EditPlanCard;
  readonly fileIndex?: number;
}

export interface ResourceView {
  readonly id: string;
  readonly kind: ResourceKind;
  readonly label: string;
  readonly path: string;
  readonly detail?: string;
}

export interface ResourceProjection {
  readonly snapshot: ChatSnapshot;
  readonly references: readonly ResourceReference[];
  readonly views: readonly ResourceView[];
}

export function projectChatResources(
  snapshot: ChatSnapshot,
  rootId: string,
  sessionId: string,
): ResourceProjection {
  const references = new Map<string, ResourceReference>();
  const turns = snapshot.turns.map((turn) => {
    const contextReceipts = turn.contextReceipts.map((receipt) => {
      if (receipt.path.length === 0) {
        return { ...receipt };
      }
      const reference = receiptReference(rootId, sessionId, receipt);
      references.set(reference.id, reference);
      return { ...receipt, resourceId: reference.id };
    });
    const contextSelections = turn.contextSelections.map((selection) => {
      const reference = selectionReference(rootId, sessionId, selection);
      references.set(reference.id, reference);
      return { ...selection, resourceId: reference.id };
    });
    const approvals = turn.approvals.map((approval) => ({
      ...approval,
      ...(approval.editPlan === undefined
        ? {}
        : {
            editPlan: {
              ...approval.editPlan,
              files: approval.editPlan.files.map((file, fileIndex) => {
                const plan = approval.editPlan;
                if (plan === undefined) {
                  throw new Error("edit plan changed during resource projection");
                }
                const reference = resource({
                  rootId,
                  sessionId,
                  kind: "diff",
                  path: file.path,
                  plan,
                  fileIndex,
                });
                references.set(reference.id, reference);
                return { ...file, resourceId: reference.id };
              }),
            },
          }),
    }));
    return {
      ...turn,
      contextReceipts,
      contextSelections,
      approvals,
    };
  });
  const all = [...references.values()];
  const linkedTurns = turns.map((turn) => ({
    ...turn,
    outputMarkdown: linkMarkdownResources(turn.outputMarkdown, all),
    reasoningMarkdown: linkMarkdownResources(turn.reasoningMarkdown, all),
  }));
  return {
    snapshot: {
      turns: linkedTurns,
      ...(snapshot.activeTurnId === undefined
        ? {}
        : { activeTurnId: snapshot.activeTurnId }),
    },
    references: all,
    views: all.map(resourceView),
  };
}

export function validateResourceReference(
  reference: ResourceReference,
): ResourceReference {
  if (!/^[0-9a-f]{64}$/u.test(reference.id) ||
    !/^[0-9a-f]{64}$/u.test(reference.rootId)) {
    throw new Error("resource identity is invalid");
  }
  validateWorkspacePath(reference.path);
  if (reference.range !== undefined) validateRange(reference.range);
  if ((reference.kind === "range" || reference.kind === "symbol") &&
    reference.range === undefined) {
    throw new Error("resource range is required");
  }
  if (reference.kind === "symbol" &&
    (reference.symbol === undefined || reference.symbol.length > 512)) {
    throw new Error("resource symbol is invalid");
  }
  if (reference.kind === "diff" &&
    (reference.plan === undefined ||
      reference.fileIndex === undefined ||
      !Number.isSafeInteger(reference.fileIndex) ||
      reference.fileIndex < 0 ||
      reference.fileIndex >= reference.plan.files.length ||
      reference.plan.files[reference.fileIndex]?.path !== reference.path)) {
    throw new Error("resource diff identity is invalid");
  }
  return reference;
}

export function validateWorkspacePath(value: string): string {
  if (value.length === 0 || value.length > 4096 ||
    value.includes("\\") || value.includes("\0") || value.includes(":") ||
    value.startsWith("/") || /^[A-Za-z]:/u.test(value)) {
    throw new Error("resource path is invalid");
  }
  const normalized = posix.normalize(value);
  if (normalized === "." || normalized === ".." ||
    normalized.startsWith("../") || normalized !== value ||
    value.split("/").some((part) => part === "" || part === "." || part === "..")) {
    throw new Error("resource path escapes the workspace");
  }
  return value;
}

function receiptReference(
  rootId: string,
  sessionId: string,
  receipt: ContextReceiptCard,
): ResourceReference {
  const range = receipt.navigationRange;
  const kind: ResourceKind = receipt.kind === "symbol"
    ? "symbol"
    : receipt.kind === "diagnostics"
      ? "diagnostic"
      : range === undefined ? "file" : "range";
  return resource({
    rootId,
    sessionId,
    kind,
    path: receipt.path,
    ...(range === undefined ? {} : { range }),
    ...(receipt.symbolName === undefined ? {} : { symbol: receipt.symbolName }),
  });
}

function selectionReference(
  rootId: string,
  sessionId: string,
  selection: ContextSelectionCard,
): ResourceReference {
  return resource({
    rootId,
    sessionId,
    kind: selection.kind === "directory" ? "directory" : "file",
    path: selection.path,
  });
}

function resource(
  value: Omit<ResourceReference, "id"> & { readonly sessionId: string },
): ResourceReference {
  const { sessionId, ...reference } = value;
  const identity = JSON.stringify({
    rootId: reference.rootId,
    sessionId,
    kind: reference.kind,
    path: reference.path,
    range: reference.range,
    symbol: reference.symbol,
    planId: reference.plan?.id,
    fileIndex: reference.fileIndex,
  });
  return validateResourceReference({
    ...reference,
    id: createHash("sha256").update(identity).digest("hex"),
  });
}

function resourceView(reference: ResourceReference): ResourceView {
  const location = reference.range === undefined
    ? undefined
    : `${String(reference.range.start.line + 1)}:` +
      String(reference.range.start.character + 1);
  const detail = reference.symbol ??
    (location === undefined ? undefined : `Line ${location}`);
  return {
    id: reference.id,
    kind: reference.kind,
    label: posix.basename(reference.path),
    path: reference.path,
    ...(detail === undefined ? {} : { detail }),
  };
}

function linkMarkdownResources(
  nodes: readonly MarkdownNode[],
  references: readonly ResourceReference[],
): readonly MarkdownNode[] {
  const candidates = uniqueReferenceLabels(references);
  return nodes.map((node): MarkdownNode => {
    if (node.kind === "text") return node;
    const children = linkMarkdownResources(node.children, references);
    if (node.tag !== "code" || children.length !== 1 ||
      children[0]?.kind !== "text") {
      return { ...node, children };
    }
    const reference = candidates.get(children[0].text.trim());
    return reference === undefined
      ? { ...node, children }
      : { ...node, children, resourceId: reference.id };
  });
}

function uniqueReferenceLabels(
  references: readonly ResourceReference[],
): ReadonlyMap<string, ResourceReference> {
  const values = new Map<string, ResourceReference | undefined>();
  for (const reference of references) {
    const labels = new Set([
      reference.path,
      posix.basename(reference.path),
      ...(reference.symbol === undefined ? [] : [reference.symbol]),
      ...(reference.range === undefined
        ? []
        : [
            `${reference.path}:${String(reference.range.start.line + 1)}`,
            `${posix.basename(reference.path)}:` +
              String(reference.range.start.line + 1),
          ]),
    ]);
    for (const label of labels) {
      values.set(
        label,
        values.has(label) ? undefined : reference,
      );
    }
  }
  return new Map(
    [...values.entries()].filter(
      (entry): entry is [string, ResourceReference] => entry[1] !== undefined,
    ),
  );
}

function validateRange(value: ResourceRange): void {
  for (const position of [value.start, value.end]) {
    if (!Number.isSafeInteger(position.line) || position.line < 0 ||
      !Number.isSafeInteger(position.character) || position.character < 0) {
      throw new Error("resource range is invalid");
    }
  }
  if (value.end.line < value.start.line ||
    (value.end.line === value.start.line &&
      value.end.character < value.start.character)) {
    throw new Error("resource range is reversed");
  }
}
