import type { SessionToolCatalog } from "../protocol/generated.js";

type JsonObject = Readonly<Record<string, unknown>>;

const sourceKinds = new Set(["builtin", "mcp", "plugin", "skill", "dynamic"]);
const availabilities = new Set(["available", "unavailable", "deferred"]);
const capabilities = new Set([
  "read", "write", "process", "network", "plugin", "unknown",
]);
const accessModes = new Set(["read", "write", "tree", "unknown"]);
const sandboxes = new Set(["none", "strong", "unknown"]);
const risks = new Set(["low", "medium", "high", "critical", "unknown"]);
const policyStates = new Set([
  "allowed", "requires_approval", "denied", "deferred",
]);
const constitutionStates = new Set(["allowed", "denied", "deferred"]);
const states = new Set([
  "eager", "deferred", "materialized", "unavailable", "revoked",
]);

export function decodeSessionToolCatalog(
  value: unknown,
): SessionToolCatalog {
  const object = requireObject(value, "session tool catalog");
  requireKeys(object, [
    "version", "catalog_id", "generation", "digest", "tools",
  ]);
  if (!Array.isArray(object["tools"]) || object["tools"].length > 4096) {
    throw new Error("session tool catalog tools are invalid");
  }
  const tools = object["tools"].map(decodeTool);
  const ids = new Set(tools.map((tool) => tool.id));
  if (ids.size !== tools.length) {
    throw new Error("session tool catalog contains duplicate tool ids");
  }
  return {
    version: positiveInteger(object["version"], "catalog version"),
    catalog_id: boundedString(object["catalog_id"], "catalog id"),
    generation: positiveInteger(object["generation"], "catalog generation"),
    digest: boundedString(object["digest"], "catalog digest"),
    tools,
  };
}

function decodeTool(value: unknown): SessionToolCatalog["tools"][number] {
  const object = requireObject(value, "session tool");
  requireKeys(object, [
    "id", "name", "description", "source_kind", "source_label",
    "capability", "access_mode", "risk_level", "sandbox_requirement",
    "policy_state", "policy_reason", "constitution_state",
    "constitution_reason", "availability", "state", "revision", "enabled",
    "guarded",
  ], ["unavailable_reason"]);
  const sourceKind = boundedString(object["source_kind"], "tool source kind");
  if (!sourceKinds.has(sourceKind)) {
    throw new Error("session tool source kind is invalid");
  }
  const id = boundedString(object["id"], "tool id");
  if (!id.startsWith(`${sourceKind}:`)) {
    throw new Error("session tool identity is not bound to its source");
  }
  const availability = boundedString(
    object["availability"],
    "tool availability",
  );
  if (!availabilities.has(availability)) {
    throw new Error("session tool availability is invalid");
  }
  if (availability === "unavailable" &&
    object["unavailable_reason"] === undefined) {
    throw new Error("session tool unavailable reason is missing");
  }
  const capability = finiteString(
    object["capability"], "tool capability", capabilities,
  );
  const accessMode = finiteString(
    object["access_mode"], "tool access mode", accessModes,
  );
  const sandbox = finiteString(
    object["sandbox_requirement"], "tool sandbox requirement", sandboxes,
  );
  const riskLevel = finiteString(
    object["risk_level"], "tool risk level", risks,
  );
  const policyState = finiteString(
    object["policy_state"], "tool policy state", policyStates,
  );
  const constitutionState = finiteString(
    object["constitution_state"],
    "tool constitution state",
    constitutionStates,
  );
  const state = finiteString(object["state"], "tool state", states);
  const guarded = boolean(object["guarded"], "tool guarded");
  if (!guarded) {
    throw new Error("session tool is not guarded");
  }
  return {
    id,
    name: boundedString(object["name"], "tool name"),
    description: boundedText(object["description"], "tool description", 4096),
    source_kind: sourceKind,
    source_label: boundedString(object["source_label"], "tool source label"),
    capability,
    access_mode: accessMode,
    risk_level: riskLevel,
    sandbox_requirement: sandbox,
    policy_state: policyState,
    policy_reason: boundedText(
      object["policy_reason"], "tool policy reason", 4096,
    ),
    constitution_state: constitutionState,
    constitution_reason: boundedText(
      object["constitution_reason"], "tool constitution reason", 4096,
    ),
    availability,
    ...(object["unavailable_reason"] === undefined
      ? {}
      : {
          unavailable_reason: boundedText(
            object["unavailable_reason"],
            "tool unavailable reason",
            4096,
          ),
        }),
    state,
    revision: positiveInteger(object["revision"], "tool revision"),
    enabled: boolean(object["enabled"], "tool enabled"),
    guarded,
  };
}

function requireObject(value: unknown, name: string): JsonObject {
  if (typeof value !== "object" || value === null || Array.isArray(value)) {
    throw new Error(`${name} is invalid`);
  }
  return value as JsonObject;
}

function requireKeys(
  value: JsonObject,
  required: readonly string[],
  optional: readonly string[] = [],
): void {
  const allowed = new Set([...required, ...optional]);
  if (Object.keys(value).some((key) => !allowed.has(key))) {
    throw new Error("session tool catalog contains unknown fields");
  }
  for (const key of required) {
    if (!Object.hasOwn(value, key)) {
      throw new Error(`session tool catalog is missing ${key}`);
    }
  }
}

function boundedString(
  value: unknown,
  name: string,
  maximum = 256,
): string {
  if (typeof value !== "string" || value.length === 0 ||
    value.length > maximum || /[\0\r\n]/u.test(value)) {
    throw new Error(`${name} is invalid`);
  }
  return value;
}

function boundedText(
  value: unknown,
  name: string,
  maximum: number,
): string {
  if (typeof value !== "string" || value.length === 0 ||
    value.length > maximum || value.includes("\0")) {
    throw new Error(`${name} is invalid`);
  }
  return value;
}

function finiteString(
  value: unknown,
  name: string,
  allowed: ReadonlySet<string>,
): string {
  const result = boundedString(value, name);
  if (!allowed.has(result)) {
    throw new Error(`${name} is invalid`);
  }
  return result;
}

function positiveInteger(value: unknown, name: string): number {
  if (!Number.isSafeInteger(value) || (value as number) <= 0) {
    throw new Error(`${name} is invalid`);
  }
  return value as number;
}

function boolean(value: unknown, name: string): boolean {
  if (typeof value !== "boolean") {
    throw new Error(`${name} is invalid`);
  }
  return value;
}
