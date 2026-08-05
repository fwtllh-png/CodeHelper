import assert from "node:assert/strict";
import test from "node:test";

import { RuntimeSupervisor, type ManagedRuntime } from "./supervisor.js";

void test("RuntimeSupervisor restarts a crashed Runtime and stops cleanly", async () => {
  const runtimes: FakeRuntime[] = [];
  const supervisor = new RuntimeSupervisor(() => {
    const runtime = new FakeRuntime();
    runtimes.push(runtime);
    return Promise.resolve(runtime);
  }, { restartDelaysMS: [1, 1] });

  await supervisor.start();
  assert.equal(supervisor.snapshot.state, "ready");
  runtimes[0]?.crash();
  await waitFor(() => runtimes.length === 2);
  assert.equal(supervisor.snapshot.state, "ready");
  assert.equal(supervisor.snapshot.restartAttempt, 1);

  await supervisor.stop();
  assert.equal(supervisor.snapshot.state, "stopped");
  assert.equal(runtimes[1]?.stopped, true);
  await delay(5);
  assert.equal(runtimes.length, 2);
});

void test("RuntimeSupervisor bounds failed crash recovery", async () => {
  let launches = 0;
  const first = new FakeRuntime();
  const supervisor = new RuntimeSupervisor<ManagedRuntime>(() => {
    launches++;
    if (launches === 1) {
      return Promise.resolve(first);
    }
    return Promise.reject(new Error("launch failed"));
  }, { restartDelaysMS: [1, 1] });

  await supervisor.start();
  first.crash();
  await waitFor(() => supervisor.snapshot.state === "failed" && launches === 3);
  assert.equal(supervisor.snapshot.restartAttempt, 2);
  assert.match(supervisor.snapshot.error ?? "", /restart budget/);
  await supervisor.stop();
});

class FakeRuntime implements ManagedRuntime {
  public readonly exited: Promise<void>;
  public stopped = false;
  readonly #resolve: () => void;

  public constructor() {
    let resolveExit: (() => void) | undefined;
    this.exited = new Promise((resolve) => {
      resolveExit = resolve;
    });
    if (resolveExit === undefined) {
      throw new Error("failed to initialize fake Runtime");
    }
    this.#resolve = resolveExit;
  }

  public crash(): void {
    this.#resolve();
  }

  public stop(): Promise<void> {
    this.stopped = true;
    this.#resolve();
    return Promise.resolve();
  }
}

async function waitFor(predicate: () => boolean): Promise<void> {
  for (let attempt = 0; attempt < 100; attempt++) {
    if (predicate()) {
      return;
    }
    await delay(2);
  }
  throw new Error("condition was not reached");
}

function delay(milliseconds: number): Promise<void> {
  return new Promise((resolve) => {
    setTimeout(resolve, milliseconds);
  });
}
