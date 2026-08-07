import type {
  SessionProfileSnapshot,
  SessionToolCatalog,
} from "../runtime/session.js";
import type { CredentialView } from "../security/credentials.js";

export type ComposerControl =
  | "mode"
  | "provider"
  | "model"
  | "thinking"
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
  readonly mode: ComposerControlView;
  readonly provider: ComposerControlView;
  readonly model: ComposerControlView;
  readonly thinking?: ComposerControlView;
  readonly tools: ComposerControlView;
  readonly credential: ComposerControlView;
  readonly approval: ComposerControlView;
}

export function projectComposer(
  snapshot: SessionProfileSnapshot,
  catalog: SessionToolCatalog,
  credential: CredentialView,
  trusted: boolean,
): ComposerView {
  const { profile, capabilities } = snapshot;
  const mutable = new Set(capabilities.mutable_fields);
  const thinking = capabilities.model_capabilities.reasoning
    ? {
        value: profile.reasoning_effort ?? "",
        label: profile.reasoning_effort === ""
          ? "Thinking: Default"
          : `Thinking: ${title(profile.reasoning_effort ?? "")}`,
        enabled: mutable.has("reasoning_effort"),
        title: mutable.has("reasoning_effort")
          ? "Select thinking effort; changing it may reset the prompt cache"
          : "Thinking effort is fixed by this Runtime",
      }
    : undefined;
  return {
    revision: profile.revision,
    mode: control(
      profile.mode,
      title(profile.mode),
      mutable.has("mode"),
      "Select Agent mode",
    ),
    provider: control(
      profile.provider,
      profile.provider,
      trusted,
      "Configure Provider through local Runtime Setup",
    ),
    model: control(
      profile.model,
      profile.model,
      trusted,
      mutable.has("model")
        ? "Select Model"
        : "Configure Model and restart the local Runtime",
    ),
    ...(thinking === undefined ? {} : { thinking }),
    tools: control(
      catalog.digest,
      String(catalog.tools.filter((tool) => tool.enabled).length) +
        `/${String(catalog.tools.length)} Tools`,
      mutable.has("enabled_tool_ids") && catalog.tools.length > 0,
      "Select the tools available to this Session; Guard still applies",
    ),
    credential: control(
      credential.status,
      credential.status === "configured" ? "Key configured" : "Configure key",
      trusted,
      credential.status === "configured"
        ? "Credential is configured; replace it in VS Code SecretStorage"
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
  };
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
