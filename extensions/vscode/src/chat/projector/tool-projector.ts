import { isUnknownEvent, type DecodedEvent } from "../../protocol/decode.js";
import {
  appendBounded, ensureToolTimelineItem, fileChangeKind, stringify, truncate,
  truncateField,
} from "./helpers.js";
import type { MutableTool, MutableTurn } from "./model.js";
export function projectTool(event: DecodedEvent, turn: MutableTurn): boolean {
  if (isUnknownEvent(event)) return false;
  switch (event.kind) {
    case "tool.start":
      turn.reasoningActive = false;
      turn.tools.set(event.data.call_id, {
        callId: event.data.call_id,
        tool: event.data.tool,
        status: "running",
        ...(event.data.arguments === undefined
          ? {}
          : { arguments: stringify(event.data.arguments) }),
        output: "",
        changes: [],
      });
      ensureToolTimelineItem(turn, event.sequence, event.data.call_id);
      return true;
    case "tool.output": {
      const tool = mutableTool(turn, event.data.call_id, event.data.tool);
      ensureToolTimelineItem(turn, event.sequence, event.data.call_id);
      tool.output = appendBounded(tool.output, event.data.chunk);
      return true;
    }
    case "tool.result": {
      const tool = mutableTool(turn, event.data.call_id, event.data.tool);
      ensureToolTimelineItem(turn, event.sequence, event.data.call_id);
      tool.output = truncate(event.data.output);
      tool.status = event.data.is_error ? "failed" : "completed";
      tool.changes = (event.data.changes ?? []).flatMap((change) => {
        const kind = fileChangeKind(change.kind);
        return kind === undefined || change.added < 0 || change.removed < 0
          ? []
          : [{
              path: truncateField(change.path, 4096),
              kind,
              added: change.added,
              removed: change.removed,
            }];
      });
      return true;
    }
    case "tool.state":
      if (event.data.text !== undefined) {
        const notice = truncate(`tool: ${event.data.state} ${event.data.text}`);
        turn.diagnosticNotices.push(notice);
        turn.timeline.push({
          id: `notice:${String(event.sequence)}`,
          sequence: event.sequence,
          kind: "notice",
          text: notice,
        });
      }
      return true;
    default:
      return false;
  }
}
function mutableTool(turn: MutableTurn, callId: string, toolName: string): MutableTool {
  let tool = turn.tools.get(callId);
  if (tool === undefined) {
    tool = {
      callId,
      tool: toolName,
      status: "running",
      output: "",
      changes: [],
    };
    turn.tools.set(callId, tool);
  }
  return tool;
}
