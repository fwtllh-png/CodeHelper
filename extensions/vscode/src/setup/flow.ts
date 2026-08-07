export type ReadinessStatus = "ready" | "degraded" | "blocked";

export interface ReadinessCheck {
  readonly id: string;
  readonly status: ReadinessStatus;
  readonly reason: string;
  readonly impact?: string;
  readonly action?: string;
}

export interface ReadinessReport {
  readonly status: ReadinessStatus;
  readonly checks: readonly ReadinessCheck[];
}

export interface SetupSelection {
  readonly workspace: string;
  readonly configPath: string;
  readonly provider: string;
  readonly model: string;
  readonly credentialKind: "env" | "file" | "keyring";
  readonly credentialName: string;
  readonly force: boolean;
}

export function decodeReadiness(value: unknown): ReadinessReport {
  if (!isObject(value) || !readinessStatus(value["status"]) ||
    !Array.isArray(value["checks"])) {
    throw new TypeError("CodeHelper readiness output is invalid");
  }
  return {
    status: value["status"],
    checks: value["checks"].map((check) => decodeCheck(check)),
  };
}

export function setupArguments(selection: SetupSelection): readonly string[] {
  if (selection.workspace.length === 0 ||
    selection.configPath.length === 0 ||
    selection.provider.length === 0 ||
    selection.model.length === 0 ||
    selection.credentialName.length === 0) {
    throw new TypeError("Setup selection is incomplete");
  }
  return [
    "setup",
    "--workspace", selection.workspace,
    "--config", selection.configPath,
    "--profile", "recommended",
    "--provider", selection.provider,
    "--model", selection.model,
    "--credential-kind", selection.credentialKind,
    "--credential-name", selection.credentialName,
    "--json",
    ...(selection.force ? ["--force"] : []),
  ];
}

export function actionableChecks(
  report: ReadinessReport,
): readonly ReadinessCheck[] {
  return report.checks.filter((check) => check.status !== "ready");
}

export function repairMessage(check: ReadinessCheck): string {
  return [
    check.reason,
    ...(check.impact === undefined ? [] : [`Impact: ${check.impact}`]),
    ...(check.action === undefined ? [] : [`Action: ${check.action}`]),
  ].join("\n");
}

export function runtimeFailureCheck(message: string): ReadinessCheck {
  const normalized = message.toLowerCase();
  let action = "restart the Runtime; if the failure persists, inspect the CodeHelper output";
  if (normalized.includes("binary") || normalized.includes("executable")) {
    action = "configure codehelper.binaryPath or install a managed CodeHelper binary";
  } else if (normalized.includes("config")) {
    action = "run CodeHelper Setup or correct codehelper.runtime.configPath";
  } else if (normalized.includes("compatible") ||
    normalized.includes("protocol")) {
    action = "update the CodeHelper binary and extension to compatible versions";
  } else if (normalized.includes("trust")) {
    action = "review the workspace and grant trust before enabling write operations";
  }
  return {
    id: "runtime.startup",
    status: "blocked",
    reason: message,
    impact: "the VS Code Runtime cannot accept Chat operations",
    action,
  };
}

function decodeCheck(value: unknown): ReadinessCheck {
  if (!isObject(value) ||
    typeof value["id"] !== "string" ||
    !readinessStatus(value["status"]) ||
    typeof value["reason"] !== "string") {
    throw new TypeError("CodeHelper readiness check is invalid");
  }
  const impact = optionalString(value["impact"], "impact");
  const action = optionalString(value["action"], "action");
  return {
    id: value["id"],
    status: value["status"],
    reason: value["reason"],
    ...(impact === undefined ? {} : { impact }),
    ...(action === undefined ? {} : { action }),
  };
}

function optionalString(value: unknown, name: string): string | undefined {
  if (value === undefined) return undefined;
  if (typeof value !== "string") {
    throw new TypeError(`CodeHelper readiness ${name} is invalid`);
  }
  return value;
}

function readinessStatus(value: unknown): value is ReadinessStatus {
  return value === "ready" || value === "degraded" || value === "blocked";
}

function isObject(value: unknown): value is Readonly<Record<string, unknown>> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}
