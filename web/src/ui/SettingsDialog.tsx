import {
  Activity,
  Bell,
  Bot,
  Boxes,
  Check,
  ChevronDown,
  Copy,
  Database,
  KeyRound,
  Monitor,
  Moon,
  Plus,
  RefreshCw,
  Save,
  Search,
  Settings2,
  ShieldCheck,
  Sun,
  Trash2,
  Wrench,
  X
} from "lucide-react";
import {
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
  type ReactNode
} from "react";
import type {
  AgentPreset,
  AgentPresetApplyResult,
  AgentPresetProfile,
  CredentialStatus,
  ModelCatalogEntry,
  ModelTestResult,
  SessionProfile,
  SessionProfileUpdateResult,
  WorkspaceConnection
} from "../protocol";
import type {RuntimeClient, RuntimeSnapshot} from "../runtime/client";
import type {BrowserNotificationSettings} from "./backgroundActivity";
import {
  initialBrowserNotificationSettings,
  setBrowserNotificationsEnabled
} from "./browserNotifications";
import "./SettingsDialog.css";

export type ThemeMode = "light" | "dark" | "system";

export type SettingsSection =
  | "general"
  | "connection"
  | "models"
  | "tools"
  | "extensions"
  | "agent";

interface ProfileDraft {
  mode: SessionProfile["mode"];
  provider: string;
  model: string;
  reasoningEffort: string;
  enabledToolIDs: string[];
  approvalPosture: string;
  executionTarget: string;
  maxSteps: number;
}

interface ApplyNotice {
  tone: "success" | "warning";
  text: string;
}

interface Props {
  snapshot: RuntimeSnapshot;
  client: RuntimeClient;
  newIsolation: "shared" | "worktree";
  theme: ThemeMode;
  initialSection?: SettingsSection;
  onIsolationChange: (value: "shared" | "worktree") => void;
  onThemeChange: (value: ThemeMode) => void;
  onClose: () => void;
  onError: (error: unknown) => void;
}

const sections: readonly {
  id: SettingsSection;
  label: string;
  icon: typeof Settings2;
}[] = [
  {id: "general", label: "General", icon: Settings2},
  {id: "connection", label: "Connection", icon: KeyRound},
  {id: "models", label: "Models", icon: Database},
  {id: "tools", label: "Tools", icon: Wrench},
  {id: "extensions", label: "Extensions", icon: Boxes},
  {id: "agent", label: "Agent preset", icon: Bot}
];

