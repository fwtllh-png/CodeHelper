import { execFile } from "node:child_process";
import { access } from "node:fs/promises";
import { join } from "node:path";

import * as vscode from "vscode";

import type { ResolvedBinary } from "../binary/source.js";
import type { WorkspaceRuntimeRegistry } from "../workspace/registry.js";
import {
  actionableChecks,
  decodeReadiness,
  repairMessage,
  runtimeFailureCheck,
  setupArguments,
} from "./flow.js";

interface ModelRow {
  readonly provider: string;
  readonly models: readonly string[];
}

interface CredentialSuggestion {
  readonly providerId: string;
  readonly kind: "env" | "file" | "keyring";
  readonly name: string;
}

export function registerSetupCommands(
  registry: WorkspaceRuntimeRegistry | undefined,
  output: vscode.LogOutputChannel,
): readonly vscode.Disposable[] {
  return [
    vscode.commands.registerCommand("codehelper.repairRuntime", async () => {
      await repairRuntime(registry, output);
    }),
    vscode.commands.registerCommand(
      "codehelper.runSetup",
      async (rootId?: unknown, provider?: unknown) => {
        await runSetup(
          registry,
          output,
          typeof rootId === "string" ? rootId : undefined,
          typeof provider === "string" ? provider : undefined,
        );
      },
    ),
    vscode.commands.registerCommand(
      "codehelper.configureCredential",
      async (rootId?: unknown, sessionId?: unknown) => {
        await configureCredential(
          registry,
          output,
          rootId,
          sessionId,
        );
      },
    ),
    vscode.commands.registerCommand("codehelper.runQuickstart", async () => {
      const root = await registry?.pick("CodeHelper: Run Quickstart");
      if (root === undefined) return;
      const binary = await root.controller.resolveBinary();
      const result = await runJSON(
        binary.path,
        ["quickstart", "--json"],
        root.folder.uri.fsPath,
      );
      output.info(`[quickstart:${root.label}] ${JSON.stringify(result.value)}`);
      if (result.code !== 0) {
        throw new Error(result.stderr || "CodeHelper quickstart failed");
      }
      void vscode.window.showInformationMessage(
        `${root.label}: CodeHelper Quickstart completed.`,
      );
    }),
  ];
}

export async function repairRuntime(
  registry: WorkspaceRuntimeRegistry | undefined,
  output: vscode.LogOutputChannel,
): Promise<void> {
  const root = await registry?.pick("CodeHelper: Repair Runtime");
  if (root === undefined) {
    void vscode.window.showErrorMessage(
      "CodeHelper Repair requires an open workspace folder.",
    );
    return;
  }
  let binary: ResolvedBinary;
  try {
    binary = await root.controller.resolveBinary();
  } catch (error) {
    const check = runtimeFailureCheck(errorMessage(error));
    output.error(`[repair:${root.label}] ${JSON.stringify(check)}`);
    const action = await vscode.window.showErrorMessage(
      repairMessage(check),
      { modal: true },
      "Open Settings",
      "Check Binary Updates",
      "Show Output",
    );
    if (action === "Open Settings") {
      await vscode.commands.executeCommand(
        "workbench.action.openSettings",
        "@ext:codehelper.codehelper binary",
      );
    } else if (action === "Check Binary Updates") {
      await vscode.commands.executeCommand("codehelper.checkBinaryUpdates");
    } else if (action === "Show Output") {
      output.show(true);
    }
    return;
  }
  const result = await vscode.window.withProgress(
    {
      location: vscode.ProgressLocation.Notification,
      title: `Inspecting CodeHelper readiness for ${root.label}`,
    },
    async () => runJSON(binary.path, ["doctor", "--json"], root.folder.uri.fsPath),
  );
  const report = decodeReadiness(result.value);
  output.info(`[repair:${root.label}] ${JSON.stringify(report)}`);
  const startup = root.controller.snapshot.state === "failed" &&
    root.controller.snapshot.error !== undefined
    ? [runtimeFailureCheck(root.controller.snapshot.error)]
    : [];
  const checks = [...startup, ...actionableChecks(report)];
  if (checks.length === 0) {
    void vscode.window.showInformationMessage(
      `${root.label}: CodeHelper Runtime is ready.`,
    );
    return;
  }
  const selected = await vscode.window.showQuickPick(
    checks.map((check) => ({
      label: `$(${check.status === "blocked" ? "error" : "warning"}) ${check.id}`,
      description: check.status,
      detail: check.reason,
      check,
    })),
    {
      title: `CodeHelper Repair: ${report.status}`,
      placeHolder: "Select a readiness issue to inspect",
      ignoreFocusOut: true,
    },
  );
  if (selected === undefined) return;
  const action = await vscode.window.showWarningMessage(
    repairMessage(selected.check),
    { modal: true },
    "Run Setup",
    "Copy Repair Action",
    "Restart Runtime",
  );
  if (action === "Run Setup") {
    await vscode.commands.executeCommand("codehelper.runSetup");
  } else if (action === "Copy Repair Action") {
    await vscode.env.clipboard.writeText(
      selected.check.action ?? selected.check.reason,
    );
  } else if (action === "Restart Runtime") {
    await root.controller.restart();
  }
}

