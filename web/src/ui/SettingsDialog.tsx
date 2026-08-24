import {
  Activity,
  Bot,
  Boxes,
  ChevronDown,
  Database,
  KeyRound,
  Monitor,
  Moon,
  RefreshCw,
  Search,
  Settings2,
  Sun,
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
  CredentialStatus,
  ModelCatalogEntry,
  SessionProfile
} from "../protocol";
import type {RuntimeClient, RuntimeSnapshot} from "../runtime/client";
import "./SettingsDialog.css";

export type ThemeMode = "light" | "dark" | "system";

type SettingsSection = "general" | "models" | "tools" | "extensions" | "agent";

interface Props {
  snapshot: RuntimeSnapshot;
  client: RuntimeClient;
  newIsolation: "shared" | "worktree";
  theme: ThemeMode;
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
  onIsolationChange,
  onThemeChange,
  onClose,
  onError
}: Props) {
  const [active, setActive] = useState<SettingsSection>("general");
  const [credential, setCredential] = useState<CredentialStatus>();
  const [diagnostics, setDiagnostics] = useState("");
  const [toolQuery, setToolQuery] = useState("");
  const [error, setError] = useState("");
  const closeRef = useRef<HTMLButtonElement>(null);
  const reportError = useCallback((value: unknown) => {
    setError(value instanceof Error ? value.message : String(value));
    onError(value);
  }, [onError]);
  useEffect(() => {
    closeRef.current?.focus();
    const close = (event: KeyboardEvent) => {
      if (event.key === "Escape") onClose();
    };
    window.addEventListener("keydown", close);
    return () => window.removeEventListener("keydown", close);
  }, [onClose]);

  useEffect(() => {
    void client.credentialStatus().then(setCredential, reportError);
  }, [client, reportError]);

  const loadDiagnostics = () => {
    void client.diagnostics().then(
      (value) => {
        setDiagnostics(JSON.stringify(value, null, 2));
        setActive("general");
      },
      reportError
    );
  };

  return (
    <div className="settingsOverlay" role="presentation" onMouseDown={(event) => {
      if (event.target === event.currentTarget) onClose();
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
                  aria-current={active === section.id ? "page" : undefined}
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
              type="button"
              className="settingsHeaderAction"
              onClick={loadDiagnostics}
            >
              <Activity size={14} /> Runtime diagnostics
            </button>
            <button
              ref={closeRef}
              type="button"
              className="settingsClose"
              aria-label="Close settings"
              onClick={onClose}
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
                diagnostics={diagnostics}
                onIsolationChange={onIsolationChange}
                onThemeChange={onThemeChange}
              />
            )}
            {active === "models" && (
              <ModelSettings
                snapshot={snapshot}
                client={client}
                credential={credential}
                onCredential={setCredential}
                onError={reportError}
              />
            )}
            {active === "tools" && (
              <ToolSettings
                snapshot={snapshot}
                client={client}
                query={toolQuery}
                onQueryChange={setToolQuery}
                onError={reportError}
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
                onError={reportError}
              />
            )}
          </div>
        </div>
      </section>
    </div>
  );
}

function GeneralSettings({
  snapshot,
  isolation,
  theme,
  diagnostics,
  onIsolationChange,
  onThemeChange
}: {
  snapshot: RuntimeSnapshot;
  isolation: "shared" | "worktree";
  theme: ThemeMode;
  diagnostics: string;
  onIsolationChange: (value: "shared" | "worktree") => void;
  onThemeChange: (value: ThemeMode) => void;
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
        title="Runtime connection"
        description={snapshot.workspaceRoot || "Workspace unavailable"}
      >
        <span className="settingsStatus" data-ready={snapshot.socketConnected || undefined}>
          <span />
          {snapshot.socketConnected ? "Connected" : snapshot.phase}
        </span>
      </SettingRow>
      {diagnostics && (
        <div className="settingsBlock">
          <div className="settingsBlockTitle">Runtime diagnostics</div>
          <pre className="settingsCode">{diagnostics}</pre>
        </div>
      )}
    </SettingsSectionView>
  );
}