export function SettingsDialog({
  snapshot,
  client,
  newIsolation,
  theme,
  initialSection = "general",
  onIsolationChange,
  onThemeChange,
  onClose,
  onError
}: Props) {
  const [active, setActive] = useState<SettingsSection>(initialSection);
  const [credential, setCredential] = useState<CredentialStatus>();
  const [toolQuery, setToolQuery] = useState("");
  const [error, setError] = useState("");
  const [profileDraft, setProfileDraft] = useState(
    () => settingsProfileDraft(snapshot)
  );
  const [profileBaseline, setProfileBaseline] = useState(
    () => settingsProfileDraft(snapshot)
  );
  const [applying, setApplying] = useState(false);
  const [applyNotice, setApplyNotice] = useState<ApplyNotice>();
  const [confirmClose, setConfirmClose] = useState(false);
  const [notificationSettings, setNotificationSettings] = useState(
    initialBrowserNotificationSettings
  );
  const [notificationPending, setNotificationPending] = useState(false);
  const closeRef = useRef<HTMLButtonElement>(null);
  const profileDraftRef = useRef(profileDraft);
  const profileBaselineRef = useRef(profileBaseline);
  const profileOwnerRef = useRef(snapshot.selectedSessionID);
  profileDraftRef.current = profileDraft;
  profileBaselineRef.current = profileBaseline;
  const dirty = !equalProfileDraft(profileDraft, profileBaseline);
  const profileDraftProblem = modelIDProblem(profileDraft.model);
  const profileApplyBlocked = Boolean(snapshot.conversation.activeTurnID);
  const reportError = useCallback((value: unknown) => {
    setError(value instanceof Error ? value.message : String(value));
    onError(value);
  }, [onError]);
  const changeNotifications = useCallback(async (enabled: boolean) => {
    if (notificationPending) return;
    setNotificationPending(true);
    try {
      setNotificationSettings(await setBrowserNotificationsEnabled(enabled));
    } catch (notificationError) {
      reportError(notificationError);
    } finally {
      setNotificationPending(false);
    }
  }, [notificationPending, reportError]);
  const requestClose = useCallback(() => {
    if (dirty) {
      setConfirmClose(true);
      return;
    }
    onClose();
  }, [dirty, onClose]);
  useEffect(() => {
    closeRef.current?.focus();
  }, []);
  useEffect(() => {
    const close = (event: KeyboardEvent) => {
      if (event.key === "Escape") requestClose();
    };
    window.addEventListener("keydown", close);
    return () => window.removeEventListener("keydown", close);
  }, [requestClose]);

  useEffect(() => {
    const next = settingsProfileDraft(snapshot);
    const sessionChanged = profileOwnerRef.current !== snapshot.selectedSessionID;
    if (
      sessionChanged ||
      equalProfileDraft(profileDraftRef.current, profileBaselineRef.current)
    ) {
      profileOwnerRef.current = snapshot.selectedSessionID;
      setProfileDraft(next);
      setProfileBaseline(next);
      setApplyNotice(undefined);
      setConfirmClose(false);
    }
  }, [
    snapshot.profile?.profile.revision,
    snapshot.selectedSessionID,
    snapshot.tools
  ]);

  useEffect(() => {
    void client.credentialStatus().then(setCredential, reportError);
  }, [client, reportError]);

  const changeProfileDraft = (patch: Partial<ProfileDraft>) => {
    setProfileDraft((current) => current ? {...current, ...patch} : current);
    setApplyNotice(undefined);
    setConfirmClose(false);
  };

  const applyProfile = async () => {
    if (!profileDraft || !profileBaseline || !dirty || applying) return;
    const patch = changedProfileFields(profileBaseline, profileDraft);
    setApplying(true);
    setError("");
    try {
      const result = await client.updateProfile(patch);
      const next = settingsProfileDraftFromProfile(
        result.profile,
        snapshot.tools
      );
      setProfileDraft(next);
      setProfileBaseline(next);
      setApplyNotice(profileApplyNotice(result, profileBaseline, next));
    } catch (applyError) {
      reportError(applyError);
    } finally {
      setApplying(false);
    }
  };

  const acceptAppliedPreset = (result: AgentPresetApplyResult) => {
    const next = settingsProfileDraftFromProfile(
      result.profile_update.profile,
      snapshot.tools
    );
    setProfileDraft(next);
    setProfileBaseline(next);
    setApplyNotice(result.restart_required
      ? {
          tone: "warning",
          text: result.restart_reason || "Applied; Runtime restart required"
        }
      : profileApplyNotice(result.profile_update));
  };

  return (
    <div className="settingsOverlay" role="presentation" onMouseDown={(event) => {
      if (event.target === event.currentTarget) requestClose();
    }}>
      <section
        className="settingsDialog"
        role="dialog"
        aria-modal="true"
        aria-labelledby="settings-title"
      >
        <nav className="settingsNav" aria-label="Settings sections">
          <h2 id="settings-title">Settings</h2>
          <div>
            {sections.map((section) => {
              const Icon = section.icon;
              return (
                <button
                  type="button"
                  key={section.id}
                  aria-label={section.label}
                  aria-current={active === section.id ? "page" : undefined}
                  title={section.label}
                  onClick={() => setActive(section.id)}
                >
                  <Icon size={16} />
                  <span>{section.label}</span>
                </button>
              );
            })}
          </div>
        </nav>
        <div className="settingsMain">
          <header className="settingsHeader">
            <button
              ref={closeRef}
              type="button"
              className="settingsClose"
              aria-label="Close settings"
              onClick={requestClose}
            >
              <X size={18} />
            </button>
          </header>
          <div className="settingsContent">
            {error && <div className="settingsError" role="alert">{error}</div>}
            {active === "general" && (
              <GeneralSettings
                snapshot={snapshot}
                isolation={newIsolation}
                theme={theme}
                notificationSettings={notificationSettings}
                notificationPending={notificationPending}
                onIsolationChange={onIsolationChange}
                onThemeChange={onThemeChange}
                onNotificationsChange={(enabled) => void changeNotifications(enabled)}
              />
            )}
            {active === "models" && (
              <ModelSettings
                snapshot={snapshot}
                draft={profileDraft}
                onDraftChange={changeProfileDraft}
                client={client}
                onError={reportError}
              />
            )}
            {active === "connection" && (
              <ConnectionSettings
                snapshot={snapshot}
                credential={credential}
                onCredential={setCredential}
                client={client}
                onError={reportError}
              />
            )}
            {active === "tools" && (
              <ToolSettings
                snapshot={snapshot}
                draft={profileDraft}
                onDraftChange={changeProfileDraft}
                query={toolQuery}
                onQueryChange={setToolQuery}
              />
            )}
            {active === "extensions" && (
              <ExtensionSettings
                snapshot={snapshot}
                client={client}
                onError={reportError}
              />
            )}
            {active === "agent" && (
              <AgentSettings
                snapshot={snapshot}
                client={client}
                draft={profileDraft}
                onDraftChange={changeProfileDraft}
                onAppliedPreset={acceptAppliedPreset}
                applyBlocked={profileApplyBlocked}
                onError={reportError}
              />
            )}
          </div>
          <footer className="settingsApplyBar" data-dirty={dirty || undefined}>
            <span aria-live="polite">
              {confirmClose
                ? "Discard unsaved changes?"
                : dirty
                  ? profileDraftProblem
                    ? profileDraftProblem
                    : profileApplyBlocked
                    ? "Unsaved changes · finish the active Turn to apply"
                    : "Unsaved changes"
                  : applyNotice?.text || "Settings are up to date"}
            </span>
            {applyNotice && !dirty && (
              <small data-tone={applyNotice.tone}>
                {applyNotice.tone === "success"
                  ? <Check size={13} />
                  : <RefreshCw size={13} />}
                {applyNotice.tone === "success" ? "Applied" : "Restart required"}
              </small>
            )}
            <div>
              {confirmClose ? (
                <>
                  <button type="button" onClick={() => setConfirmClose(false)}>
                    Keep editing
                  </button>
                  <button type="button" onClick={onClose}>
                    Discard and close
                  </button>
                </>
              ) : (
                <>
                  <button
                    type="button"
                    disabled={!dirty || applying}
                    onClick={() => {
                      setProfileDraft(profileBaseline);
                      setApplyNotice(undefined);
                    }}
                  >
                    Discard
                  </button>
                  <button
                    type="button"
                    className="settingsApply"
                    disabled={
                      !dirty || applying || profileApplyBlocked ||
                      Boolean(profileDraftProblem)
                    }
                    title={profileApplyBlocked
                      ? "Finish the active Turn before applying settings"
                      : "Apply changes to this Session"}
                    onClick={() => void applyProfile()}
                  >
                    <Save size={14} />
                    {applying ? "Applying..." : "Apply changes"}
                  </button>
                </>
              )}
            </div>
          </footer>
        </div>
      </section>
    </div>
  );
}

function GeneralSettings({
  snapshot,
  isolation,
  theme,
  notificationSettings,
  notificationPending,
  onIsolationChange,
  onThemeChange,
  onNotificationsChange
}: {
  snapshot: RuntimeSnapshot;
  isolation: "shared" | "worktree";
  theme: ThemeMode;
  notificationSettings: BrowserNotificationSettings;
  notificationPending: boolean;
  onIsolationChange: (value: "shared" | "worktree") => void;
  onThemeChange: (value: ThemeMode) => void;
  onNotificationsChange: (enabled: boolean) => void;
}) {
  return (
    <SettingsSectionView
      title="General"
      description="Browser preferences and defaults for newly created sessions."
    >
      <SettingRow
        title="New session isolation"
        description="Choose whether new sessions share the workspace or use an isolated worktree."
      >
        <SelectControl
          label="New session isolation"
          value={isolation}
          values={["shared", "worktree"]}
          onChange={(value) => onIsolationChange(value as "shared" | "worktree")}
        />
      </SettingRow>
      <div className="settingsBlock">
        <div className="settingsBlockTitle">Appearance</div>
        <div className="themeChoices" role="radiogroup" aria-label="Appearance">
          <ThemeChoice
            label="Light"
            icon={<Sun size={20} />}
            selected={theme === "light"}
            onClick={() => onThemeChange("light")}
          />
          <ThemeChoice
            label="Dark"
            icon={<Moon size={20} />}
            selected={theme === "dark"}
            onClick={() => onThemeChange("dark")}
          />
          <ThemeChoice
            label="System"
            icon={<Monitor size={20} />}
            selected={theme === "system"}
            onClick={() => onThemeChange("system")}
          />
        </div>
      </div>
      <SettingRow
        title="Desktop notifications"
        description="Only background Session status is shown. Prompts and tool output are excluded."
      >
        <label className="settingsPreferenceControl">
          <Bell size={15} aria-hidden="true" />
          <input
            type="checkbox"
            role="switch"
            aria-label="Desktop notifications"
            checked={notificationSettings.enabled}
            disabled={
              notificationPending ||
              notificationSettings.permission === "unsupported" ||
              notificationSettings.permission === "denied"
            }
            onChange={(event) => onNotificationsChange(event.target.checked)}
          />
          <span>
            {notificationPending
              ? "Requesting"
              : notificationSettings.enabled
                ? "On"
                : notificationSettings.permission === "denied"
                  ? "Blocked"
                  : notificationSettings.permission === "unsupported"
                    ? "Unavailable"
                    : "Off"}
          </span>
        </label>
      </SettingRow>
      <SettingRow
        title="Runtime connection"
        description={snapshot.workspaceRoot || "Workspace unavailable"}
      >
        <span className="settingsStatus" data-ready={snapshot.socketConnected || undefined}>
          <span />
          {snapshot.socketConnected ? "Connected" : snapshot.phase}
        </span>
      </SettingRow>
    </SettingsSectionView>
  );
}

