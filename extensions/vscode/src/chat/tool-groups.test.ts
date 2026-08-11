import assert from "node:assert/strict";
import test from "node:test";

import type { SessionToolCatalog } from "../protocol/generated.js";
import { groupToolsForPicker } from "./tool-groups.js";

type Tool = SessionToolCatalog["tools"][number];

void test("tool picker folds Agent lifecycle operations into one group", () => {
  const tools = [
    tool("agent", "write"),
    tool("agent_close", "write"),
    tool("agent_list", "read"),
    tool("file_read", "read"),
  ];
  const entries = groupToolsForPicker(tools);

  assert.equal(entries.length, 2);
  assert.equal(entries[0]?.kind, "group");
  const group = entries[0];
  assert.ok(group);
  assert.equal(group.group.label, "Agent");
  assert.equal(group.group.capabilityLabel, "Read/Write");
  assert.deepEqual(
    group.group.tools.map((candidate) => candidate.name),
    ["agent", "agent_close", "agent_list"],
  );
  assert.equal(entries[1]?.kind, "tool");
});

void test("tool picker leaves a lone Agent tool ungrouped", () => {
  const entries = groupToolsForPicker([
    tool("agent", "write"),
    tool("file_read", "read"),
  ]);
  assert.deepEqual(entries.map((entry) => entry.kind), ["tool", "tool"]);
});

function tool(name: string, capability: "read" | "write"): Tool {
  return {
    id: `builtin:${name}`,
    name,
    description: `${name} description`,
    source_kind: "builtin",
    source_label: "CodeHelper",
    capability,
    access_mode: capability,
    risk_level: "low",
    sandbox_requirement: "none",
    policy_state: "allowed",
    policy_reason: "allowed",
    constitution_state: "allowed",
    constitution_reason: "allowed",
    availability: "available",
    state: "eager",
    revision: 1,
    enabled: true,
    guarded: true,
  };
}
