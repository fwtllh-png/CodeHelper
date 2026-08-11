import type {
  ModelCatalog,
  ProviderCatalog,
} from "../protocol/generated.js";
import { decodeSessionProfileSnapshot } from "./profile.js";

type JsonObject = Readonly<Record<string, unknown>>;

export function decodeProviderCatalog(value: unknown): ProviderCatalog {
  const object = requireObject(value, "provider catalog");
  requireKeys(object, ["version", "providers"]);
  if (object["version"] !== 1 || !Array.isArray(object["providers"]) ||
    object["providers"].length > 256) {
    throw new Error("provider catalog version or size is invalid");
  }
  const seen = new Set<string>();
  const providers = object["providers"].map((candidate) => {
    const row = requireObject(candidate, "provider catalog entry");
    requireKeys(row, [
      "id", "display_name", "selected", "availability",
    ], ["reason"]);
    const id = identifier(row["id"], "provider id");
    const availability = finite(
      row["availability"], "provider availability",
      new Set(["available", "unavailable"]),
    );
    const reason = row["reason"] === undefined
      ? undefined
      : text(row["reason"], "provider reason");
    if (availability === "unavailable" && reason === undefined) {
      throw new Error("unavailable provider requires a reason");
    }
    if (seen.has(id)) throw new Error("provider catalog contains duplicates");
    seen.add(id);
    return {
      id,
      display_name: text(row["display_name"], "provider display name"),
      selected: boolean(row["selected"], "provider selected"),
      availability,
      ...(reason === undefined ? {} : { reason }),
    };
  });
  return { version: 1, providers };
}

export function decodeModelCatalog(value: unknown): ModelCatalog {
  const object = requireObject(value, "model catalog");
  requireKeys(object, ["version", "models"]);
  if (object["version"] !== 1 || !Array.isArray(object["models"]) ||
    object["models"].length > 4096) {
    throw new Error("model catalog version or size is invalid");
  }
  const seen = new Set<string>();
  const models = object["models"].map((candidate) => {
    const row = requireObject(candidate, "model catalog entry");
    requireKeys(row, ["provider", "id", "selected", "capabilities"]);
    const provider = identifier(row["provider"], "model provider");
    const id = identifier(row["id"], "model id");
    const key = `${provider}\0${id}`;
    if (seen.has(key)) throw new Error("model catalog contains duplicates");
    seen.add(key);
    const snapshot = decodeSessionProfileSnapshot({
      profile: {
        version: 1,
        revision: 1,
        mode: "act",
        provider,
        model: id,
        approval_posture: "never",
        execution_target: "local",
        max_steps: 1,
        prompt_cache_revision: 1,
      },
      capabilities: {
        provider,
        model: id,
        model_capabilities: row["capabilities"],
        mutable_fields: [],
      },
    });
    return {
      provider,
      id,
      selected: boolean(row["selected"], "model selected"),
      capabilities: snapshot.capabilities.model_capabilities,
    };
  });
  return { version: 1, models };
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
  if (Object.keys(value).some((key) => !allowed.has(key)) ||
    required.some((key) => !Object.hasOwn(value, key))) {
    throw new Error("catalog fields are invalid");
  }
}

function identifier(value: unknown, name: string): string {
  const result = text(value, name);
  if (!/^[A-Za-z0-9][A-Za-z0-9._/:+-]{0,255}$/u.test(result)) {
    throw new Error(`${name} is invalid`);
  }
  return result;
}

function text(value: unknown, name: string): string {
  if (typeof value !== "string" || value.trim().length === 0 ||
    value.length > 256 || /[\0\r\n]/u.test(value)) {
    throw new Error(`${name} is invalid`);
  }
  return value;
}

function finite(
  value: unknown,
  name: string,
  allowed: ReadonlySet<string>,
): string {
  const result = text(value, name);
  if (!allowed.has(result)) throw new Error(`${name} is invalid`);
  return result;
}

function boolean(value: unknown, name: string): boolean {
  if (typeof value !== "boolean") throw new Error(`${name} is invalid`);
  return value;
}