function ModelSettings({
  snapshot,
  draft,
  onDraftChange,
  client,
  onError
}: {
  snapshot: RuntimeSnapshot;
  draft: ProfileDraft;
  onDraftChange: (patch: Partial<ProfileDraft>) => void;
  client: RuntimeClient;
  onError: (error: unknown) => void;
}) {
  const catalogModel = snapshot.models.find(
    (model) => model.provider === draft.provider && model.id === draft.model
  );
  const availableModels = snapshot.models.filter(
    (model) =>
      model.provider === draft.provider &&
      model.capabilities.availability === "available"
  );
  const profileCapabilities = snapshot.profile?.capabilities.model_capabilities;
  const selectedModel = catalogModel ?? (
    profileCapabilities && draft.model.trim()
      ? {
          provider: draft.provider,
          id: draft.model,
          source: "connection_baseline" as const,
          selected: true,
          capabilities: {
            ...profileCapabilities,
            display_name: draft.model,
            selection_mode: "hot" as const
          }
        }
      : undefined
  );
  const [configuringModel, setConfiguringModel] = useState(!catalogModel);
  const [testing, setTesting] = useState(false);
  const [testResult, setTestResult] = useState<ModelTestResult>();
  const modelInputRef = useRef<HTMLInputElement>(null);
  useEffect(() => {
    setConfiguringModel(!catalogModel);
  }, [snapshot.selectedSessionID]);
  useEffect(() => {
    if (configuringModel) modelInputRef.current?.focus();
  }, [configuringModel]);
  const advertisedReasoningValues =
    selectedModel?.capabilities.reasoning_efforts ?? [];
  const reasoningValues = selectedModel?.capabilities.default_reasoning_effort
    ? advertisedReasoningValues
    : ["", ...advertisedReasoningValues];
  const changeModel = (modelID: string) => {
    setTestResult(undefined);
    const model = availableModels.find(
      (entry) => entry.id === modelID
    );
    onDraftChange({
      model: modelID,
      reasoningEffort: model
        ? model.capabilities.default_reasoning_effort ?? ""
        : draft.reasoningEffort
    });
  };
  const showExistingModels = () => {
    if (!catalogModel && availableModels[0]) {
      changeModel(availableModels[0].id);
    }
    setConfiguringModel(false);
  };
  const showNewModel = () => {
    changeModel("");
    setConfiguringModel(true);
  };
  const testModel = async () => {
    if (testing || modelIDProblem(draft.model)) return;
    setTesting(true);
    setTestResult(undefined);
    try {
      setTestResult(await client.testModel(draft.model.trim()));
    } catch (error) {
      onError(error);
    } finally {
      setTesting(false);
    }
  };
  return (
    <SettingsSectionView
      title="Models"
      description="Choose the model CodeHelper uses for this session."
    >
      <div className="settingsBlock">
        <div className="settingsBlockTitle">Session model</div>
        <p>Select a configured model, or add another model.</p>
          <div className="settingsButtonRow modelSelectionPrimary">
            {configuringModel ? (
              <input
                ref={modelInputRef}
                className="settingsSelect"
                aria-label="Settings model"
                value={draft.model}
                maxLength={256}
                autoComplete="off"
                spellCheck={false}
                disabled={!mutable(snapshot, "model")}
                aria-invalid={Boolean(modelIDProblem(draft.model)) || undefined}
                onChange={(event) => changeModel(event.target.value)}
              />
            ) : (
              <SelectControl
                label="Settings model"
                value={draft.model}
                values={availableModels.map((model) => model.id)}
                disabled={!mutable(snapshot, "model")}
                format={(value) => value}
                onChange={changeModel}
              />
            )}
            <button
              type="button"
              disabled={
                !mutable(snapshot, "model") ||
                (configuringModel && availableModels.length === 0)
              }
              onClick={() => configuringModel
                ? showExistingModels()
                : showNewModel()}
            >
              {configuringModel
                ? <><Database size={13} /> Existing models</>
                : <><Plus size={13} /> New model</>}
            </button>
          </div>
          {configuringModel && modelIDProblem(draft.model) && (
            <small role="alert">{modelIDProblem(draft.model)}</small>
          )}
          <div className="settingsButtonRow">
            <button
              type="button"
              disabled={testing || Boolean(modelIDProblem(draft.model))}
              onClick={() => void testModel()}
            >
              {testing
                ? <><RefreshCw className="spin" size={13} /> Testing...</>
                : <><Activity size={13} /> Test model</>}
            </button>
            {testResult && (
              <span
                className="settingsInlineResult"
                data-tone={
                  testResult.status === "available" ? undefined : "warning"
                }
                role="status"
              >
                {testResult.status === "available" && <Check size={13} />}
                {testResult.detail}
              </span>
            )}
          </div>
      </div>
      {selectedModel?.capabilities.reasoning && (
        <SettingRow
          title="Reasoning effort"
          description="Choose how much time the model spends thinking before it answers."
        >
          <SelectControl
            label="Settings reasoning effort"
            value={
              reasoningValues.includes(draft.reasoningEffort)
                ? draft.reasoningEffort
                : selectedModel.capabilities.default_reasoning_effort ??
                  reasoningValues[0] ??
                  ""
            }
            values={reasoningValues}
            disabled={!mutable(snapshot, "reasoning_effort")}
            onChange={(reasoningEffort) => onDraftChange({reasoningEffort})}
          />
        </SettingRow>
      )}
      {selectedModel && (
        <ModelCapabilityPanel model={selectedModel} />
      )}
    </SettingsSectionView>
  );
}

