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
    "display_name",
    "context_window",
    "max_output_tokens",
    "streaming",
    "reasoning",
    "tool_calls",
    "parallel_tool_calls",
    "native_search",
    "vision",
    "image_input",
    "prompt_cache",
    "credential_status",
    "availability",
    "selection_mode",
  ], [
    "reasoning_efforts",
    "default_reasoning_effort",
    "unavailable_reason",
  ]);
  const availability = finiteString(
    modelCapabilities["availability"],
    "model availability",
    new Set(["available", "unavailable"]),
  );
  if (availability === "unavailable" &&
    modelCapabilities["unavailable_reason"] === undefined) {
    throw new Error("unavailable model requires a reason");
  }
  const result = {
    profile,
    capabilities: {
      provider: requireString(capabilities["provider"], "capability provider"),
      model: requireString(capabilities["model"], "capability model"),
      model_capabilities: {
        display_name: requireString(
          modelCapabilities["display_name"],
          "model display name",
        ),
        context_window: requirePositiveInteger(
          modelCapabilities["context_window"],
          "model context window",
        ),
        max_output_tokens: requirePositiveInteger(
          modelCapabilities["max_output_tokens"],
          "model max output tokens",
        ),
        streaming: requireBoolean(modelCapabilities["streaming"], "streaming"),
        reasoning: requireBoolean(modelCapabilities["reasoning"], "reasoning"),
        tool_calls: requireBoolean(modelCapabilities["tool_calls"], "tool calls"),
        parallel_tool_calls: finiteString(
          modelCapabilities["parallel_tool_calls"],
          "parallel tool calls",
          new Set(["supported", "unsupported", "unknown"]),
        ),
        native_search: requireBoolean(modelCapabilities["native_search"], "native search"),
        vision: requireBoolean(modelCapabilities["vision"], "vision"),
        image_input: requireBoolean(modelCapabilities["image_input"], "image input"),
        prompt_cache: requireBoolean(modelCapabilities["prompt_cache"], "prompt cache"),
        credential_status: finiteString(
          modelCapabilities["credential_status"],
          "model credential status",
          new Set(["configured", "missing", "invalid", "unknown"]),
        ),
        availability,
        selection_mode: finiteString(
          modelCapabilities["selection_mode"],
          "model selection mode",
          new Set(["hot", "restart_required", "fixed"]),
        ),
        ...(modelCapabilities["reasoning_efforts"] === undefined
          ? {}
          : {
              reasoning_efforts: requireStrings(
                modelCapabilities["reasoning_efforts"],
                "reasoning efforts",
              ),
            }),
        ...(modelCapabilities["default_reasoning_effort"] === undefined
          ? {}
          : {
              default_reasoning_effort: requireString(
                modelCapabilities["default_reasoning_effort"],
                "default reasoning effort",
                true,
              ),
            }),
        ...(modelCapabilities["unavailable_reason"] === undefined
          ? {}
          : {
              unavailable_reason: requireString(
                modelCapabilities["unavailable_reason"],
                "model unavailable reason",
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
  const model = result.capabilities.model_capabilities;
  if (model.max_output_tokens > model.context_window) {
    throw new Error("model output limit exceeds its context window");
  }
  if (!model.reasoning &&
    ((model.reasoning_efforts?.length ?? 0) > 0 ||
      (model.default_reasoning_effort?.length ?? 0) > 0)) {
    throw new Error("non-reasoning model advertises reasoning effort");
  }
  if (model.default_reasoning_effort !== undefined &&
    !model.reasoning_efforts?.includes(model.default_reasoning_effort)) {
    throw new Error("default reasoning effort is not advertised");
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
    mode: finiteString(
      object["mode"],
      "profile mode",
      new Set(["plan", "act", "operate"]),
    ),
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
    execution_target: finiteString(
      object["execution_target"],
      "execution target",
      new Set(["local"]),
    ),
    max_steps: requirePositiveInteger(object["max_steps"], "max steps"),
    prompt_cache_revision: requirePositiveInteger(
      object["prompt_cache_revision"],
      "prompt cache revision",
    ),
  };
}

function finiteString(
  value: unknown,
  name: string,
  allowed: ReadonlySet<string>,
): string {
  const result = requireString(value, name);
  if (!allowed.has(result)) throw new Error(`${name} is invalid`);
  return result;
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
