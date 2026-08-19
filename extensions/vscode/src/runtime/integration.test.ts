import assert from "node:assert/strict";
import test from "node:test";
import { createHash } from "node:crypto";
import {
  access,
  chmod,
  mkdir,
  mkdtemp,
  readFile,
  rename,
  rm,
  writeFile,
} from "node:fs/promises";
import { dirname, join } from "node:path";
import { tmpdir } from "node:os";
import { pathToFileURL } from "node:url";

import {
  launchRuntime,
  verifyBinary,
  type RuntimeProcess,
} from "./process.js";
import {
  connectSession,
  negotiateRuntime,
  type ConnectedSession,
} from "./recovery.js";
import { SessionCommands } from "./session.js";
import { ChatProjector, type ApprovalCard } from "../chat/projector.js";
import { BackgroundQuery } from "../background/query.js";
import { isUnknownEvent } from "../protocol/decode.js";
import { createWorkspaceIdentity } from "../workspace/identity.js";
import { BindingStore, type Memento } from "../state/store.js";
import type { RuntimePosture } from "../security/trust.js";

const integrationBinary = process.env["CODEHELPER_VSCODE_BINARY"];
const integrationFixture = process.env["CODEHELPER_VSCODE_FIXTURE"];
const contextFixture = process.env["CODEHELPER_VSCODE_CONTEXT_FIXTURE"];
const cleanupReportPath = process.env["CODEHELPER_Q1_CLEANUP_REPORT"];
const cleanupQualificationID = process.env["CODEHELPER_Q1_QUALIFICATION_ID"];
const cleanupTaskID = process.env["CODEHELPER_Q1_TASK_ID"];

interface OwnedResource {
  readonly kind: "process" | "temporary_directory";
  readonly owner: string;
  readonly identity: string;
  readonly pid?: number;
  cleanup_attempted: boolean;
  cleanup_succeeded: boolean;
}

const ownedResources = new Map<string, OwnedResource>();
let cleanupWrite = Promise.resolve();

void test(
  "real Runtime restarts, replays its cursor, and denies writes when untrusted",
  {
    skip: integrationBinary === undefined || integrationFixture === undefined,
    timeout: 30_000,
  },
  async () => {
    assert.ok(integrationBinary);
    assert.ok(integrationFixture);
    const root = await mkdtemp(join(tmpdir(), "codehelper-vscode-runtime-"));
    await registerTemporaryRoot("restart", root);
    const workspace = join(root, "workspace");
    const dataDirectory = join(root, "state");
    await mkdir(workspace);
    await mkdir(dataDirectory);
    const wrapper = await fixtureWrapper(root, integrationBinary, integrationFixture);

    const store = new BindingStore(new MemoryMemento());
    let first: RuntimeProcess | undefined;
    let firstSession: ConnectedSession | undefined;
    let second: RuntimeProcess | undefined;
    let secondSession: ConnectedSession | undefined;
    try {
      assert.equal((await verifyBinary(wrapper)).name, "codehelper");
      first = await start(wrapper, workspace, dataDirectory);
      await registerRuntime("restart-first", first);
      await negotiateRuntime(first.client);
      firstSession = await connectSession(
        first.client,
        store,
        workspace,
        async () => Promise.resolve(),
        (error) => {
          throw error;
        },
      );
      await first.client.request("session/prompt", {
          sessionId: firstSession.binding.sessionId,
          prompt: [{ type: "text", text: "create result" }],
        }).catch(() => undefined);
      await firstSession.settled();
      await assert.rejects(access(join(workspace, "result.txt")));
      const originalSessionID = firstSession.binding.sessionId;
      const consumedSeq = store.load(workspace)?.lastSeq ?? 0;
      assert.ok(consumedSeq > 0);
      firstSession.dispose();
      await stopOwnedRuntime("restart-first", first);
      first = undefined;

      second = await start(wrapper, workspace, dataDirectory);
      await registerRuntime("restart-second", second);
      await negotiateRuntime(second.client);
      const hydrated: number[] = [];
      secondSession = await connectSession(
        second.client,
        store,
        workspace,
        (event, replayed) => {
          assert.equal(replayed, true);
          hydrated.push(event.sequence);
          return Promise.resolve();
        },
        (error) => {
          throw error;
        },
      );
      assert.equal(secondSession.binding.sessionId, originalSessionID);
      assert.ok(secondSession.replayedEvents > 0);
      assert.equal(secondSession.replayedEvents, hydrated.length);
      assert.ok(hydrated.every((sequence) => sequence <= consumedSeq));
      assert.equal(store.load(workspace)?.lastSeq, consumedSeq);
      const background = new BackgroundQuery(second.client);
      assert.equal(
        (await background.threads()).some(
          (thread) => thread.id === secondSession?.binding.threadId,
        ),
        true,
      );
      assert.equal((await background.agents(originalSessionID)).length, 0);
      assert.equal((await background.tasks(originalSessionID)).length, 0);
      assert.equal((await background.usage(originalSessionID)).turns, 0);
    } finally {
      await cleanupIntegrationResources(
        [firstSession, secondSession],
        [
          ["restart-first", first],
          ["restart-second", second],
        ],
        "restart",
        root,
      );
    }
  },
);

