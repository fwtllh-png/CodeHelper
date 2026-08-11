import type {
  ChatHostMessage,
  ChatPatchMessage,
  ChatSnapshotMessage,
} from "../contract.js";

export class ChatWebviewStore {
  #snapshot: ChatSnapshotMessage | undefined;

  public current(): ChatSnapshotMessage | undefined {
    return this.#snapshot;
  }

  public apply(
    message: Extract<
      ChatHostMessage,
      { readonly type: "snapshot" | "patch" }
    >,
  ): ChatSnapshotMessage {
    if (message.type === "snapshot") {
      if (this.#snapshot !== undefined &&
        message.revision <= this.#snapshot.revision) {
        throw new Error("Chat Snapshot Revision did not advance");
      }
      this.#snapshot = message;
      return message;
    }
    const current = this.#snapshot;
    if (current === undefined || message.baseRevision !== current.revision ||
      message.revision <= message.baseRevision) {
      throw new Error("Chat Patch base Revision is stale");
    }
    const next = applyPatch(current, message);
    this.#snapshot = next;
    return next;
  }
}

export function transcriptTurnsAffectedByPatch(
  snapshot: ChatSnapshotMessage,
  patch: ChatPatchMessage,
): ReadonlySet<string> {
  const affected = new Set<string>();
  let runtimeChanged = false;
  for (const operation of patch.operations) {
    if (operation.kind === "turn.upsert") {
      affected.add(operation.turn.id);
    } else if (operation.kind === "runtime.replace") {
      runtimeChanged = true;
    }
  }
  if (runtimeChanged) {
    for (const turn of snapshot.snapshot.turns) {
      if (turn.workspaceChange !== undefined) affected.add(turn.id);
    }
  }
  return affected;
}

function applyPatch(
  current: ChatSnapshotMessage,
  patch: ChatPatchMessage,
): ChatSnapshotMessage {
  const turns = new Map(
    current.snapshot.turns.map((turn) => [turn.id, turn]),
  );
  let runtime = current.runtime;
  let presentation = current.presentation;
  let composer = current.composer;
  let resources = current.resources;
  for (const operation of patch.operations) {
    switch (operation.kind) {
      case "turn.upsert":
        turns.set(operation.turn.id, operation.turn);
        break;
      case "turn.remove":
        turns.delete(operation.turnId);
        break;
      case "runtime.replace":
        runtime = operation.runtime;
        presentation = operation.presentation;
        break;
      case "composer.replace":
        composer = operation.composer;
        break;
      case "resources.replace":
        resources = operation.resources;
        break;
    }
  }
  const activeTurnId = [...turns.values()].find((turn) =>
    turn.status === "running" ||
    turn.status === "awaiting_approval" ||
    turn.status === "awaiting_input")?.id;
  return {
    type: "snapshot",
    version: current.version,
    revision: patch.revision,
    snapshot: {
      turns: [...turns.values()],
      ...(activeTurnId === undefined ? {} : { activeTurnId }),
    },
    resources,
    runtime,
    presentation,
    ...(composer === undefined ? {} : { composer }),
  };
}
