export interface ExtensionProjection {
  readonly kind: "plugin" | "skill";
  readonly name: string;
  readonly version?: string;
  readonly trust?: string;
  readonly enabled: boolean;
  readonly health: string;
}

export function decodeExtensionResult(value: unknown): ExtensionProjection[] {
  if (!isObject(value) || !Array.isArray(value["extensions"])) {
    throw new Error("extension/list returned an invalid result");
  }
  return value["extensions"].map((item) => {
    if (!isObject(item) ||
      (item["kind"] !== "plugin" && item["kind"] !== "skill") ||
      typeof item["name"] !== "string" ||
      typeof item["enabled"] !== "boolean" ||
      typeof item["health"] !== "string") {
      throw new Error("extension projection is invalid");
    }
    return {
      kind: item["kind"],
      name: item["name"],
      enabled: item["enabled"],
      health: item["health"],
      ...(typeof item["version"] === "string"
        ? { version: item["version"] }
        : {}),
      ...(typeof item["trust"] === "string" ? { trust: item["trust"] } : {}),
    };
  });
}

function isObject(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}