void test(
  "real Runtime streams an approval round trip through extension commands",
  {
    skip: integrationBinary === undefined || integrationFixture === undefined,
    timeout: 30_000,
  },
  async () => {
    assert.ok(integrationBinary);
    assert.ok(integrationFixture);
    const root = await mkdtemp(join(tmpdir(), "codehelper-vscode-approval-"));
    await registerTemporaryRoot("approval", root);
    const workspace = join(root, "workspace");
    const dataDirectory = join(root, "state");
    await mkdir(workspace);
    await mkdir(dataDirectory);
    const rules = await approvalRules(root);
    const wrapper = await fixtureWrapper(root, integrationBinary, integrationFixture);
    // This fixture exercises write, declaration repair, quality,
    // final declaration, and final answer as five distinct samples.
    const runtime = await start(
      wrapper,
      workspace,
      dataDirectory,
      "suggest",
      5,
      rules,
    );
    await registerRuntime("approval", runtime);
    const store = new BindingStore(new MemoryMemento());
    const projector = new ChatProjector();
    const approval = deferred<ApprovalCard>();
    const qualityApproval = deferred<ApprovalCard>();
    const completed = deferred<true>();
    let session: ConnectedSession | undefined;
    try {
      await negotiateRuntime(runtime.client);
      session = await connectSession(
        runtime.client,
        store,
        workspace,
        (event) => {
          traceIntegrationEvent(event);
          projector.apply(event);
          const pending = projector.pendingApprovals()[0];
          if (pending?.tool === "file_apply") {
            approval.resolve(pending);
          } else if (pending?.tool === "quality_verify") {
            qualityApproval.resolve(pending);
          }
          if (projector.snapshot().turns.some((turn) => turn.status === "completed")) {
            completed.resolve(true);
          }
          return Promise.resolve();
        },
        (error) => {
          throw error;
        },
      );
      const commands = new SessionCommands(
        runtime.client,
        session.binding.sessionId,
        () => true,
      );
      await commands.submitPrompt("create result", [], "workspace_change");
      const request = await settleWithin(approval.promise, "file_apply approval");
      assert.ok(request.editPlan);
      assert.deepEqual(request.allowedScopes, ["once"]);
      const plannedFile = request.editPlan.files[0];
      assert.ok(plannedFile);
      assert.equal(plannedFile.path, "result.txt");
      assert.equal(plannedFile.beforeExists, false);
      assert.match(plannedFile.after, /created by engine/);
      await commands.decideApproval(
        request.turnId,
        request.requestId,
        "approve",
        "once",
        request.expiresAt,
        request.editPlan.id,
      );
      const qualityRequest = await settleWithin(
        qualityApproval.promise,
        "quality_verify approval",
      );
      assert.equal(qualityRequest.editPlan, undefined);
      assert.deepEqual(qualityRequest.allowedScopes, ["once", "session", "always"]);
      await commands.decideApproval(
        qualityRequest.turnId,
        qualityRequest.requestId,
        "approve",
        "once",
        qualityRequest.expiresAt,
      );
      await settleWithin(completed.promise, "approval turn completion");
      await settleWithin(session.settled(), "approval event projection");
      assert.equal(projector.pendingApprovals().length, 0);
      assert.match(
        await readFile(join(workspace, "result.txt"), "utf8"),
        /created by engine/,
      );
    } finally {
      await cleanupIntegrationResources(
        [session],
        [["approval", runtime]],
        "approval",
        root,
      );
    }
  },
);

