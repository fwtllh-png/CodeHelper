import type { SessionToolCatalog } from "../protocol/generated.js";

export type SessionTool = SessionToolCatalog["tools"][number];

export interface ToolPickerGroup {
  readonly id: "agent";
  readonly label: "Agent";
  readonly tools: readonly SessionTool[];
  readonly capabilityLabel: string;
}

export type ToolPickerEntry =
  | { readonly kind: "tool"; readonly tool: SessionTool }
  | { readonly kind: "group"; readonly group: ToolPickerGroup };

export function groupToolsForPicker(
  tools: readonly SessionTool[],
): readonly ToolPickerEntry[] {
  const agentTools = tools.filter((tool) => isAgentTool(tool.name));
  if (agentTools.length < 2) {
    return tools.map((tool) => ({ kind: "tool", tool }));
  }
  const capabilities = new Set(agentTools.map((tool) => tool.capability));
  const capabilityLabel = ["read", "write"]
    .filter((capability) => capabilities.has(capability))
    .map(title)
    .join("/");
  const result: ToolPickerEntry[] = [];
  let grouped = false;
  for (const tool of tools) {
    if (!isAgentTool(tool.name)) {
      result.push({ kind: "tool", tool });
    } else if (!grouped) {
      result.push({
        kind: "group",
        group: {
          id: "agent",
          label: "Agent",
          tools: agentTools,
          capabilityLabel: capabilityLabel || "Mixed",
        },
      });
      grouped = true;
    }
  }
  return result;
}

function isAgentTool(name: string): boolean {
  return new Set([
    "spawn_agent",
    "send_message",
    "wait_agent",
    "list_agents",
    "followup_task",
    "interrupt_agent",
    "close_agent",
    "integrate_agent",
  ]).has(name);
}

function title(value: string): string {
  return value.length === 0
    ? value
    : value.charAt(0).toUpperCase() + value.slice(1);
}
