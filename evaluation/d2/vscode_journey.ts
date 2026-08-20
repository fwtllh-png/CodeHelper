import { copyFile, chmod, writeFile } from "node:fs/promises";
import { dirname, join } from "node:path";
import { pathToFileURL } from "node:url";

import {
  launchRuntime,
  verifyBinary,
  type RuntimeProcess,
} from "../../extensions/vscode/src/runtime/process.js";
import {
  connectSession,
  negotiateRuntime,
  type ConnectedSession,
} from "../../extensions/vscode/src/runtime/recovery.js";
import { SessionCommands } from "../../extensions/vscode/src/runtime/session.js";
import { SessionArtifactCommands } from "../../extensions/vscode/src/runtime/artifacts.js";
import { BindingStore, type Memento } from "../../extensions/vscode/src/state/store.js";
import { createWorkspaceIdentity } from "../../extensions/vscode/src/workspace/identity.js";
import type { DecodedEvent } from "../../extensions/vscode/src/protocol/decode.js";

interface JourneyInput {
  readonly id: string;
  readonly runtime: string;
  readonly fixture: string;
  readonly workspace: string;
  readonly stateDir: string;
  readonly receipt: string;
  readonly sessionState: string;
  readonly lifecycle: string;
}

class MemoryMemento implements Memento {
  readonly #values = new Map<string, unknown>();

  public get(key: string): unknown {
    return this.#values.get(key);
  }

  public update(key: string, value: unknown): Promise<void> {
    this.#values.set(key, value);
    return Promise.resolve();
  }
}

const input = readInput();
const steps: string[] = [];
const events: DecodedEvent[] = [];
const store = new BindingStore(new MemoryMemento());
const identity = createWorkspaceIdentity(
  pathToFileURL(input.workspace).toString(),
  input.workspace,
);
const wrapper = join(dirname(input.stateDir), "codehelper-vscode-d2");
let runtime: RuntimeProcess | undefined;
let session: ConnectedSession | undefined;

async function main(): Promise<void> {
  try {
    await writeWrapper(wrapper, input.runtime, input.fixture);
    ({ runtime, session } = await startRuntime(wrapper));
    steps.push("start_runtime");

    const commands = () => new SessionCommands(
      runtime?.client ?? fail("Runtime is unavailable"),
      session?.binding.sessionId ?? fail("Session is unavailable"),
      () => false,
      identity,
    );
    const artifacts = () => new SessionArtifactCommands(
      runtime?.client ?? fail("Runtime is unavailable"),
    );

    const prompt = input.sessionState === "canceled_effect"
      ? "wait for interrupt"
      : "say hello";
    const receipt = await commands().submitPrompt(prompt, []);
    steps.push("submit_prompt");
    if (input.sessionState === "canceled_effect") {
      steps.push("start_effect");
      await commands().cancelTurn(receipt.turnId);
      steps.push("cancel_turn");
      await waitForTerminal(receipt.turnId);
    } else {
      await waitForTerminal(receipt.turnId);
    }

    if (input.sessionState === "checkpoint_resume") {
      await waitFor(() => countEvent("checkpoint.created") > 0);
      const list = await artifacts().checkpoints(session.binding.sessionId);
      const checkpoint = list.checkpoints[0] ??
        fail("VS Code checkpoint list is empty");
      steps.push("list_checkpoint");
      await artifacts().restore(session.binding.sessionId, checkpoint.id);
      steps.push("restore_checkpoint");
      const resumed = await commands().submitPrompt("say hello", []);
      await waitForTerminal(resumed.turnId);
      steps.push("resume_session");
    } else if (input.sessionState === "long_compacted") {
      const extended = await commands().submitPrompt("say hello", []);
      await waitForTerminal(extended.turnId);
      steps.push("extend_session");
      await waitFor(() => countEvent("checkpoint.created") > 0);
      const before = countEvent("thread.compacted");
      await runtime.client.request("session/submit", {
        sessionId: session.binding.sessionId,
        operation: {
          kind: "thread.compact",
          payload: {
            thread_id: session.binding.threadId,
            turn_id: extended.turnId,
          },
        },
        idempotencyKey: `d2-compact-${input.id}`,
      });
      await waitFor(() => countEvent("thread.compacted") > before);
      steps.push("observe_compaction");
    }

    if (input.lifecycle === "crash_recovery") {
      session.dispose();
      session = undefined;
      const pid = runtime.pid ?? fail("VS Code Runtime PID is unavailable");
      process.kill(pid, "SIGKILL");
      await runtime.exited;
      runtime = undefined;
      steps.push("crash_runtime");
      ({ runtime, session } = await startRuntime(wrapper));
      steps.push("restart_runtime", "reconnect_session");
    } else if (input.lifecycle === "version_upgrade") {
      await stopCurrent();
      steps.push("stop_runtime");
      const staged = join(dirname(input.stateDir), "runtime-upgrade");
      await stageRuntime(input.runtime, staged);
      await writeWrapper(wrapper, staged, input.fixture);
      steps.push("upgrade_runtime");
      ({ runtime, session } = await startRuntime(wrapper));
      steps.push("restart_runtime");
    } else if (input.lifecycle === "rollback_reconnect") {
      await stopCurrent();
      steps.push("stop_runtime");
      const staged = join(dirname(input.stateDir), "runtime-rollback");
      await stageRuntime(input.runtime, staged);
      await writeWrapper(wrapper, staged, input.fixture);
      steps.push("rollback_runtime");
      ({ runtime, session } = await startRuntime(wrapper));
      steps.push("reconnect_session");
    }

    steps.push("observe_terminal");
    await writeFile(input.receipt, `${JSON.stringify({
      schema_version: 1,
      case_id: input.id,
      steps,
      event_kinds: events.map((event) => event.kind),
    })}\n`, { mode: 0o600 });
  } finally {
    await stopCurrent();
  }
}

