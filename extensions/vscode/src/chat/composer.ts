import type {
  SessionProfileSnapshot,
  SessionToolCatalog,
} from "../runtime/session.js";
import type { CredentialView } from "../security/credentials.js";

export type ComposerControl =
  | "mode"
  | "provider"
  | "model"
  | "tools"
  | "credential"
  | "approval";

export interface ComposerControlView {
  readonly value: string;
  readonly label: string;
  readonly enabled: boolean;
  readonly title: string;
}

export interface ComposerView {
  readonly revision: number;
  readonly environment: ComposerControlView;
  readonly mode: ComposerControlView;
  readonly provider: ComposerControlView;
  readonly model: ComposerControlView;
  readonly tools: ComposerControlView;
  readonly credential: ComposerControlView;
  readonly approval: ComposerControlView;
  readonly contexts: readonly ComposerContextView[];
}

export interface ComposerContextView {
  readonly id: string;
  readonly kind: string;
  readonly label: string;
}

export function projectComposer(
  snapshot: SessionProfileSnapshot,
  catalog: SessionToolCatalog,
  credential: CredentialView,
  trusted: boolean,
  contexts: readonly ComposerContextView[] = [],
): ComposerView {
  const { profile, capabilities } = snapshot;
  const mutable = new Set(capabilities.mutable_fields);
  return {
    revision: profile.revision,
    environment: control(
      profile.execution_target,
      profile.execution_target === "local" ? "Local" : title(profile.execution_target),
      false,
      "CodeHelper VS Code executes only in the local UI Extension Host",
    ),
    mode: control(
      profile.mode,
      modeLabel(profile.mode),
      mutable.has("mode"),
      "Select Agent mode",
    ),
    provider: control(
      profile.provider,
      `${providerLabel(profile.provider)} · ${profile.model}`,
      trusted,
      "Select Provider and Model",
    ),
    model: control(
      profile.model,
      profile.model,
      trusted,
      mutable.has("model")
        ? "Select Model"
        : "Configure Model and restart the local Runtime",
    ),
    tools: control(
      catalog.digest,
      String(catalog.tools.filter((tool) => tool.enabled).length) +
        `/${String(catalog.tools.length)} Tools`,
      mutable.has("enabled_tool_ids") && catalog.tools.length > 0,
      "Select the tools available to this Session; Guard still applies",
    ),
    credential: control(
      credential.status,
      credential.validation === "valid"
        ? "Key validated"
        : credential.validation === "invalid"
          ? "Key invalid"
          : credential.status === "configured" ? "Key configured" : "Configure key",
      trusted,
      credential.validation === "valid"
        ? `Provider credential validated${
            credential.validatedAt === undefined ? "" : ` at ${credential.validatedAt}`
          }`
        : credential.validation === "invalid"
          ? `Provider credential validation failed${
              credential.validationFailure === undefined
                ? ""
                : ` (${credential.validationFailure})`
            }`
          : credential.status === "configured"
            ? "Credential exists but has not been validated by the Provider"
            : "Configure a credential in VS Code SecretStorage",
    ),
    approval: control(
      profile.approval_posture,
      trusted ? title(profile.approval_posture) : "Read-only",
      trusted && mutable.has("approval_posture"),
      trusted
        ? "Select approval posture"
        : "Approval escalation is unavailable in an untrusted workspace",
    ),
    contexts: contexts.map((context) => ({ ...context })),
  };
}

function modeLabel(value: string): string {
  switch (value) {
    case "plan":
      return "Plan";
    case "act":
      return "Implement";
    case "operate":
      return "Operate";
    default:
      return title(value);
  }
}

function providerLabel(value: string): string {
  if (value === "deepseek" || value.startsWith("deepseek-")) {
    return "DeepSeek";
  }
  return title(value);
}

function control(
  value: string,
  label: string,
  enabled: boolean,
  tooltip: string,
): ComposerControlView {
  return { value, label, enabled, title: tooltip };
}

function title(value: string): string {
  return value.length === 0
    ? "Default"
    : `${value[0]?.toUpperCase() ?? ""}${value.slice(1)}`;
}
