import type {
  SessionProfilePatch,
  SessionProfileSnapshot,
  SessionProfileUpdate,
} from "../protocol/generated.js";

type JsonObject = Readonly<Record<string, unknown>>;

export function decodeSessionProfileSnapshot(
  value: unknown,
): SessionProfileSnapshot {
  const object = requireObject(value, "session profile snapshot");
  requireKeys(object, ["profile", "capabilities"]);
  const profile = decodeProfile(object["profile"]);
  const capabilities = requireObject(
    object["capabilities"],
    "session profile capabilities",
  );
  requireKeys(capabilities, [
    "provider",
    "model",
    "model_capabilities",
    "mutable_fields",
  ]);
  const modelCapabilities = requireObject(
    capabilities["model_capabilities"],
    "model capabilities",
  );
  requireKeys(modelCapabilities, [
    "streaming",
    "reasoning",
    "tool_calls",
    "native_search",
    "vision",
    "image_input",
    "prompt_cache",
  ], ["reasoning_efforts"]);
  const result = {
    profile,
    capabilities: {
      provider: requireString(capabilities["provider"], "capability provider"),
      model: requireString(capabilities["model"], "capability model"),
      model_capabilities: {
        streaming: requireBoolean(modelCapabilities["streaming"], "streaming"),
        reasoning: requireBoolean(modelCapabilities["reasoning"], "reasoning"),
        tool_calls: requireBoolean(modelCapabilities["tool_calls"], "tool calls"),
        native_search: requireBoolean(modelCapabilities["native_search"], "native search"),
        vision: requireBoolean(modelCapabilities["vision"], "vision"),
        image_input: requireBoolean(modelCapabilities["image_input"], "image input"),
        prompt_cache: requireBoolean(modelCapabilities["prompt_cache"], "prompt cache"),
        ...(modelCapabilities["reasoning_efforts"] === undefined
          ? {}
          : {
              reasoning_efforts: requireStrings(
                modelCapabilities["reasoning_efforts"],
                "reasoning efforts",
              ),
            }),
      },
      mutable_fields: requireStrings(
        capabilities["mutable_fields"],
        "mutable profile fields",
      ),
    },
  } satisfies SessionProfileSnapshot;
  if (result.capabilities.provider !== profile.provider ||
    result.capabilities.model !== profile.model) {
    throw new Error("session profile capabilities do not match its route");
  }
  return result;
}

export function decodeSessionProfileUpdate(
  value: unknown,
): SessionProfileUpdate {
  const object = requireObject(value, "session profile update");
  requireKeys(object, ["profile", "prompt_cache_reset"], ["reset_reason"]);
  return {
    profile: decodeProfile(object["profile"]),
    prompt_cache_reset: requireBoolean(
      object["prompt_cache_reset"],
      "prompt cache reset",
    ),
    ...(object["reset_reason"] === undefined
      ? {}
      : {
          reset_reason: requireString(
            object["reset_reason"],
            "prompt cache reset reason",
          ),
        }),
  };
}

export function validateSessionProfilePatch(
  patch: SessionProfilePatch,
): SessionProfilePatch {
  if (Object.keys(patch).length === 0) {
    throw new Error("session profile patch must not be empty");
  }
  return patch;
}

function decodeProfile(value: unknown): SessionProfileSnapshot["profile"] {
  const object = requireObject(value, "session profile");
  requireKeys(object, [
    "version",
    "revision",
    "mode",
    "provider",
    "model",
    "approval_posture",
    "execution_target",
    "max_steps",
    "prompt_cache_revision",
  ], ["reasoning_effort", "enabled_tool_ids"]);
  return {
    version: requirePositiveInteger(object["version"], "profile version"),
    revision: requirePositiveInteger(object["revision"], "profile revision"),
    mode: requireString(object["mode"], "profile mode"),
    provider: requireString(object["provider"], "profile provider"),
    model: requireString(object["model"], "profile model"),
    ...(object["reasoning_effort"] === undefined
      ? {}
      : {
          reasoning_effort: requireString(
            object["reasoning_effort"],
            "reasoning effort",
            true,
          ),
        }),
    ...(object["enabled_tool_ids"] === undefined
      ? {}
      : {
          enabled_tool_ids: requireStrings(
            object["enabled_tool_ids"],
            "enabled tool ids",
          ),
        }),
    approval_posture: requireString(
      object["approval_posture"],
      "approval posture",
    ),
    execution_target: requireString(
      object["execution_target"],
      "execution target",
    ),
    max_steps: requirePositiveInteger(object["max_steps"], "max steps"),
    prompt_cache_revision: requirePositiveInteger(
      object["prompt_cache_revision"],
      "prompt cache revision",
    ),
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
  for (const key of Object.keys(value)) {
    if (!allowed.has(key)) {
      throw new Error(`session profile contains unknown field ${key}`);
    }
  }
  for (const key of required) {
    if (!Object.hasOwn(value, key)) {
      throw new Error(`session profile is missing ${key}`);
    }
  }
}

function requireString(
  value: unknown,
  name: string,
  allowEmpty = false,
): string {
  if (typeof value !== "string" ||
    (!allowEmpty && value.length === 0) ||
    value.length > 256) {
    throw new Error(`${name} is invalid`);
  }
  return value;
}

function requireStrings(value: unknown, name: string): readonly string[] {
  if (!Array.isArray(value) || value.length > 512 ||
    value.some((entry) => typeof entry !== "string" ||
      entry.length === 0 || entry.length > 256)) {
    throw new Error(`${name} is invalid`);
  }
  return value as readonly string[];
}

function requireBoolean(value: unknown, name: string): boolean {
  if (typeof value !== "boolean") {
    throw new Error(`${name} is invalid`);
  }
  return value;
}

function requirePositiveInteger(value: unknown, name: string): number {
  if (!Number.isSafeInteger(value) || (value as number) <= 0) {
    throw new Error(`${name} is invalid`);
  }
  return value as number;
}
