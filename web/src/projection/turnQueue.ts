import type {QueuedTurn, RuntimeEvent} from "../protocol";

export function projectTurnQueue(
  initial: readonly QueuedTurn[],
  events: readonly RuntimeEvent[]
): readonly QueuedTurn[] {
  const items = new Map(initial.map((item) => [item.queue_id, item]));
  for (const event of events) {
    const queueID = stringValue(event.data.queue_id);
    if (!queueID) continue;
    switch (event.kind) {
      case "turn.queued":
        items.set(queueID, {
          queue_id: queueID,
          thread_id: event.thread_id,
          source_turn_id: event.turn_id,
          prompt: stringValue(event.data.prompt),
          display_prompt: stringValue(event.data.display_prompt) || undefined,
          intent: stringValue(event.data.intent) || undefined,
          workspace_identity: objectValue(event.data.workspace_identity),
          context: arrayValue(event.data.context),
          added_sequence: event.sequence,
          created_at: event.created_at,
          updated_at: event.created_at
        });
        break;
      case "turn.queue.updated": {
        const current = items.get(queueID);
        if (!current) break;
        items.set(queueID, {
          ...current,
          prompt: stringValue(event.data.prompt),
          display_prompt: stringValue(event.data.display_prompt) || undefined,
          updated_at: event.created_at
        });
        break;
      }
      case "turn.queue.removed":
      case "turn.started":
      case "turn.steered":
        items.delete(queueID);
        break;
    }
  }
  return [...items.values()].sort((left, right) =>
    left.added_sequence - right.added_sequence ||
    left.queue_id.localeCompare(right.queue_id)
  );
}

function stringValue(value: unknown): string {
  return typeof value === "string" ? value : "";
}

function objectValue<T extends object>(value: unknown): T | undefined {
  return value && typeof value === "object" && !Array.isArray(value)
    ? value as T
    : undefined;
}

function arrayValue<T>(value: unknown): T[] | undefined {
  return Array.isArray(value) ? value as T[] : undefined;
}