export async function runSetup(
  registry: WorkspaceRuntimeRegistry | undefined,
  output: vscode.LogOutputChannel,
  requestedRootId?: string,
  requestedProvider?: string,
): Promise<void> {
  if (!vscode.workspace.isTrusted) {
    void vscode.window.showErrorMessage(
      "Trust this workspace before CodeHelper Setup writes configuration.",
    );
    return;
  }
  const root = requestedRootId === undefined
    ? await registry?.pick("CodeHelper: Setup Workspace")
    : registry?.find(requestedRootId);
  if (root === undefined) {
    void vscode.window.showErrorMessage(
      "CodeHelper Setup requires an open workspace folder.",
    );
    return;
  }
  const binary = await root.controller.resolveBinary();
  const modelResult = await runJSON(
    binary.path,
    ["model", "list", "--json"],
    root.folder.uri.fsPath,
  );
  if (modelResult.code !== 0) {
    throw new Error(modelResult.stderr || "CodeHelper model catalog failed");
  }
  const models = decodeModels(modelResult.value);
  const requestedRow = requestedProvider === undefined
    ? undefined
    : models.find((row) => row.provider === requestedProvider);
  if (requestedProvider !== undefined && requestedRow === undefined) {
    throw new Error("The current Provider is unavailable in the model catalog");
  }
  const provider = requestedRow === undefined
    ? await vscode.window.showQuickPick(
        models.map((row) => ({
          label: row.provider,
          description: `${String(row.models.length)} model(s)`,
          row,
        })),
        {
          title: "CodeHelper Setup: Provider",
          placeHolder: "Select the live model provider",
          ignoreFocusOut: true,
        },
      )
    : {
        label: requestedRow.provider,
        description: `${String(requestedRow.models.length)} model(s)`,
        row: requestedRow,
      };
  if (provider === undefined) return;
  const model = await vscode.window.showQuickPick(
    provider.row.models,
    {
      title: "CodeHelper Setup: Model",
      placeHolder: "Select the model",
      ignoreFocusOut: true,
    },
  );
  if (model === undefined) return;
  const suggestionsResult = await runJSON(
    binary.path,
    ["auth", "suggestions", "--json"],
    root.folder.uri.fsPath,
  );
  const suggestion = suggestionsResult.code === 0
    ? decodeSuggestions(suggestionsResult.value).find(
      (candidate) => candidate.providerId === provider.row.provider,
    )
    : undefined;
  const credentialChoice = await vscode.window.showQuickPick(
    [
      {
        label: "$(key) VS Code SecretStorage",
        description: "Recommended",
        credentialKind: "secret-storage" as const,
      },
      { label: "Environment variable", credentialKind: "env" as const },
      { label: "Protected file", credentialKind: "file" as const },
      { label: "OS keyring reference", credentialKind: "keyring" as const },
    ],
    {
      title: "CodeHelper Setup: Credential",
      placeHolder: "Select where the credential is stored",
      ignoreFocusOut: true,
    },
  );
  if (credentialChoice === undefined) return;
  let credentialKind: "env" | "file" | "keyring";
  let credentialName: string;
  let secret: string | undefined;
  if (credentialChoice.credentialKind === "secret-storage") {
    secret = await vscode.window.showInputBox({
      title: `CodeHelper Setup: ${provider.row.provider} API Key`,
      prompt: "Stored in VS Code SecretStorage. It is never sent to the Chat Webview.",
      password: true,
      ignoreFocusOut: true,
      validateInput: validateSecret,
    });
    if (secret === undefined) return;
    credentialKind = "env";
    credentialName = root.controller.credentialReference(provider.row.provider);
  } else {
    credentialKind = credentialChoice.credentialKind;
    const reference = await vscode.window.showInputBox({
      title: "CodeHelper Setup: Credential Reference",
      prompt: "Enter an environment variable, protected file path, or keyring key. Do not enter a secret value.",
      value: suggestion?.kind === credentialKind ? suggestion.name : "",
      ignoreFocusOut: true,
      validateInput: (value) => value.trim().length === 0
        ? "A non-secret credential reference is required"
        : undefined,
    });
    if (reference === undefined) return;
    credentialName = reference.trim();
  }

  const configPath = join(root.folder.uri.fsPath, "codehelper.toml");
  const force = await exists(configPath)
    ? await confirmReplace(configPath)
    : false;
  if (force === undefined) return;
  if (secret !== undefined) {
    await root.controller.storeCredential(provider.row.provider, secret);
  }
  const args = setupArguments({
    workspace: root.folder.uri.fsPath,
    configPath,
    provider: provider.row.provider,
    model,
    credentialKind,
    credentialName: credentialName.trim(),
    force,
  });
  const setupResult = await vscode.window.withProgress(
    {
      location: vscode.ProgressLocation.Notification,
      title: `Configuring CodeHelper for ${root.label}`,
    },
    async () => runJSON(
      binary.path,
      args,
      root.folder.uri.fsPath,
      secret === undefined
        ? undefined
        : { ...process.env, [credentialName]: secret },
    ),
  );
  output.info(`[setup:${root.label}] ${JSON.stringify(setupResult.value)}`);
  if (setupResult.code !== 0) {
    throw new Error(setupResult.stderr || "CodeHelper Setup failed");
  }
  const report = decodeReadiness(setupResult.value);
  if (secret !== undefined) {
    await root.controller.activateCredentialProvider(provider.row.provider);
  } else {
    await root.controller.activateCredentialProvider();
  }
  await vscode.workspace.getConfiguration("codehelper", root.folder.uri)
    .update(
      "runtime.configPath",
      configPath,
      vscode.ConfigurationTarget.WorkspaceFolder,
    );
  await root.controller.restart();
  const action = actionableChecks(report).length === 0
    ? "ready"
    : `${report.status}; run CodeHelper: Repair Runtime for next actions`;
  void vscode.window.showInformationMessage(
    `${root.label}: CodeHelper Setup completed (${action}).`,
  );
}