function ConnectionSettings({
  snapshot,
  credential,
  onCredential,
  client,
  onError
}: {
  snapshot: RuntimeSnapshot;
  credential?: CredentialStatus;
  onCredential: (status: CredentialStatus) => void;
  client: RuntimeClient;
  onError: (error: unknown) => void;
}) {
  const provider = snapshot.providers.find((entry) => entry.selected);
  const [connection, setConnection] = useState<WorkspaceConnection>();
  useEffect(() => {
    void client.connectionStatus().then(setConnection, onError);
  }, [client, onError]);
  return (
    <SettingsSectionView
      title="Connection"
      description="Workspace Runtime provider and credential boundary."
    >
      <SettingRow
        title="Provider"
        description="Shared by every Session in this Workspace."
      >
        <span className="settingsStatus" data-ready>
          <span />
          {provider?.display_name || snapshot.profile?.profile.provider}
        </span>
      </SettingRow>
      <SettingRow title="Endpoint" description="Fixed for this Workspace Runtime.">
        <code className="settingsReference">
          {connection ? connection.endpoint || "Runtime-managed" : "Loading..."}
        </code>
      </SettingRow>
      <SettingRow title="Protocol" description="Provider wire protocol.">
        <code className="settingsReference">
          {connection?.protocol || "Loading..."}
        </code>
      </SettingRow>
      <CredentialPanel
        status={credential}
        onSet={(secret) => client.setKeyringCredential(secret).then(onCredential)}
        onValidate={() => client.validateCredential().then(onCredential)}
        onClear={() => client.clearKeyringCredential().then(onCredential)}
        onError={onError}
      />
    </SettingsSectionView>
  );
}

function ModelCapabilityPanel({model}: {model: ModelCatalogEntry}) {
  const capabilities = model.capabilities;
  const labels = [
    capabilities.streaming && "Streaming",
    capabilities.reasoning && "Reasoning",
    capabilities.tool_calls && "Tool calls",
    capabilities.parallel_tool_calls === "supported" && "Parallel tools",
    capabilities.vision && "Vision",
    capabilities.image_input && "Image input",
    capabilities.native_search && "Native search",
    capabilities.prompt_cache && "Prompt cache"
  ].filter((value): value is string => Boolean(value));
  return (
    <div className="settingsBlock modelCapabilityPanel">
      <div className="settingsBlockTitle">
        <span>{capabilities.display_name || model.id}</span>
        <small data-restart={
          capabilities.selection_mode === "restart_required" || undefined
        }>
          {capabilities.selection_mode.replaceAll("_", " ")}
        </small>
      </div>
      {capabilities.unavailable_reason && (
        <p>{capabilities.unavailable_reason}</p>
      )}
      {model.source === "connection_baseline" && (
        <p role="status">
          Unverified metadata inherited from the Workspace connection.
        </p>
      )}
      <dl className="settingsFacts">
        <div><dt>Context window</dt><dd>{capabilities.context_window.toLocaleString()}</dd></div>
        <div><dt>Max output</dt><dd>{capabilities.max_output_tokens.toLocaleString()}</dd></div>
      </dl>
      <div className="capabilityTags">
        {labels.map((label) => <span key={label}>{label}</span>)}
      </div>
    </div>
  );
}

function ToolSettings({
  snapshot,
  draft,
  onDraftChange,
  query,
  onQueryChange
}: {
  snapshot: RuntimeSnapshot;
  draft: ProfileDraft;
  onDraftChange: (patch: Partial<ProfileDraft>) => void;
  query: string;
  onQueryChange: (value: string) => void;
}) {
  const normalized = query.trim().toLocaleLowerCase();
  const tools = useMemo(() => snapshot.tools.filter((tool) =>
    !normalized || [tool.name, tool.description, tool.source_label]
      .some((value) => value.toLocaleLowerCase().includes(normalized))
  ), [normalized, snapshot.tools]);
  return (
    <SettingsSectionView
      title="Tools"
      description="Tools remain governed by Runtime policy, approval, and sandbox."
    >
      <label className="settingsSearch">
        <Search size={15} />
        <input
          type="search"
          aria-label="Search tools"
          placeholder="Search tools"
          value={query}
          onChange={(event) => onQueryChange(event.target.value)}
        />
      </label>
      <div className="settingsCatalogHeading">
        <strong>Catalog</strong><span>{tools.length}</span>
      </div>
      <div className="settingsCatalog">
        {tools.map((tool) => {
          const enabled = draft.enabledToolIDs.includes(tool.id);
          return (
            <div className="settingsCatalogRow settingsToolRow" key={tool.id}>
              <Wrench size={15} />
              <span>
                <strong>{tool.name}</strong>
                <small>{tool.description || tool.source_label}</small>
              </span>
              <span className="settingsTags">
                <small>{tool.source_label}</small>
                <small>{tool.risk_level}</small>
                {tool.guarded && <small>guarded</small>}
              </span>
              <label className="settingsToggle">
                <span className="srOnly">{`${enabled ? "Disable" : "Enable"} ${tool.name}`}</span>
                <input
                  type="checkbox"
                  checked={enabled}
                  disabled={
                    tool.availability !== "available" ||
                    !mutable(snapshot, "enabled_tool_ids") ||
                    (enabled && draft.enabledToolIDs.length === 1)
                  }
                  title={enabled && draft.enabledToolIDs.length === 1
                    ? "At least one tool must remain enabled"
                    : undefined}
                  onChange={(event) => {
                    const next = event.target.checked
                      ? [...new Set([...draft.enabledToolIDs, tool.id])]
                      : draft.enabledToolIDs.filter((id) => id !== tool.id);
                    onDraftChange({enabledToolIDs: next.sort()});
                  }}
                />
              </label>
              <details className="settingsCatalogDetails">
                <summary>Details</summary>
                <dl>
                  <div><dt>Source</dt><dd>{tool.source_kind} · {tool.source_label}</dd></div>
                  <div><dt>Capability</dt><dd>{tool.capability || "unknown"}</dd></div>
                  <div><dt>Access</dt><dd>{tool.access_mode || "unknown"}</dd></div>
                  <div><dt>Sandbox</dt><dd>{tool.sandbox_requirement || "unknown"}</dd></div>
                  <div>
                    <dt>Policy</dt>
                    <dd>{tool.policy_state || "unknown"} · {tool.policy_reason}</dd>
                  </div>
                  <div>
                    <dt>Constitution</dt>
                    <dd>
                      {tool.constitution_state || "unknown"} · {tool.constitution_reason}
                    </dd>
                  </div>
                  {tool.unavailable_reason && (
                    <div><dt>Error</dt><dd>{tool.unavailable_reason}</dd></div>
                  )}
                </dl>
              </details>
            </div>
          );
        })}
      </div>
    </SettingsSectionView>
  );
}

