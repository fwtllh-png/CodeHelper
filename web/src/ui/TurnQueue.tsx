import {
  Check,
  ChevronDown,
  ListTodo,
  Pencil,
  Trash2,
  X,
  Zap
} from "lucide-react";
import {useState} from "react";

import type {QueuedTurn} from "../protocol";

export function TurnQueue({
  items,
  activeTurnID,
  onUpdate,
  onRemove,
  onPromote,
  onError
}: {
  items: readonly QueuedTurn[];
  activeTurnID?: string;
  onUpdate: (queueID: string, prompt: string) => Promise<void>;
  onRemove: (queueID: string) => Promise<void>;
  onPromote: (queueID: string, turnID: string) => Promise<void>;
  onError: (error: unknown) => void;
}) {
  const [expanded, setExpanded] = useState(true);
  const [editingID, setEditingID] = useState("");
  const [editingPrompt, setEditingPrompt] = useState("");
  const [busyID, setBusyID] = useState("");

  if (items.length === 0) return null;

  const run = async (queueID: string, action: () => Promise<void>) => {
    if (busyID) return;
    setBusyID(queueID);
    try {
      await action();
    } catch (error) {
      onError(error);
    } finally {
      setBusyID("");
    }
  };

  const save = async (item: QueuedTurn) => {
    const prompt = editingPrompt.trim();
    if (!prompt) return;
    await run(item.queue_id, async () => {
      await onUpdate(item.queue_id, prompt);
      setEditingID("");
      setEditingPrompt("");
    });
  };

  return (
    <section className="turnQueue" aria-label="Follow-up queue">
      <button
        type="button"
        className="turnQueueSummary"
        aria-expanded={expanded}
        onClick={() => setExpanded((value) => !value)}
      >
        <ListTodo size={15} />
        <span>{items.length} queued {items.length === 1 ? "message" : "messages"}</span>
        <ChevronDown size={15} data-expanded={expanded || undefined} />
      </button>
      {expanded && (
        <ol className="turnQueueList">
          {items.map((item, index) => {
            const editing = editingID === item.queue_id;
            const busy = busyID === item.queue_id;
            return (
              <li key={item.queue_id} className="turnQueueItem">
                <span className="turnQueueOrdinal">{index + 1}</span>
                {editing ? (
                  <input
                    aria-label={`Edit queued message ${index + 1}`}
                    value={editingPrompt}
                    autoFocus
                    onChange={(event) => setEditingPrompt(event.target.value)}
                    onKeyDown={(event) => {
                      if (event.key === "Enter") void save(item);
                      if (event.key === "Escape") setEditingID("");
                    }}
                  />
                ) : (
                  <span className="turnQueuePrompt">
                    {item.display_prompt || item.prompt}
                  </span>
                )}
                <span className="turnQueueActions">
                  {editing ? (
                    <>
                      <button
                        type="button"
                        aria-label={`Save queued message ${index + 1}`}
                        title="Save"
                        disabled={busy || !editingPrompt.trim()}
                        onClick={() => void save(item)}
                      >
                        <Check size={14} />
                      </button>
                      <button
                        type="button"
                        aria-label={`Cancel editing queued message ${index + 1}`}
                        title="Cancel"
                        disabled={busy}
                        onClick={() => setEditingID("")}
                      >
                        <X size={14} />
                      </button>
                    </>
                  ) : (
                    <>
                      {activeTurnID && (
                        <button
                          type="button"
                          aria-label={`Steer with queued message ${index + 1}`}
                          title="Steer current turn"
                          disabled={busy}
                          onClick={() => void run(
                            item.queue_id,
                            () => onPromote(item.queue_id, activeTurnID)
                          )}
                        >
                          <Zap size={14} />
                        </button>
                      )}
                      <button
                        type="button"
                        aria-label={`Edit queued message ${index + 1}`}
                        title="Edit"
                        disabled={busy}
                        onClick={() => {
                          setEditingID(item.queue_id);
                          setEditingPrompt(item.display_prompt || item.prompt);
                        }}
                      >
                        <Pencil size={14} />
                      </button>
                      <button
                        type="button"
                        aria-label={`Remove queued message ${index + 1}`}
                        title="Remove"
                        disabled={busy}
                        onClick={() => void run(
                          item.queue_id,
                          () => onRemove(item.queue_id)
                        )}
                      >
                        <Trash2 size={14} />
                      </button>
                    </>
                  )}
                </span>
              </li>
            );
          })}
        </ol>
      )}
    </section>
  );
}