async function configureCredential(
  registry: WorkspaceRuntimeRegistry | undefined,
  output: vscode.LogOutputChannel,
  rootId: unknown,
  sessionId: unknown,
): Promise<void> {
  if (!vscode.workspace.isTrusted) {
    throw new Error(
      "Trust this workspace before configuring a CodeHelper credential.",
    );
  }
  const root = typeof rootId === "string"
    ? registry?.find(rootId)
    : await registry?.pick("CodeHelper: Configure Credential");
  if (root === undefined) {
    throw new Error("Credential configuration requires a workspace root");
  }
  const sessions = root.controller.sessions();
  const resolvedSessionId = typeof sessionId === "string"
    ? sessionId
    : sessions.find((session) => session.selected)?.sessionId;
  if (resolvedSessionId === undefined ||
    !sessions.some((session) => session.sessionId === resolvedSessionId)) {
    throw new Error("Credential configuration target is unknown or stale");
  }
  const profile = await root.controller.sessionProfile(resolvedSessionId);
  const secret = await vscode.window.showInputBox({
    title: `CodeHelper: ${profile.profile.provider} API Key`,
    prompt: "Stored in VS Code SecretStorage. It is never sent to the Chat Webview.",
    password: true,
    ignoreFocusOut: true,
    validateInput: validateSecret,
  });
  if (secret === undefined) return;
  const configuration = vscode.workspace.getConfiguration(
    "codehelper",
    root.folder.uri,
  );
  const configPath = configuration.get<string>("runtime.configPath", "").trim();
  if (configPath.length === 0) {
    throw new Error(
      "Run CodeHelper Setup before configuring a SecretStorage credential.",
    );
  }
  const reference = root.controller.credentialReference(profile.profile.provider);
  await root.controller.storeCredential(profile.profile.provider, secret);
  const binary = await root.controller.resolveBinary();
  const result = await runCommand(
    binary.path,
    [
      "auth", "login",
      "--config", configPath,
      "--kind", "env",
      "--name", reference,
    ],
    root.folder.uri.fsPath,
  );
  if (result.code !== 0) {
    throw new Error(result.stderr || "CodeHelper credential configuration failed");
  }
  await root.controller.activateCredentialProvider(profile.profile.provider);
  output.info(
    `[credential:${root.label}] provider=${profile.profile.provider} ` +
    "status=configured source=secret-storage",
  );
  await root.controller.restart();
  void vscode.window.showInformationMessage(
    `${root.label}: ${profile.profile.provider} credential configured.`,
  );
}