function ExtensionSettings({
  snapshot,
  client,
  onError
}: {
  snapshot: RuntimeSnapshot;
  client: RuntimeClient;
  onError: (error: unknown) => void;
}) {
  const [pending, setPending] = useState("");
  const [details, setDetails] = useState<Record<string, string>>({});
  const [result, setResult] = useState("");
  const run = async (
    extension: RuntimeSnapshot["extensions"][number],
    action: "detail" | "verify" | "enable" | "disable"
  ) => {
    const key = `${extension.kind}:${extension.name}`;
    if (pending) return;
    setPending(`${key}:${action}`);
    setResult("");
    try {
      const value = action === "enable" || action === "disable"
        ? await client.setExtensionEnabled(
            extension.kind,
            extension.name,
            action === "enable"
          )
        : await client.controlExtension(
            extension.kind,
            extension.name,
            action
          );
      if (action === "detail" || value.detail) {
        setDetails((current) => ({
          ...current,
          [key]: JSON.stringify(
            value.detail ?? value.receipt ?? value.extensions ?? {},
            null,
            2
          )
        }));
      }
      setResult(
        `${titleCase(action)} ${value.receipt?.status || "completed"}`
      );
    } catch (operationError) {
      onError(operationError);
    } finally {
      setPending("");
    }
  };
  return (
    <SettingsSectionView
      title="Extensions"
      description="Installed skills visible to the Runtime."
    >
      {result && (
        <div className="settingsInlineResult" role="status">
          <Check size={13} /> {result}
        </div>
      )}
      {snapshot.extensions.length === 0 ? (
        <p className="settingsEmpty">No extensions are registered.</p>
      ) : (
        <div className="settingsCatalog settingsExtensionGrid">
          {snapshot.extensions.map((extension) => {
            const key = `${extension.kind}:${extension.name}`;
            return (
              <article className="settingsCatalogRow settingsExtension" key={key}>
                <Boxes size={15} />
                <span>
                  <strong>{extension.name}</strong>
                  <small>
                    {extension.kind}
                    {extension.version ? ` · ${extension.version}` : ""}
                    {extension.source ? ` · ${extension.source}` : ""}
                  </small>
                </span>
                <span className="settingsHealth" data-health={extension.health}>
                  <span />{extension.health}
                </span>
                <label className="settingsToggle">
                  <span className="srOnly">
                    {`${extension.enabled ? "Disable" : "Enable"} ${extension.name}`}
                  </span>
                  <input
                    type="checkbox"
                    checked={extension.enabled}
                    disabled={Boolean(pending)}
                    onChange={(event) => void run(
                      extension,
                      event.target.checked ? "enable" : "disable"
                    )}
                  />
                </label>
                <div className="settingsExtensionFacts">
                  <span>Trust: {extension.trust || "not declared"}</span>
                  {extension.publisher && (
                    <span>Publisher: {extension.publisher}</span>
                  )}
                  <span>
                    Permissions: {extension.permissions?.join(", ") || "none declared"}
                  </span>
                  <span>
                    Capabilities: {
                      extension.capabilities?.map((capability) =>
                        `${capability.id}${capability.enabled ? "" : " (off)"}`
                      ).join(", ") || "none declared"
                    }
                  </span>
                  {extension.digest && <span>Digest: {extension.digest.slice(0, 12)}</span>}
                </div>
                <div className="settingsButtonRow settingsExtensionActions">
                  <button
                    type="button"
                    disabled={Boolean(pending)}
                    onClick={() => void run(extension, "detail")}
                  >
                    Details
                  </button>
                  <button
                    type="button"
                    disabled={Boolean(pending)}
                    onClick={() => void run(extension, "verify")}
                  >
                    <ShieldCheck size={13} />
                    Verify
                  </button>
                </div>
                {details[key] && (
                  <pre className="settingsCode settingsExtensionDetail">
                    {details[key]}
                  </pre>
                )}
              </article>
            );
          })}
        </div>
      )}
    </SettingsSectionView>
  );
}