void test(
  "real Runtime validates editor context while Chat keeps the display prompt",
  {
    skip: integrationBinary === undefined || contextFixture === undefined,
    timeout: 30_000,
  },
  async () => {
    assert.ok(integrationBinary);
    assert.ok(contextFixture);
    const root = await mkdtemp(join(tmpdir(), "codehelper-vscode-context-"));
    await registerTemporaryRoot("context", root);
    const workspace = join(root, "workspace");
    const dataDirectory = join(root, "state");
    await mkdir(workspace);
    await mkdir(dataDirectory);
    const content = Buffer.from("const marker = 'context-sentinel-4821';\n");
    const contextPath = join(workspace, "value.ts");
    await writeFile(contextPath, content);
    const wrapper = await fixtureWrapper(root, integrationBinary, contextFixture);
    const runtime = await start(wrapper, workspace, dataDirectory, "suggest");
    await registerRuntime("context", runtime);
    const projector = new ChatProjector();
    const completed = deferred<true>();
    let startedContext: unknown;
    let receiptContext: unknown;
    let session: ConnectedSession | undefined;
    try {
      await negotiateRuntime(runtime.client);
      session = await connectSession(
        runtime.client,
        new BindingStore(new MemoryMemento()),
        workspace,
        (event) => {
          traceIntegrationEvent(event);
          projector.apply(event);
          if (!isUnknownEvent(event) && event.kind === "turn.started") {
            startedContext = event.data.editor_context;
          } else if (!isUnknownEvent(event) && event.kind === "turn.receipt") {
            receiptContext = event.data.editor_context;
          }
          if (projector.snapshot().turns.some((turn) => turn.status === "completed")) {
            completed.resolve(true);
          }
          return Promise.resolve();
        },
        (error) => {
          throw error;
        },
      );
      const commands = new SessionCommands(
        runtime.client,
        session.binding.sessionId,
        () => true,
      );
      await commands.submitPrompt("inspect active file", [{
        kind: "file",
        uri: pathToFileURL(contextPath).toString(),
        path: "value.ts",
        document_version: 1,
        digest: createHash("sha256").update(content).digest("hex"),
        explicit: true,
      }]);
      await settleWithin(completed.promise, "editor-context turn completion");
      await settleWithin(session.settled(), "editor-context event projection");
      const turn = projector.snapshot().turns[0];
      assert.ok(turn);
      assert.equal(turn.user, "inspect active file");
      assert.equal(turn.user.includes("context-sentinel"), false);
      assert.equal(turn.output, "context received");
      assert.deepEqual(startedContext, [{
        kind: "file",
        path: "value.ts",
        digest: createHash("sha256").update(content).digest("hex"),
        original_bytes: content.byteLength,
        retained_bytes: content.byteLength,
      }]);
      assert.deepEqual(receiptContext, startedContext);
    } finally {
      await cleanupIntegrationResources(
        [session],
        [["context", runtime]],
        "context",
        root,
      );
    }
  },
);

void test(
  "real Runtime rejects an approved edit plan after workspace drift",
  {
    skip: integrationBinary === undefined || integrationFixture === undefined,
    timeout: 30_000,
  },
  async () => {
    assert.ok(integrationBinary);
    assert.ok(integrationFixture);
    const root = await mkdtemp(join(tmpdir(), "codehelper-vscode-plan-drift-"));
    await registerTemporaryRoot("plan-drift", root);
    const workspace = join(root, "workspace");
    const dataDirectory = join(root, "state");
    await mkdir(workspace);
    await mkdir(dataDirectory);
    const rules = await approvalRules(root);
    const wrapper = await fixtureWrapper(root, integrationBinary, integrationFixture);
    const runtime = await start(
      wrapper,
      workspace,
      dataDirectory,
      "suggest",
      2,
      rules,
    );
    await registerRuntime("plan-drift", runtime);
    const projector = new ChatProjector();
    const approval = deferred<ApprovalCard>();
    const terminal = deferred<true>();
    let projectionError: Error | undefined;
    let session: ConnectedSession | undefined;
    try {
      await negotiateRuntime(runtime.client);
      session = await connectSession(
        runtime.client,
        new BindingStore(new MemoryMemento()),
        workspace,
        (event) => {
          traceIntegrationEvent(event);
          if (!isUnknownEvent(event) && (
            event.kind === "turn.completed" ||
            event.kind === "turn.failed" ||
            event.kind === "turn.canceled"
          )) {
            terminal.resolve(true);
          }
          projector.apply(event);
          const pending = projector.pendingApprovals()[0];
          if (pending !== undefined) {
            approval.resolve(pending);
          }
          return Promise.resolve();
        },
        (error) => {
          projectionError = error;
        },
      );
      const commands = new SessionCommands(
        runtime.client,
        session.binding.sessionId,
        () => true,
      );
      await commands.submitPrompt("create result", [], "workspace_change");
      const request = await settleWithin(approval.promise, "plan-drift approval");
      assert.ok(request.editPlan);
      await writeFile(join(workspace, "result.txt"), "external change\n");
      await commands.decideApproval(
        request.turnId,
        request.requestId,
        "approve",
        "once",
        request.expiresAt,
        request.editPlan.id,
      );
      await settleWithin(terminal.promise, "plan-drift terminal");
      await settleWithin(session.settled(), "plan-drift event projection");
      assert.equal(projectionError, undefined);
      assert.equal(
        await readFile(join(workspace, "result.txt"), "utf8"),
        "external change\n",
      );
    } finally {
      await cleanupIntegrationResources(
        [session],
        [["plan-drift", runtime]],
        "plan-drift",
        root,
      );
    }
  },
);

