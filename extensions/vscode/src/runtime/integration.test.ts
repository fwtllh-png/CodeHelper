import assert from "node:assert/strict";
import test from "node:test";
import { createHash } from "node:crypto";
import {
  access,
  chmod,
  mkdir,
  mkdtemp,
  readFile,
  rm,
  writeFile,
} from "node:fs/promises";
import { join } from "node:path";
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
      await first.stop();
      first = undefined;

      second = await start(wrapper, workspace, dataDirectory);
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
      assert.equal((await background.agents()).length, 0);
      assert.equal((await background.tasks()).length, 0);
      assert.equal((await background.usage()).turns, 0);
    } finally {
      firstSession?.dispose();
      secondSession?.dispose();
      await first?.stop();
      await second?.stop();
      await rm(root, { recursive: true, force: true });
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
    const workspace = join(root, "workspace");
    const dataDirectory = join(root, "state");
    await mkdir(workspace);
    await mkdir(dataDirectory);
    const wrapper = await fixtureWrapper(root, integrationBinary, integrationFixture);
    const runtime = await start(wrapper, workspace, dataDirectory, "suggest");
    const store = new BindingStore(new MemoryMemento());
    const projector = new ChatProjector();
    const approval = deferred<ApprovalCard>();
    const completed = deferred<true>();
    let session: ConnectedSession | undefined;
    try {
      await negotiateRuntime(runtime.client);
      session = await connectSession(
        runtime.client,
        store,
        workspace,
        (event) => {
          projector.apply(event);
          const pending = projector.pendingApprovals()[0];
          if (pending !== undefined) {
            approval.resolve(pending);
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
      await commands.submitPrompt("create result", []);
      const request = await approval.promise;
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
      await completed.promise;
      await session.settled();
      assert.equal(projector.pendingApprovals().length, 0);
      assert.match(
        await readFile(join(workspace, "result.txt"), "utf8"),
        /created by engine/,
      );
    } finally {
      session?.dispose();
      await runtime.stop();
      await rm(root, { recursive: true, force: true });
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
    const workspace = join(root, "workspace");
    const dataDirectory = join(root, "state");
    await mkdir(workspace);
    await mkdir(dataDirectory);
    const content = Buffer.from("const marker = 'context-sentinel-4821';\n");
    const contextPath = join(workspace, "value.ts");
    await writeFile(contextPath, content);
    const wrapper = await fixtureWrapper(root, integrationBinary, contextFixture);
    const runtime = await start(wrapper, workspace, dataDirectory, "suggest");
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
      await completed.promise;
      await session.settled();
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
      session?.dispose();
      await runtime.stop();
      await rm(root, { recursive: true, force: true });
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
    const workspace = join(root, "workspace");
    const dataDirectory = join(root, "state");
    await mkdir(workspace);
    await mkdir(dataDirectory);
    const wrapper = await fixtureWrapper(root, integrationBinary, integrationFixture);
    const runtime = await start(wrapper, workspace, dataDirectory, "suggest");
    const projector = new ChatProjector();
    const approval = deferred<ApprovalCard>();
    const terminal = deferred<true>();
    let session: ConnectedSession | undefined;
    try {
      await negotiateRuntime(runtime.client);
      session = await connectSession(
        runtime.client,
        new BindingStore(new MemoryMemento()),
        workspace,
        (event) => {
          projector.apply(event);
          const pending = projector.pendingApprovals()[0];
          if (pending !== undefined) {
            approval.resolve(pending);
          }
          if (projector.snapshot().turns.some(
            (turn) => turn.status === "completed" || turn.status === "failed",
          )) {
            terminal.resolve(true);
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
      await commands.submitPrompt("create result", []);
      const request = await approval.promise;
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
      await terminal.promise;
      await session.settled();
      assert.equal(
        await readFile(join(workspace, "result.txt"), "utf8"),
        "external change\n",
      );
    } finally {
      session?.dispose();
      await runtime.stop();
      await rm(root, { recursive: true, force: true });
    }
  },
);

async function start(
  binaryPath: string,
  workspaceRoot: string,
  dataDirectory: string,
  posture: RuntimePosture = "never",
): Promise<RuntimeProcess> {
  return launchRuntime({
    binaryPath,
    workspaceRoot,
    dataDirectory,
    posture,
    maxSteps: 2,
    workspaceIdentity: createWorkspaceIdentity(
      pathToFileURL(workspaceRoot).toString(),
      workspaceRoot,
    ),
    diagnostics: () => undefined,
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