void main().catch((error: unknown) => {
  const message = error instanceof Error ? error.message : String(error);
  process.stderr.write(
    `${message}; steps=${steps.join(",")}; ` +
      `events=${events.map((event) => event.kind).join(",")}\n`,
  );
  process.exitCode = 1;
});

async function startRuntime(
  binaryPath: string,
): Promise<{ runtime: RuntimeProcess; session: ConnectedSession }> {
  const started = await launchRuntime({
    binaryPath,
    workspaceRoot: input.workspace,
    dataDirectory: input.stateDir,
    posture: "never",
    maxSteps: 2,
    workspaceIdentity: identity,
    diagnostics: (text) => {
      if (process.env["CODEHELPER_D2_DIAGNOSTICS"] === "1") {
        process.stderr.write(text);
      }
    },
  });
  await negotiateRuntime(started.client, identity);
  const connected = await connectSession(
    started.client,
    store,
    identity,
    (event) => {
      events.push(event);
    },
    (error) => {
      throw error;
    },
    { title: input.id },
  );
  return { runtime: started, session: connected };
}

async function stopCurrent(): Promise<void> {
  session?.dispose();
  session = undefined;
  const current = runtime;
  runtime = undefined;
  if (current !== undefined) {
    await current.stop();
  }
}

async function waitForTerminal(turnID: string): Promise<void> {
  await waitFor(() => events.some((event) =>
    event.turn_id === turnID &&
    (event.kind === "turn.completed" ||
      event.kind === "turn.failed" ||
      event.kind === "turn.canceled")));
  await session?.settled();
}

async function waitFor(predicate: () => boolean): Promise<void> {
  const deadline = Date.now() + 20_000;
  while (!predicate()) {
    if (Date.now() >= deadline) {
      throw new Error("VS Code Journey evidence timed out");
    }
    await new Promise((resolve) => setTimeout(resolve, 10));
  }
}

function countEvent(kind: string): number {
  return events.filter((event) => event.kind === kind).length;
}

async function stageRuntime(source: string, destination: string): Promise<void> {
  const sourceVersion = await verifyBinary(source);
  await copyFile(source, destination);
  await chmod(destination, 0o700);
  const stagedVersion = await verifyBinary(destination);
  if (JSON.stringify(sourceVersion) !== JSON.stringify(stagedVersion)) {
    throw new Error("staged VS Code Runtime identity drifted");
  }
}

async function writeWrapper(
  path: string,
  binary: string,
  fixture: string,
): Promise<void> {
  await writeFile(
    path,
    "#!/bin/sh\n" +
      `if [ "$1" = "version" ]; then exec ${quote(binary)} "$@"; fi\n` +
      `exec ${quote(binary)} "$@" --provider-fixture ${quote(fixture)}\n`,
    { mode: 0o700 },
  );
}

function quote(value: string): string {
  return `'${value.replaceAll("'", "'\\''")}'`;
}

function readInput(): JourneyInput {
  const raw = process.env["CODEHELPER_D2_VSCODE_INPUT"];
  if (raw === undefined) {
    throw new Error("CODEHELPER_D2_VSCODE_INPUT is required");
  }
  const value = JSON.parse(raw) as Partial<JourneyInput>;
  for (const key of [
    "id",
    "runtime",
    "fixture",
    "workspace",
    "stateDir",
    "receipt",
    "sessionState",
    "lifecycle",
  ] as const) {
    if (typeof value[key] !== "string" || value[key]?.length === 0) {
      throw new Error(`VS Code Journey input ${key} is invalid`);
    }
  }
  return value as JourneyInput;
}

function fail(message: string): never {
  throw new Error(message);
}