async function start(
  binaryPath: string,
  workspaceRoot: string,
  dataDirectory: string,
  posture: RuntimePosture = "never",
  maxSteps = 2,
  repositoryRulesPath?: string,
): Promise<RuntimeProcess> {
  return launchRuntime({
    binaryPath,
    workspaceRoot,
    dataDirectory,
    posture,
    maxSteps,
    ...(repositoryRulesPath === undefined ? {} : { repositoryRulesPath }),
    workspaceIdentity: createWorkspaceIdentity(
      pathToFileURL(workspaceRoot).toString(),
      workspaceRoot,
    ),
    diagnostics: (text) => {
      if (process.env["CODEHELPER_TEST_RUNTIME_DIAGNOSTICS"] === "1") {
        process.stderr.write(text);
      }
    },
  });
}

async function fixtureWrapper(
  root: string,
  binary: string,
  fixture: string,
): Promise<string> {
  const wrapper = join(root, "codehelper-fixture");
  await writeFile(
    wrapper,
    `#!/bin/sh\n` +
    `if [ "$1" = "version" ]; then exec ${shellQuote(binary)} "$@"; fi\n` +
    `exec ${shellQuote(binary)} "$@" --provider-fixture ${shellQuote(fixture)}\n`,
  );
  await chmod(wrapper, 0o700);
  return wrapper;
}

async function approvalRules(root: string): Promise<string> {
  const path = join(root, "repository-rules.json");
  await writeFile(path, JSON.stringify([
    { tool: "file_apply", action: "ask" },
    { tool: "quality_verify", action: "ask" },
  ]), { mode: 0o600 });
  return path;
}

async function registerTemporaryRoot(owner: string, path: string): Promise<void> {
  if (cleanupReportPath === undefined) return;
  const identity = digestIdentity(path);
  ownedResources.set(`temporary_directory:${identity}`, {
    kind: "temporary_directory",
    owner,
    identity,
    cleanup_attempted: false,
    cleanup_succeeded: false,
  });
  await writeCleanupEvidence();
}

async function registerRuntime(
  owner: string,
  runtime: RuntimeProcess,
): Promise<void> {
  if (cleanupReportPath === undefined) return;
  assert.notEqual(runtime.pid, undefined, "tracked Runtime must have a PID");
  const pid = runtime.pid as number;
  const identity = digestIdentity(`pid:${String(pid)}:${owner}`);
  ownedResources.set(`process:${identity}`, {
    kind: "process",
    owner,
    identity,
    pid,
    cleanup_attempted: false,
    cleanup_succeeded: false,
  });
  await writeCleanupEvidence();
}

async function cleanupIntegrationResources(
  sessions: readonly (ConnectedSession | undefined)[],
  runtimes: readonly (readonly [string, RuntimeProcess | undefined])[],
  rootOwner: string,
  root: string,
): Promise<void> {
  for (const session of sessions) {
    session?.dispose();
  }
  const errors: unknown[] = [];
  for (const [owner, runtime] of runtimes) {
    if (runtime === undefined) continue;
    try {
      await stopOwnedRuntime(owner, runtime);
    } catch (error) {
      errors.push(error);
    }
  }
  try {
    await removeOwnedRoot(rootOwner, root);
  } catch (error) {
    errors.push(error);
  }
  if (errors.length !== 0) {
    throw new AggregateError(errors, "integration resource cleanup failed");
  }
}