function AgentSettings({
  snapshot,
  client,
  draft,
  onDraftChange,
  onAppliedPreset,
  applyBlocked,
  onError
}: {
  snapshot: RuntimeSnapshot;
  client: RuntimeClient;
  draft: ProfileDraft;
  onDraftChange: (patch: Partial<ProfileDraft>) => void;
  onAppliedPreset: (result: AgentPresetApplyResult) => void;
  applyBlocked: boolean;
  onError: (error: unknown) => void;
}) {
  const [presets, setPresets] = useState<AgentPreset[]>([]);
  const [selectedPresetID, setSelectedPresetID] = useState("");
  const [presetName, setPresetName] = useState("");
  const [presetDescription, setPresetDescription] = useState("");
  const [presetPending, setPresetPending] = useState("");
  const [presetResult, setPresetResult] = useState("");
  const [confirmDelete, setConfirmDelete] = useState(false);
  const selectedPreset = presets.find(
    (preset) => preset.id === selectedPresetID
  );

  useEffect(() => {
    let current = true;
    void client.listAgentPresets().then(
      (result) => {
        if (!current) return;
        setPresets(result.presets);
        setSelectedPresetID((selected) =>
          result.presets.some((preset) => preset.id === selected)
            ? selected
            : result.presets[0]?.id ?? ""
        );
      },
      onError
    );
    return () => {
      current = false;
    };
  }, [client, onError]);

  useEffect(() => {
    if (!selectedPreset) return;
    setPresetName(selectedPreset.name);
    setPresetDescription(selectedPreset.description ?? "");
    setConfirmDelete(false);
  }, [selectedPreset?.id, selectedPreset?.revision]);

  const runPreset = async (action: string, operation: () => Promise<void>) => {
    if (presetPending) return;
    setPresetPending(action);
    setPresetResult("");
    try {
      await operation();
      setPresetResult(action);
    } catch (operationError) {
      onError(operationError);
    } finally {
      setPresetPending("");
    }
  };
  const savePreset = async (
    id?: string,
    expectedRevision?: number,
    name = presetName
  ) => {
    const result = await client.saveAgentPreset({
      id,
      expectedRevision,
      name: name.trim(),
      description: presetDescription.trim(),
      profile: agentPresetProfile(draft)
    });
    if (!result.preset) return;
    setPresets((current) => [
      ...current.filter((preset) => preset.id !== result.preset?.id),
      result.preset!
    ].sort((left, right) => left.name.localeCompare(right.name)));
    setSelectedPresetID(result.preset.id);
  };
  return (
    <SettingsSectionView
      title="Agent preset"
      description="Compose reusable workspace presets and apply them to the active session."
    >
      <div className="settingsBlock presetWorkbench">
        <div className="settingsBlockTitle">
          <span>Workspace presets</span>
          <small>{presets.length}</small>
        </div>
        <div className="presetFields">
          <label>
            <span>Saved preset</span>
            <select
              className="settingsSelect"
              aria-label="Saved agent preset"
              value={selectedPresetID}
              onChange={(event) => {
                const id = event.target.value;
                setSelectedPresetID(id);
                if (!id) {
                  setPresetName("");
                  setPresetDescription("");
                }
              }}
            >
              <option value="">New preset</option>
              {presets.map((preset) => (
                <option value={preset.id} key={preset.id}>
                  {preset.name}
                </option>
              ))}
            </select>
          </label>
          <label>
            <span>Name</span>
            <input
              aria-label="Agent preset name"
              value={presetName}
              maxLength={80}
              placeholder="Focused review"
              onChange={(event) => setPresetName(event.target.value)}
            />
          </label>
          <label>
            <span>Description</span>
            <input
              aria-label="Agent preset description"
              value={presetDescription}
              maxLength={512}
              placeholder="Optional"
              onChange={(event) => setPresetDescription(event.target.value)}
            />
          </label>
        </div>
        <div className="presetScope">
          <span>Scope</span>
          <strong>Workspace</strong>
          <small>Apply target: current Session</small>
        </div>
        <div className="settingsButtonRow">
          <button
            type="button"
            disabled={!presetName.trim() || Boolean(presetPending)}
            onClick={() => void runPreset(
              "Preset created",
              () => savePreset()
            )}
          >
            <Save size={13} /> Save new
          </button>
          <button
            type="button"
            disabled={!selectedPreset || !presetName.trim() || Boolean(presetPending)}
            onClick={() => selectedPreset && void runPreset(
              "Preset updated",
              () => savePreset(selectedPreset.id, selectedPreset.revision)
            )}
          >
            <RefreshCw size={13} /> Update
          </button>
          <button
            type="button"
            disabled={!selectedPreset || Boolean(presetPending)}
            onClick={() => selectedPreset && void runPreset(
              "Preset copied",
              () => savePreset(
                undefined,
                undefined,
                uniquePresetName(`${selectedPreset.name} copy`, presets)
              )
            )}
          >
            <Copy size={13} /> Duplicate
          </button>
          <button
            type="button"
            disabled={!selectedPreset || Boolean(presetPending)}
            onClick={() => selectedPreset && void runPreset(
              "Preset loaded",
              async () => {
                onDraftChange(profileDraftFromPreset(
                  selectedPreset.profile,
                  snapshot.tools
                ));
              }
            )}
          >
            Load into draft
          </button>
          <button
            type="button"
            disabled={!selectedPreset || Boolean(presetPending) || applyBlocked}
            title={applyBlocked
              ? "Finish the active Turn before applying a preset"
              : "Apply preset to current Session"}
            onClick={() => selectedPreset && void runPreset(
              "Preset applied",
              async () => {
                const result = await client.applyAgentPreset(selectedPreset.id);
                onAppliedPreset(result);
              }
            )}
          >
            <Check size={13} /> Apply to session
          </button>
          {confirmDelete ? (
            <>
              <button type="button" onClick={() => setConfirmDelete(false)}>
                Keep preset
              </button>
              <button
                type="button"
                className="settingsDanger"
                disabled={!selectedPreset || Boolean(presetPending)}
                onClick={() => selectedPreset && void runPreset(
                  "Preset deleted",
                  async () => {
                    await client.deleteAgentPreset(selectedPreset);
                    setPresets((current) => current.filter(
                      (preset) => preset.id !== selectedPreset.id
                    ));
                    setSelectedPresetID("");
                    setPresetName("");
                    setPresetDescription("");
                  }
                )}
              >
                <Trash2 size={13} /> Confirm delete
              </button>
            </>
          ) : (
            <button
              type="button"
              className="settingsDanger"
              disabled={!selectedPreset || Boolean(presetPending)}
              onClick={() => setConfirmDelete(true)}
            >
              <Trash2 size={13} /> Delete
            </button>
          )}
        </div>
        {presetResult && (
          <div className="settingsInlineResult" role="status">
            <Check size={13} /> {presetResult}
          </div>
        )}
      </div>
      <SettingRow title="Mode" description="Plan before acting or execute the requested task.">
        <SelectControl
          label="Agent mode"
          value={draft.mode}
          values={["plan", "act", "operate"]}
          disabled={!mutable(snapshot, "mode")}
          onChange={(mode) => onDraftChange({
            mode: mode as ProfileDraft["mode"]
          })}
        />
      </SettingRow>
      <SettingRow title="Approval" description="Control when consequential actions ask first.">
        <SelectControl
          label="Approval posture"
          value={draft.approvalPosture}
          values={["suggest", "auto", "never"]}
          disabled={!mutable(snapshot, "approval_posture")}
          onChange={(approvalPosture) => onDraftChange({approvalPosture})}
        />
      </SettingRow>
      <SettingRow title="Execution" description="Choose the execution isolation target.">
        <SelectControl
          label="Execution target"
          value={draft.executionTarget}
          values={["local"]}
          disabled={!mutable(snapshot, "execution_target")}
          onChange={(executionTarget) => onDraftChange({executionTarget})}
        />
      </SettingRow>
      <NumberSetting
        value={draft.maxSteps}
        disabled={!mutable(snapshot, "max_steps")}
        onChange={(maxSteps) => onDraftChange({maxSteps})}
      />
      {(snapshot.tasks.length > 0 || snapshot.agents.length > 0 || snapshot.usage) && (
        <div className="settingsBlock">
          <div className="settingsBlockTitle">Current activity</div>
          {snapshot.usage && (
            <dl className="settingsFacts" aria-label="Usage">
              <div><dt>Turns</dt><dd>{snapshot.usage.turns}</dd></div>
              <div><dt>Calls</dt><dd>{snapshot.usage.calls}</dd></div>
              <div><dt>Tokens</dt><dd>{snapshot.usage.total_tokens.toLocaleString()}</dd></div>
            </dl>
          )}
          <div className="settingsActivity">
            {snapshot.tasks.map((task) => (
              <div key={task.id}>
                <span>
                  <strong>{task.kind}</strong>
                  <small>{task.id}</small>
                </span>
                <b>{task.state}</b>
                {(task.failure_reason || task.reason) && (
                  <small>{task.failure_reason || task.reason}</small>
                )}
              </div>
            ))}
            {snapshot.agents.map((agent) => (
              <div key={agent.id}>
                <span>
                  <strong>{agent.role}</strong>
                  <small>{agent.id}</small>
                </span>
                <b>{agent.status}</b>
                {agent.last_message && <small>{agent.last_message}</small>}
              </div>
            ))}
          </div>
        </div>
      )}
      {snapshot.checkpoints.length > 0 && (
        <div className="settingsBlock">
          <div className="settingsBlockTitle">Recovery checkpoints</div>
          <div className="checkpointList">
            {snapshot.checkpoints.map((checkpoint) => (
              <div key={checkpoint.id}>
                <span title={checkpoint.summary}>{checkpoint.summary}</span>
                <button
                  type="button"
                  disabled={!checkpoint.can_restore}
                  onClick={() => void client.restoreCheckpoint(checkpoint.id).catch(onError)}
                >
                  Restore
                </button>
                <button
                  type="button"
                  disabled={!checkpoint.can_fork}
                  onClick={() => void client.forkCheckpoint(checkpoint.id).catch(onError)}
                >
                  Fork
                </button>
              </div>
            ))}
          </div>
        </div>
      )}
    </SettingsSectionView>
  );
}

