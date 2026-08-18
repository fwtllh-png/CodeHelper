import { constants } from "node:fs";
import { access } from "node:fs/promises";
import {
  delimiter,
  extname,
  isAbsolute,
  join,
  relative,
  resolve,
  sep,
} from "node:path";
import {
  execFile,
  spawn,
  type ChildProcessWithoutNullStreams,
} from "node:child_process";
import { promisify } from "node:util";

import { AcpClient } from "./client.js";
import type { RuntimePosture } from "../security/trust.js";
import type { WorkspaceIdentity } from "../workspace/identity.js";

const execFileAsync = promisify(execFile);
const stderrLimit = 64 << 10;

export interface BinaryDiscoveryOptions {
  readonly configuredPath?: string;
  readonly developmentRoot?: string;
  readonly pathEnvironment?: string;
  readonly platform?: NodeJS.Platform;
}

export interface BinaryVersion {
  readonly name: "codehelper";
  readonly version: string;
  readonly commit: string;
  readonly os: string;
  readonly arch: string;
  readonly acpProtocolMin: number;
  readonly acpProtocolMax: number;
  readonly operationSchemaVersion: number;
}

export interface RuntimeLaunchOptions {
  readonly binaryPath: string;
  readonly workspaceRoot: string;
  readonly dataDirectory: string;
  readonly configPath?: string;
  readonly mcpConfigPath?: string;
  readonly environment?: Readonly<Record<string, string>>;
  readonly posture: RuntimePosture;
  readonly maxSteps: number;
  readonly workspaceIdentity: WorkspaceIdentity;
  readonly diagnostics: (line: string) => void;
}

export interface RuntimeExit {
  readonly code: number | null;
  readonly signal: NodeJS.Signals | null;
  readonly stderr: string;
}

export class RuntimeProcess {
  public readonly client: AcpClient;
  public readonly exited: Promise<RuntimeExit>;
  readonly #child: ChildProcessWithoutNullStreams;
  #stderr = "";
  #stopping = false;