async function stopOwnedRuntime(
  owner: string,
  runtime: RuntimeProcess,
): Promise<void> {
  const resource = ownedRuntime(owner, runtime);
  if (resource !== undefined) {
    resource.cleanup_attempted = true;
    await writeCleanupEvidence();
  }
  try {
    await runtime.stop();
    if (resource !== undefined) {
      resource.cleanup_succeeded = true;
    }
  } finally {
    await writeCleanupEvidence();
  }
}

async function removeOwnedRoot(owner: string, path: string): Promise<void> {
  const resource = ownedTemporaryRoot(owner, path);
  if (resource !== undefined) {
    resource.cleanup_attempted = true;
    await writeCleanupEvidence();
  }
  try {
    await rm(path, { recursive: true, force: true });
    if (resource !== undefined) {
      resource.cleanup_succeeded = true;
    }
  } finally {
    await writeCleanupEvidence();
  }
}

function ownedRuntime(
  owner: string,
  runtime: RuntimeProcess,
): OwnedResource | undefined {
  if (runtime.pid === undefined) return undefined;
  const identity = digestIdentity(`pid:${String(runtime.pid)}:${owner}`);
  return ownedResources.get(`process:${identity}`);
}

function ownedTemporaryRoot(
  owner: string,
  path: string,
): OwnedResource | undefined {
  const identity = digestIdentity(path);
  const resource = ownedResources.get(`temporary_directory:${identity}`);
  return resource?.owner === owner ? resource : undefined;
}

function digestIdentity(value: string): string {
  return `sha256:${createHash("sha256").update(value).digest("hex")}`;
}

async function writeCleanupEvidence(): Promise<void> {
  if (cleanupReportPath === undefined) return;
  if (cleanupQualificationID === undefined || cleanupTaskID === undefined) {
    throw new Error("Q1 cleanup evidence identity is incomplete");
  }
  cleanupWrite = cleanupWrite.then(async () => {
    const resources = [...ownedResources.values()].sort((left, right) =>
      `${left.kind}:${left.identity}`.localeCompare(`${right.kind}:${right.identity}`)
    );
    const report = {
      schema_version: 1,
      qualification_id: cleanupQualificationID,
      task_id: cleanupTaskID,
      resources,
      outstanding: resources.filter((resource) =>
        !resource.cleanup_attempted || !resource.cleanup_succeeded
      ).length,
    };
    const temporary = `${cleanupReportPath}.tmp-${String(process.pid)}`;
    await mkdir(dirname(cleanupReportPath), { recursive: true, mode: 0o700 });
    await writeFile(temporary, `${JSON.stringify(report, null, 2)}\n`, {
      mode: 0o600,
    });
    await rename(temporary, cleanupReportPath);
  });
  await cleanupWrite;
}

class MemoryMemento implements Memento {
  readonly #values = new Map<string, unknown>();

  public get(key: string): unknown {
    return this.#values.get(key);
  }

  public update(key: string, value: unknown): Promise<void> {
    if (value === undefined) {
      this.#values.delete(key);
    } else {
      this.#values.set(key, value);
    }
    return Promise.resolve();
  }
}

function shellQuote(value: string): string {
  return `'${value.replaceAll("'", "'\\''")}'`;
}

function traceIntegrationEvent(event: unknown): void {
  if (process.env["CODEHELPER_TEST_RUNTIME_DIAGNOSTICS"] === "1") {
    process.stderr.write(`[runtime-event] ${JSON.stringify(event)}\n`);
  }
}

function deferred<T>(): {
  readonly promise: Promise<T>;
  readonly resolve: (value: T) => void;
} {
  let resolvePromise: ((value: T) => void) | undefined;
  const promise = new Promise<T>((resolve) => {
    resolvePromise = resolve;
  });
  if (resolvePromise === undefined) {
    throw new Error("failed to initialize deferred promise");
  }
  return { promise, resolve: resolvePromise };
}

async function settleWithin<T>(
  promise: Promise<T>,
  label: string,
  milliseconds = 10_000,
): Promise<T> {
  let timer: NodeJS.Timeout | undefined;
  const timeout = new Promise<never>((_resolve, reject) => {
    timer = setTimeout(() => {
      reject(new Error(`${label} did not settle within ${String(milliseconds)}ms`));
    }, milliseconds);
  });
  try {
    return await Promise.race([promise, timeout]);
  } finally {
    if (timer !== undefined) {
      clearTimeout(timer);
    }
  }
}
