import assert from "node:assert/strict";
import test from "node:test";

import { BindingStore, type Memento, type RuntimeBinding } from "./store.js";
import { createWorkspaceIdentity } from "../workspace/identity.js";

void test("BindingStore scopes bindings to the exact workspace", async () => {
  const memento = new MemoryMemento();
  const store = new BindingStore(memento);
  const binding = runtimeBinding(1);
  await store.save(binding);

  assert.deepEqual(store.load("/workspace"), binding);
  assert.equal(store.load("/other"), undefined);
});

void test("BindingStore serializes monotonic cursor writes", async () => {
  const memento = new MemoryMemento();
  const store = new BindingStore(memento);
  const binding = runtimeBinding(1);
  await store.save(binding);

  await Promise.all([
    store.advanceCursor(binding, 2),
    store.advanceCursor(binding, 3),
  ]);
  assert.equal(store.load("/workspace")?.lastSeq, 3);
  const current = store.load("/workspace");
  assert.ok(current);
  await store.advanceCursor(current, 2);
  assert.equal(store.load("/workspace")?.lastSeq, 3);
});

void test("BindingStore keeps multiple sessions and persists selection", async () => {
  const store = new BindingStore(new MemoryMemento());
  const first = runtimeBinding(1);
  const second = {
    ...runtimeBinding(4),
    sessionId: "session_2",
    threadId: "thread_2",
  };
  await store.save(first);
  await store.save(second);
  assert.deepEqual(
    store.loadAll("/workspace").map((binding) => binding.sessionId),
    ["session_1", "session_2"],
  );
  assert.equal(store.load("/workspace")?.sessionId, "session_1");
  await store.select(first.rootId, second.sessionId);
  assert.equal(store.load("/workspace")?.sessionId, "session_2");
  await store.advanceCursor(first, 9);
  assert.equal(
    store.loadAll("/workspace").find(
      (binding) => binding.sessionId === first.sessionId,
    )?.lastSeq,
    9,
  );
});

class MemoryMemento implements Memento {
  readonly #values = new Map<string, unknown>();

  public get(key: string): unknown {
    return this.#values.get(key);
  }

  public async update(key: string, value: unknown): Promise<void> {
    await Promise.resolve();
    if (value === undefined) {
      this.#values.delete(key);
    } else {
      this.#values.set(key, value);
    }
  }
}

function runtimeBinding(
  lastSeq: number,
  workspaceRoot = "/workspace",
): RuntimeBinding {
  const identity = createWorkspaceIdentity(
    `file://${workspaceRoot}`,
    workspaceRoot,
  );
  return {
    version: 1,
    rootId: identity.root_id,
    workspaceURI: identity.editor_uri,
    workspaceRoot,
    sessionId: "session_1",
    threadId: "thread_1",
    lastSeq,
  };
}