  public constructor(
    child: ChildProcessWithoutNullStreams,
    diagnostics: (line: string) => void,
  ) {
    this.#child = child;
    this.client = new AcpClient(child.stdout, child.stdin);
    child.stderr.setEncoding("utf8");
    child.stderr.on("data", (chunk: string) => {
      diagnostics(chunk.slice(-stderrLimit));
      this.#stderr = `${this.#stderr}${chunk}`.slice(-stderrLimit);
    });
    this.exited = new Promise((resolve) => {
      child.once("exit", (code, signal) => {
        this.client.close(`CodeHelper Runtime exited with code ${String(code)}`);
        resolve({ code, signal, stderr: this.#stderr });
      });
    });
  }

  public get stopping(): boolean {
    return this.#stopping;
  }

  public get pid(): number | undefined {
    return this.#child.pid;
  }

  public async startupFailure(cause: unknown): Promise<Error> {
    const exit = await Promise.race([
      this.exited,
      delay(100).then(() => undefined),
    ]);
    return enrichRuntimeError(cause, exit?.stderr ?? "");
  }

  public async stop(): Promise<void> {
    if (this.#stopping) {
      await this.exited;
      return;
    }
    this.#stopping = true;
    try {
      await Promise.race([
        this.client.request("shutdown"),
        delay(2_000).then(() => {
          throw new Error("Runtime shutdown timed out");
        }),
      ]);
    } catch {
      this.#child.kill("SIGTERM");
    }
    const exited = await Promise.race([
      this.exited.then(() => true),
      delay(2_000).then(() => false),
    ]);
    if (!exited) {
      this.#child.kill("SIGKILL");
      await this.exited;
    }
  }
}

export async function discoverBinary(
  options: BinaryDiscoveryOptions,
): Promise<string> {
  if (options.configuredPath !== undefined) {
    if (!isAbsolute(options.configuredPath)) {
      throw new Error("codehelper.binaryPath must be an absolute path");
    }
    if (!await isExecutable(options.configuredPath)) {
      throw new Error(`configured CodeHelper binary is not executable: ${options.configuredPath}`);
    }
    return options.configuredPath;
  }
  const platform = options.platform ?? process.platform;
  const names = platform === "win32" ? ["codehelper.exe", "codehelper.cmd"] : ["codehelper"];
  if (options.developmentRoot !== undefined) {
    for (const name of names) {
      const candidate = join(options.developmentRoot, "bin", name);
      if (await isExecutable(candidate)) {
        return candidate;
      }
    }
  }
  const pathEnvironment = options.pathEnvironment ?? process.env["PATH"] ?? "";
  for (const directory of pathEnvironment.split(delimiter)) {
    if (directory.length === 0 || !isAbsolute(directory)) {
      continue;
    }
    for (const name of names) {
      const candidate = join(directory, name);
      if (await isExecutable(candidate)) {
        return candidate;
      }
    }
  }
  throw new Error(
    "CodeHelper binary was not found; configure codehelper.binaryPath or add codehelper to PATH",
  );
}

export async function verifyBinary(binaryPath: string): Promise<BinaryVersion> {
  let stdout: string;
  try {
    ({ stdout } = await execFileAsync(binaryPath, ["version", "--json"], {
      encoding: "utf8",
      maxBuffer: 64 << 10,
      windowsHide: true,
    }));
  } catch (error) {
    throw new Error(`failed to inspect CodeHelper binary: ${asError(error).message}`);
  }
  let value: unknown;
  try {
    value = JSON.parse(stdout);
  } catch (error) {
    throw new Error(`CodeHelper version output is invalid JSON: ${asError(error).message}`);
  }
  if (!isObject(value) ||
    value["name"] !== "codehelper" ||
    typeof value["version"] !== "string" ||
    typeof value["commit"] !== "string" ||
    typeof value["os"] !== "string" ||
    typeof value["arch"] !== "string" ||
    !Number.isSafeInteger(value["acp_protocol_min"]) ||
    !Number.isSafeInteger(value["acp_protocol_max"]) ||
    !Number.isSafeInteger(value["operation_schema_version"])) {
    throw new Error("configured executable is not a compatible CodeHelper binary");
  }
  return {
    name: "codehelper",
    version: value["version"],
    commit: value["commit"],
    os: value["os"],
    arch: value["arch"],
    acpProtocolMin: value["acp_protocol_min"] as number,
    acpProtocolMax: value["acp_protocol_max"] as number,
    operationSchemaVersion: value["operation_schema_version"] as number,
  };
}

export async function launchRuntime(
  options: RuntimeLaunchOptions,
): Promise<RuntimeProcess> {
  const args = runtimeArguments(options);
  const child = spawn(options.binaryPath, args, {
    cwd: options.workspaceRoot,
    env: { ...process.env, ...options.environment },
    stdio: ["pipe", "pipe", "pipe"],
    windowsHide: true,
  });
  await new Promise<void>((resolve, reject) => {
    child.once("spawn", resolve);
    child.once("error", reject);
  });
  return new RuntimeProcess(child, options.diagnostics);
}

export function runtimeArguments(options: RuntimeLaunchOptions): readonly string[] {
  return [
    "host",
    "--adapter", "acp",
    "--data-dir", options.dataDirectory,
    ...(options.configPath === undefined ? [] : ["--config", options.configPath]),
    ...(options.mcpConfigPath === undefined
      ? []
      : ["--mcp-config", options.mcpConfigPath]),
    "--workspace", options.workspaceRoot,
    "--workspace-uri", options.workspaceIdentity.editor_uri,
    "--workspace-root-id", options.workspaceIdentity.root_id,
    "--posture", options.posture,
    "--max-steps", String(options.maxSteps),
    "--enable-tools",
  ];
}

export function resolveMCPConfigPath(
  workspaceRoot: string,
  configuredPath: string,
): string | undefined {
  const path = configuredPath.trim();
  if (path.length === 0) return undefined;
  if (isAbsolute(path)) return path;
  const candidate = resolve(workspaceRoot, path);
  const workspaceRelative = relative(workspaceRoot, candidate);
  if (workspaceRelative === ".." ||
    workspaceRelative.startsWith(`..${sep}`) ||
    isAbsolute(workspaceRelative)) {
    throw new Error(
      "codehelper.runtime.mcpConfigPath must not escape the workspace root",
    );
  }
  return candidate;
}

export function enrichRuntimeError(cause: unknown, stderr: string): Error {
  const message = asError(cause).message;
  const lines = stderr.trim().split(/\r?\n/u).filter((line) => line.length > 0);
  const detail = lines.at(-1);
  if (detail === undefined || message.includes(detail)) {
    return new Error(message);
  }
  return new Error(`${message}: ${detail.slice(0, 2_048)}`);
}

async function isExecutable(path: string): Promise<boolean> {
  try {
    const mode = process.platform === "win32" || extname(path).toLowerCase() === ".cmd"
      ? constants.F_OK
      : constants.X_OK;
    await access(path, mode);
    return true;
  } catch {
    return false;
  }
}

function delay(milliseconds: number): Promise<void> {
  return new Promise((resolve) => {
    setTimeout(resolve, milliseconds);
  });
}

function isObject(value: unknown): value is Readonly<Record<string, unknown>> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function asError(value: unknown): Error {
  return value instanceof Error ? value : new Error(String(value));
}