function ModelSettings({
  snapshot,
  client,
  credential,
  onCredential,
  onError
}: {
  snapshot: RuntimeSnapshot;
  client: RuntimeClient;
  credential?: CredentialStatus;
  onCredential: (status: CredentialStatus) => void;
  onError: (error: unknown) => void;
}) {
  const profile = snapshot.profile?.profile;
  const selectedModel = snapshot.models.find(
    (model) => model.provider === profile?.provider && model.id === profile.model
  );
  const changeProvider = (provider: string) => {
    const model = snapshot.models.find(
      (entry) => entry.provider === provider &&
        entry.capabilities.availability === "available"
    );
    if (!model) return;
    void client.updateProfile({
      provider,
      model: model.id,
      reasoning_effort: model.capabilities.default_reasoning_effort ?? ""
    }).catch(onError);
  };
  return (
    <SettingsSectionView
      title="Models"
      description="Provider routing, model capabilities, and credential status."
    >
      <SettingRow title="Provider" description="Runtime model provider for this session.">
        <SelectControl
          label="Provider"
          value={profile?.provider ?? ""}
          values={snapshot.providers
            .filter((provider) => provider.availability === "available")
            .map((provider) => provider.id)}
          disabled={!mutable(snapshot, "provider")}
          onChange={changeProvider}
        />
      </SettingRow>
      <SettingRow title="Model" description="Changing model resets the prompt cache.">
        <select
          className="settingsSelect"
          aria-label="Settings model"
          value={profile?.model ?? ""}
          disabled={!mutable(snapshot, "model")}
          onChange={(event) => {
            const target = snapshot.models.find(
              (model) =>
                model.provider === profile?.provider &&
                model.id === event.target.value
            );
            void client.updateProfile({
              model: event.target.value,
              reasoning_effort: target?.capabilities.default_reasoning_effort ?? ""
            }).catch(onError);
          }}
        >
          {snapshot.models
            .filter((model) => model.provider === profile?.provider)
            .map((model) => (
              <option
                value={model.id}
                key={`${model.provider}:${model.id}`}
                disabled={model.capabilities.availability !== "available"}
              >
                {model.capabilities.display_name || model.id}
              </option>
            ))}
        </select>
      </SettingRow>
      {selectedModel && <ModelCapabilityPanel model={selectedModel} />}
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
        <small>{capabilities.selection_mode.replaceAll("_", " ")}</small>
      </div>
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
  client,
  query,
  onQueryChange,
  onError
}: {
  snapshot: RuntimeSnapshot;
  client: RuntimeClient;
  query: string;
  onQueryChange: (value: string) => void;
  onError: (error: unknown) => void;
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
        {tools.map((tool) => (
          <label className="settingsCatalogRow" key={tool.id}>
            <Wrench size={15} />
            <span>
              <strong>{tool.name}</strong>
              <small>{tool.description || tool.source_label}</small>
            </span>
            <span className="settingsTags">
              <small>{tool.risk_level}</small>
              {tool.guarded && <small>guarded</small>}
            </span>
            <input
              type="checkbox"
              checked={tool.enabled}
              disabled={
                tool.availability !== "available" ||
                !mutable(snapshot, "enabled_tool_ids")
              }
              onChange={(event) => {
                void client.setToolEnabled(tool.id, event.target.checked).catch(onError);
              }}
            />
          </label>
        ))}
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
  return (
    <SettingsSectionView
      title="Extensions"
      description="Installed skills and plugins visible to the Runtime."
    >
      {snapshot.extensions.length === 0 ? (
        <p className="settingsEmpty">No extensions are registered.</p>
      ) : (
        <div className="settingsCatalog settingsExtensionGrid">
          {snapshot.extensions.map((extension) => (
            <label
              className="settingsCatalogRow"
              key={`${extension.kind}:${extension.name}`}
            >
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
              <input
                type="checkbox"
                checked={extension.enabled}
                onChange={(event) => {
                  void client.setExtensionEnabled(
                    extension.kind,
                    extension.name,
                    event.target.checked
                  ).catch(onError);
                }}
              />
            </label>
          ))}
        </div>
      )}
    </SettingsSectionView>
  );
}

function AgentSettings({
  snapshot,
  client,
  onError
}: {
  snapshot: RuntimeSnapshot;
  client: RuntimeClient;
  onError: (error: unknown) => void;
}) {
  const profile = snapshot.profile?.profile;
  return (
    <SettingsSectionView
      title="Agent preset"
      description="These changes apply immediately to the active session."
    >
      <SettingRow title="Mode" description="Plan before acting or execute the requested task.">
        <SelectControl
          label="Agent mode"
          value={profile?.mode ?? "act"}
          values={["plan", "act", "operate"]}
          disabled={!mutable(snapshot, "mode")}
          onChange={(mode) => void client.updateProfile({mode}).catch(onError)}
        />
      </SettingRow>
      <SettingRow title="Approval" description="Control when consequential actions ask first.">
        <SelectControl
          label="Approval posture"
          value={profile?.approval_posture ?? "suggest"}
          values={["suggest", "auto", "never"]}
          disabled={!mutable(snapshot, "approval_posture")}
          onChange={(approval_posture) => {
            void client.updateProfile({approval_posture}).catch(onError);
          }}
        />
      </SettingRow>
      <SettingRow title="Execution" description="Choose the execution isolation target.">
        <SelectControl
          label="Execution target"
          value={profile?.execution_target ?? "local"}
          values={["local", "sandbox"]}
          disabled={!mutable(snapshot, "execution_target")}
          onChange={(execution_target) => {
            void client.updateProfile({execution_target}).catch(onError);
          }}
        />
      </SettingRow>
      <NumberSetting
        profile={profile}
        disabled={!mutable(snapshot, "max_steps")}
        onCommit={(max_steps) => client.updateProfile({max_steps})}
        onError={onError}
      />
      {(snapshot.tasks.length > 0 || snapshot.agents.length > 0 || snapshot.usage) && (
        <div className="settingsBlock">
          <div className="settingsBlockTitle">Current activity</div>
          {snapshot.usage && (
            <dl className="settingsFacts" aria-label="Usage">
              <div><dt>Turns</dt><dd>{snapshot.usage.turns}</dd></div>
              <div><dt>Calls</dt><dd>{snapshot.usage.calls}</dd></div>
              <div><dt>Tokens</dt><dd>{snapshot.usage.total_tokens.toLocaleString()}</dd></div>
              <div>
                <dt>Cost</dt>
                <dd>
                  {snapshot.usage.cost_known
                    ? `${snapshot.usage.cost_microunits} µ`
                    : "Unpriced"}
                </dd>
              </div>
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
      {snapshot.plan && (
        <div className="settingsBlock">
          <div className="settingsBlockTitle">Current plan</div>
          <p className="settingsSummary">{snapshot.plan.body}</p>
          <div className="settingsButtonRow">
            <button
              type="button"
              disabled={!snapshot.plan.can_implement}
              onClick={() => void client.transitionPlan("implement").catch(onError)}
            >
              Implement
            </button>
            <button
              type="button"
              disabled={!snapshot.plan.can_autopilot}
              onClick={() => void client.transitionPlan("autopilot").catch(onError)}
            >
              Autopilot
            </button>
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
  profile,
  disabled,
  onCommit,
  onError
}: {
  profile?: SessionProfile;
  disabled: boolean;
  onCommit: (value: number) => Promise<void>;
  onError: (error: unknown) => void;
}) {
  const [value, setValue] = useState(String(profile?.max_steps ?? 0));
  useEffect(() => setValue(String(profile?.max_steps ?? 0)), [profile?.max_steps]);
  const commit = () => {
    const next = Number(value);
    if (!Number.isInteger(next) || next < 1 || next === profile?.max_steps) return;
    void onCommit(next).catch(onError);
  };
  return (
    <SettingRow title="Maximum steps" description="Hard limit for one agent turn.">
      <input
        className="settingsNumber"
        aria-label="Maximum steps"
        type="number"
        min={1}
        value={value}
        disabled={disabled}
        onChange={(event) => setValue(event.target.value)}
        onBlur={commit}
        onKeyDown={(event) => {
          if (event.key === "Enter") commit();
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
  const run = async (operation: () => Promise<unknown>, clear = false) => {
    if (pending) return;
    setPending(true);
    try {
      await operation();
      if (clear) setSecret("");
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
      {status?.validation_detail && <p>{status.validation_detail}</p>}
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
          onClick={() => void run(() => onSet(secret), true)}
        >
          Set key
        </button>
      </div>
      <div className="settingsButtonRow">
        <button
          type="button"
          disabled={!status?.configured || pending}
          onClick={() => void run(onValidate)}
        >
          <RefreshCw size={13} /> Validate
        </button>
        {status?.reference.kind === "keyring" && (
          <button
            type="button"
            disabled={!status.configured || pending}
            onClick={() => void run(onClear)}
          >
            Clear key
          </button>
        )}
      </div>
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
  onChange
}: {
  label: string;
  value: string;
  values: readonly string[];
  disabled?: boolean;
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
          <option key={option} value={option}>{titleCase(option)}</option>
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