function NumberSetting({
  value,
  disabled,
  onChange
}: {
  value: number;
  disabled: boolean;
  onChange: (value: number) => void;
}) {
  return (
    <SettingRow
      title="Maximum steps"
      description="Consecutive no-progress steps allowed before finalization."
    >
      <input
        className="settingsNumber"
        aria-label="Maximum steps"
        type="number"
        min={0}
        value={value}
        disabled={disabled}
        onChange={(event) => {
          const next = Number(event.target.value);
          if (Number.isInteger(next) && next >= 0) onChange(next);
        }}
      />
    </SettingRow>
  );
}

function CredentialPanel({
  status,
  onSet,
  onValidate,
  onClear,
  onError
}: {
  status?: CredentialStatus;
  onSet: (secret: string) => Promise<unknown>;
  onValidate: () => Promise<unknown>;
  onClear: () => Promise<unknown>;
  onError: (error: unknown) => void;
}) {
  const [secret, setSecret] = useState("");
  const [pending, setPending] = useState(false);
  const [confirmClear, setConfirmClear] = useState(false);
  const [result, setResult] = useState("");
  const run = async (
    operation: () => Promise<unknown>,
    success: string,
    clear = false
  ) => {
    if (pending) return;
    setPending(true);
    setResult("");
    try {
      await operation();
      if (clear) setSecret("");
      setConfirmClear(false);
      setResult(success);
    } catch (error) {
      onError(error);
    } finally {
      setPending(false);
    }
  };
  return (
    <div className="settingsBlock credentialPanel">
      <div className="settingsBlockTitle">
        <span><KeyRound size={15} /> Credential</span>
        <span className="settingsHealth" data-health={
          status?.configured ? status.validation : "missing"
        }>
          <span />
          {status?.configured ? status.validation : "missing"}
        </span>
      </div>
      <p>
        Stored in {status?.reference.kind || "secure external storage"}.
        Secret values are never returned to the browser.
      </p>
      {status?.reference.name && (
        <p className="settingsReference">Reference: {status.reference.name}</p>
      )}
      {status?.validation_detail && <p>{status.validation_detail}</p>}
      {status?.validated_at && (
        <p>Validated {new Date(status.validated_at).toLocaleString()}</p>
      )}
      {status?.restart_required && (
        <div className="settingsInlineResult" data-tone="warning">
          <RefreshCw size={13} /> Runtime restart required
        </div>
      )}
      <div className="credentialInput">
        <input
          type="password"
          autoComplete="off"
          aria-label="Provider credential"
          placeholder="API key"
          value={secret}
          onChange={(event) => setSecret(event.target.value)}
        />
        <button
          type="button"
          disabled={!secret.trim() || pending}
          onClick={() => void run(
            () => onSet(secret),
            status?.configured ? "Credential rotated" : "Credential created",
            true
          )}
        >
          {status?.configured ? "Rotate key" : "Set key"}
        </button>
      </div>
      <div className="settingsButtonRow">
        <button
          type="button"
          disabled={!status?.configured || pending}
          onClick={() => void run(onValidate, "Connection verified")}
        >
          <RefreshCw className={pending ? "spin" : undefined} size={13} />
          {pending ? "Testing..." : "Test connection"}
        </button>
        {status?.reference.kind === "keyring" && (
          confirmClear ? (
            <>
              <button type="button" onClick={() => setConfirmClear(false)}>
                Keep key
              </button>
              <button
                type="button"
                className="settingsDanger"
                disabled={!status.configured || pending}
                onClick={() => void run(
                  onClear,
                  "Credential removed from Keyring"
                )}
              >
                Confirm clear
              </button>
            </>
          ) : (
            <button
              type="button"
              disabled={!status.configured || pending}
              onClick={() => setConfirmClear(true)}
            >
              Clear key
            </button>
          )
        )}
      </div>
      {result && (
        <div className="settingsInlineResult" role="status">
          <Check size={13} /> {result}
        </div>
      )}
    </div>
  );
}

function SettingsSectionView({
  title,
  description,
  children
}: {
  title: string;
  description: string;
  children: ReactNode;
}) {
  return (
    <section className="settingsSection">
      <h3>{title}</h3>
      <p className="settingsIntro">{description}</p>
      <div className="settingsRows">{children}</div>
    </section>
  );
}

function SettingRow({
  title,
  description,
  children
}: {
  title: string;
  description: string;
  children: ReactNode;
}) {
  return (
    <div className="settingsRow">
      <span>
        <strong>{title}</strong>
        <small>{description}</small>
      </span>
      {children}
    </div>
  );
}

function SelectControl({
  label,
  value,
  values,
  disabled,
  format = titleCase,
  onChange
}: {
  label: string;
  value: string;
  values: readonly string[];
  disabled?: boolean;
  format?: (value: string) => string;
  onChange: (value: string) => void;
}) {
  return (
    <label className="settingsSelectWrap">
      <select
        className="settingsSelect"
        aria-label={label}
        value={value}
        disabled={disabled}
        onChange={(event) => onChange(event.target.value)}
      >
        {values.map((option) => (
          <option key={option} value={option}>{format(option)}</option>
        ))}
      </select>
      <ChevronDown size={15} />
    </label>
  );
}