interface JSONResult {
  readonly value: unknown;
  readonly code: number;
  readonly stderr: string;
}

function runJSON(
  binary: string,
  args: readonly string[],
  cwd: string,
  environment?: NodeJS.ProcessEnv,
): Promise<JSONResult> {
  return new Promise((resolve, reject) => {
    execFile(
      binary,
      [...args],
      {
        cwd,
        encoding: "utf8",
        maxBuffer: 4 << 20,
        timeout: 60_000,
        windowsHide: true,
        ...(environment === undefined ? {} : { env: environment }),
      },
      (error, stdout, stderr) => {
        if (stdout.trim().length === 0) {
          reject(new Error(stderr.trim() ||
            (error instanceof Error ? error.message : "CodeHelper command failed")));
          return;
        }
        try {
          resolve({
            value: JSON.parse(stdout) as unknown,
            code: typeof error?.code === "number" ? error.code : 0,
            stderr: stderr.trim(),
          });
        } catch (parseError) {
          reject(new Error(
            `CodeHelper returned invalid JSON: ${
              parseError instanceof Error ? parseError.message : String(parseError)
            }`,
          ));
        }
      },
    );
  });
}

function runCommand(
  binary: string,
  args: readonly string[],
  cwd: string,
): Promise<{ readonly code: number; readonly stderr: string }> {
  return new Promise((resolve) => {
    execFile(
      binary,
      [...args],
      {
        cwd,
        encoding: "utf8",
        maxBuffer: 64 << 10,
        timeout: 30_000,
        windowsHide: true,
      },
      (error, _stdout, stderr) => {
        resolve({
          code: error === null
            ? 0
            : typeof error.code === "number" ? error.code : 1,
          stderr: stderr.trim(),
        });
      },
    );
  });
}

function validateSecret(value: string): string | undefined {
  if (value.trim().length === 0) return "An API key is required";
  if (value.length > 32 << 10) return "The API key is too large";
  return undefined;
}

function decodeModels(value: unknown): readonly ModelRow[] {
  if (!Array.isArray(value)) {
    throw new TypeError("CodeHelper model catalog output is invalid");
  }
  return value.map((row) => {
    if (!isObject(row) ||
      typeof row["provider"] !== "string" ||
      !Array.isArray(row["models"]) ||
      row["models"].some((model) => typeof model !== "string")) {
      throw new TypeError("CodeHelper model catalog row is invalid");
    }
    return {
      provider: row["provider"],
      models: row["models"] as readonly string[],
    };
  });
}

function decodeSuggestions(value: unknown): readonly CredentialSuggestion[] {
  if (!isObject(value) || !Array.isArray(value["suggestions"])) return [];
  return value["suggestions"].flatMap((entry) => {
    if (!isObject(entry) ||
      typeof entry["provider_id"] !== "string" ||
      !isObject(entry["credential"]) ||
      !isCredentialKind(entry["credential"]["kind"]) ||
      typeof entry["credential"]["name"] !== "string") {
      return [];
    }
    return [{
      providerId: entry["provider_id"],
      kind: entry["credential"]["kind"],
      name: entry["credential"]["name"],
    }];
  });
}

async function exists(path: string): Promise<boolean> {
  try {
    await access(path);
    return true;
  } catch {
    return false;
  }
}

async function confirmReplace(path: string): Promise<boolean | undefined> {
  const action = await vscode.window.showWarningMessage(
    `CodeHelper configuration already exists at ${path}.`,
    { modal: true },
    "Replace",
  );
  return action === "Replace" ? true : undefined;
}

function isCredentialKind(
  value: unknown,
): value is "env" | "file" | "keyring" {
  return value === "env" || value === "file" || value === "keyring";
}

function isObject(value: unknown): value is Readonly<Record<string, unknown>> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function errorMessage(value: unknown): string {
  return value instanceof Error ? value.message : String(value);
}