function ThemeChoice({
  label,
  icon,
  selected,
  onClick
}: {
  label: string;
  icon: ReactNode;
  selected: boolean;
  onClick: () => void;
}) {
  return (
    <button
      type="button"
      role="radio"
      aria-checked={selected}
      onClick={onClick}
    >
      {icon}<span>{label}</span>
    </button>
  );
}

function mutable(snapshot: RuntimeSnapshot, field: keyof SessionProfile): boolean {
  return snapshot.profile?.capabilities.mutable_fields.includes(field) ?? false;
}

function titleCase(value: string): string {
  return value
    .replaceAll("_", " ")
    .replace(/\b\w/g, (letter) => letter.toUpperCase());
}

function settingsProfileDraft(snapshot: RuntimeSnapshot): ProfileDraft {
  const profile = snapshot.profile?.profile;
  return settingsProfileDraftFromProfile(profile ?? {
    version: 1,
    revision: 1,
    mode: "act",
    planning_policy: "adaptive",
    provider: "",
    model: "",
    approval_posture: "auto",
    execution_target: "local",
    max_steps: 64,
    prompt_cache_revision: 1
  }, snapshot.tools);
}

function settingsProfileDraftFromProfile(
  profile: SessionProfile,
  tools: RuntimeSnapshot["tools"]
): ProfileDraft {
  const enabledToolIDs = profile.enabled_tool_ids?.length
    ? [...profile.enabled_tool_ids]
    : tools.filter((tool) => tool.enabled).map((tool) => tool.id);
  return {
    mode: profile.mode,
    provider: profile.provider,
    model: profile.model,
    reasoningEffort: profile.reasoning_effort ?? "",
    enabledToolIDs: enabledToolIDs.sort(),
    approvalPosture: profile.approval_posture,
    executionTarget: profile.execution_target,
    maxSteps: profile.max_steps
  };
}

function equalProfileDraft(
  left: ProfileDraft,
  right: ProfileDraft
): boolean {
  return left.mode === right.mode &&
    left.provider === right.provider &&
    left.model === right.model &&
    left.reasoningEffort === right.reasoningEffort &&
    left.approvalPosture === right.approvalPosture &&
    left.executionTarget === right.executionTarget &&
    left.maxSteps === right.maxSteps &&
    arraysEqual(left.enabledToolIDs, right.enabledToolIDs);
}

function modelIDProblem(modelID: string): string {
  const value = modelID.trim();
  if (!value) return "Model ID is required";
  if (value.length > 256) return "Model ID must be 256 characters or fewer";
  if (/\s/.test(value)) return "Model ID cannot contain whitespace";
  return "";
}

function changedProfileFields(
  baseline: ProfileDraft,
  draft: ProfileDraft
): Record<string, unknown> {
  const patch: Record<string, unknown> = {};
  if (draft.mode !== baseline.mode) {
    patch.mode = draft.mode;
    patch.planning_policy = "adaptive";
  }
  if (draft.provider !== baseline.provider) patch.provider = draft.provider;
  if (draft.model !== baseline.model) patch.model = draft.model;
  if (draft.reasoningEffort !== baseline.reasoningEffort) {
    patch.reasoning_effort = draft.reasoningEffort;
  }
  if (draft.approvalPosture !== baseline.approvalPosture) {
    patch.approval_posture = draft.approvalPosture;
  }
  if (draft.executionTarget !== baseline.executionTarget) {
    patch.execution_target = draft.executionTarget;
  }
  if (draft.maxSteps !== baseline.maxSteps) patch.max_steps = draft.maxSteps;
  if (
    draft.enabledToolIDs.length !== baseline.enabledToolIDs.length ||
    draft.enabledToolIDs.some((id, index) => id !== baseline.enabledToolIDs[index])
  ) {
    patch.enabled_tool_ids = draft.enabledToolIDs;
  }
  return patch;
}

function agentPresetProfile(draft: ProfileDraft): AgentPresetProfile {
  return {
    mode: draft.mode,
    planning_policy: "adaptive",
    provider: draft.provider,
    model: draft.model,
    reasoning_effort: draft.reasoningEffort,
    enabled_tool_ids: [...draft.enabledToolIDs],
    approval_posture: draft.approvalPosture,
    execution_target: draft.executionTarget,
    max_steps: draft.maxSteps
  };
}

function profileDraftFromPreset(
  profile: AgentPresetProfile,
  tools: RuntimeSnapshot["tools"]
): ProfileDraft {
  return {
    mode: profile.mode,
    provider: profile.provider,
    model: profile.model,
    reasoningEffort: profile.reasoning_effort ?? "",
    enabledToolIDs: profile.enabled_tool_ids?.length
      ? [...profile.enabled_tool_ids].sort()
      : tools.filter((tool) => tool.enabled).map((tool) => tool.id).sort(),
    approvalPosture: profile.approval_posture,
    executionTarget: profile.execution_target,
    maxSteps: profile.max_steps
  };
}

function profileApplyNotice(
  result: SessionProfileUpdateResult,
  before?: ProfileDraft,
  after?: ProfileDraft
): ApplyNotice {
  const changes = before && after ? [
    before.model !== after.model && `Model ${before.model} → ${after.model}`,
    before.mode !== after.mode && `Mode ${before.mode} → ${after.mode}`,
    before.reasoningEffort !== after.reasoningEffort &&
      `Reasoning ${before.reasoningEffort || "default"} → ${
        after.reasoningEffort || "default"
      }`,
    before.approvalPosture !== after.approvalPosture &&
      `Approval ${before.approvalPosture} → ${after.approvalPosture}`,
    before.maxSteps !== after.maxSteps && `Maximum steps ${after.maxSteps}`,
    !arraysEqual(before.enabledToolIDs, after.enabledToolIDs) && "Tools updated"
  ].filter((value): value is string => Boolean(value)) : [];
  const cache = result.prompt_cache_reset ? " · Prompt cache reset" : "";
  return {
    tone: "success",
    text: `${changes.length ? `Applied: ${changes.join("; ")}` : "Applied to current Session"}${cache}`
  };
}

function arraysEqual(left: readonly string[], right: readonly string[]): boolean {
  return left.length === right.length &&
    left.every((value, index) => value === right[index]);
}

function uniquePresetName(
  candidate: string,
  presets: readonly AgentPreset[]
): string {
  const names = new Set(presets.map((preset) => preset.name.toLocaleLowerCase()));
  if (!names.has(candidate.toLocaleLowerCase())) return candidate;
  for (let suffix = 2; suffix <= presets.length + 2; suffix += 1) {
    const value = `${candidate} ${suffix}`;
    if (!names.has(value.toLocaleLowerCase())) return value;
  }
  return `${candidate} ${Date.now()}`;
}
