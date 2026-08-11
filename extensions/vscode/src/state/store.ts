import type { WorkspaceIdentity } from "../workspace/identity.js";

const bindingKey = "codehelper.runtimeBindings.v1";
export const maxChatSessions = 32;

export interface Memento {
  get(key: string): unknown;
  update(key: string, value: unknown): Thenable<void>;
}

export interface RuntimeBinding {
  readonly version: 1;
  readonly rootId: string;
  readonly workspaceURI: string;
  readonly workspaceRoot: string;
  readonly sessionId: string;
  readonly threadId: string;
  readonly lastSeq: number;
}

interface StoredRootBindings {
  readonly workspaceURI: string;
  readonly workspaceRoot: string;
  readonly selectedSessionId: string;
  readonly bindings: Readonly<Record<string, RuntimeBinding>>;
}

interface StoredBindings {
  readonly version: 1;
  readonly roots: Readonly<Record<string, StoredRootBindings>>;
}

export class BindingStore {
  readonly #memento: Memento;
  #writeChain: Promise<void> = Promise.resolve();

  public constructor(memento: Memento) {
    this.#memento = memento;
  }

  public load(identity: WorkspaceIdentity | string): RuntimeBinding | undefined {
    const bindings = this.loadAll(identity);
    const root = this.#root(identity);
    if (root === undefined) return bindings[0];
    return bindings.find(
      (binding) => binding.sessionId === root.selectedSessionId,
    ) ?? bindings[0];
  }

  public loadAll(identity: WorkspaceIdentity | string): readonly RuntimeBinding[] {
    const root = this.#root(identity);
    return root === undefined ? [] : Object.values(root.bindings);
  }

  public save(binding: RuntimeBinding): Promise<void> {
    if (!isBinding(binding)) {
      return Promise.reject(new TypeError("runtime binding is invalid"));
    }
    return this.#enqueue((stored) => {
      const root = stored.roots[binding.rootId];
      const bindings = {
        ...(root?.bindings ?? {}),
        [binding.sessionId]: binding,
      };
      if (Object.keys(bindings).length > maxChatSessions) {
        throw new Error(`at most ${String(maxChatSessions)} Chat sessions are supported`);
      }
      return {
        version: 1,
        roots: {
          ...stored.roots,
          [binding.rootId]: {
            workspaceURI: binding.workspaceURI,
            workspaceRoot: binding.workspaceRoot,
            selectedSessionId: root?.selectedSessionId ?? binding.sessionId,
            bindings,
          },
        },
      };
    });
  }

  public select(rootId: string, sessionId: string): Promise<void> {
    return this.#enqueue((stored) => {
      const root = stored.roots[rootId];
      if (root?.bindings[sessionId] === undefined) {
        throw new Error("Chat session binding is unavailable");
      }
      return {
        version: 1,
        roots: {
          ...stored.roots,
          [rootId]: { ...root, selectedSessionId: sessionId },
        },
      };
    });
  }

  public advanceCursor(binding: RuntimeBinding, sequence: number): Promise<void> {
    if (!Number.isSafeInteger(sequence) || sequence < 0) {
      return Promise.reject(new TypeError(
        "runtime cursor must be a non-negative integer",
      ));
    }
    return this.#enqueue((stored) => {
      const root = stored.roots[binding.rootId];
      if (root === undefined) {
        throw new Error("runtime workspace binding changed before cursor update");
      }
      const current = root.bindings[binding.sessionId];
      if (current === undefined ||
        current.threadId !== binding.threadId) {
        throw new Error("runtime binding changed before cursor update");
      }
      if (sequence <= current.lastSeq) {
        return stored;
      }
      return {
        version: 1,
        roots: {
          ...stored.roots,
          [binding.rootId]: {
            ...root,
            bindings: {
              ...root.bindings,
              [binding.sessionId]: { ...current, lastSeq: sequence },
            },
          },
        },
      };
    });
  }

  public clear(rootId: string, sessionId?: string): Promise<void> {
    return this.#enqueue((stored) => {
      if (sessionId === undefined) {
        const roots = Object.fromEntries(
          Object.entries(stored.roots).filter(([key]) => key !== rootId),
        );
        return { version: 1, roots };
      }
      const root = stored.roots[rootId];
      if (root === undefined) return stored;
      const bindings = Object.fromEntries(
        Object.entries(root.bindings).filter(([key]) => key !== sessionId),
      );
      const remaining = Object.values(bindings);
      if (remaining.length === 0) {
        const roots = Object.fromEntries(
          Object.entries(stored.roots).filter(([key]) => key !== rootId),
        );
        return { version: 1, roots };
      }
      const selectedSessionId = bindings[root.selectedSessionId] === undefined
        ? remaining[0]?.sessionId ?? ""
        : root.selectedSessionId;
      return {
        version: 1,
        roots: {
          ...stored.roots,
          [rootId]: { ...root, bindings, selectedSessionId },
        },
      };
    });
  }

  #root(identity: WorkspaceIdentity | string): StoredRootBindings | undefined {
    const stored = this.#decode();
    if (typeof identity === "string") {
      return Object.values(stored.roots).find(
        (candidate) => candidate.workspaceRoot === identity,
      );
    }
    const root = stored.roots[identity.root_id];
    return root?.workspaceURI === identity.editor_uri &&
      root.workspaceRoot === identity.runtime_path
      ? root
      : undefined;
  }

  #decode(): StoredBindings {
    return decodeBindings(this.#memento.get(bindingKey));
  }

  #enqueue(
    update: (stored: StoredBindings) => StoredBindings,
  ): Promise<void> {
    const write = async (): Promise<void> => {
      const current = this.#decode();
      await this.#memento.update(bindingKey, update(current));
    };
    this.#writeChain = this.#writeChain.then(write, write);
    return this.#writeChain;
  }
}

function decodeBindings(value: unknown): StoredBindings {
  if (!isObject(value) || value["version"] !== 1 ||
    !isObject(value["roots"])) {
    return { version: 1, roots: {} };
  }
  const roots: Record<string, StoredRootBindings> = {};
  for (const [rootId, candidate] of Object.entries(value["roots"])) {
    if (!/^[0-9a-f]{64}$/u.test(rootId) || !isObject(candidate) ||
      typeof candidate["workspaceURI"] !== "string" ||
      typeof candidate["workspaceRoot"] !== "string" ||
      typeof candidate["selectedSessionId"] !== "string" ||
      !isObject(candidate["bindings"])) continue;
    const bindings: Record<string, RuntimeBinding> = {};
    for (const [sessionId, binding] of Object.entries(candidate["bindings"])) {
      if (isBinding(binding) && binding.rootId === rootId &&
        binding.sessionId === sessionId) {
        bindings[sessionId] = {
          version: 1,
          rootId: binding.rootId,
          workspaceURI: binding.workspaceURI,
          workspaceRoot: binding.workspaceRoot,
          sessionId: binding.sessionId,
          threadId: binding.threadId,
          lastSeq: binding.lastSeq,
        };
      }
    }
    if (Object.keys(bindings).length > 0 &&
      bindings[candidate["selectedSessionId"]] !== undefined) {
      roots[rootId] = {
        workspaceURI: candidate["workspaceURI"],
        workspaceRoot: candidate["workspaceRoot"],
        selectedSessionId: candidate["selectedSessionId"],
        bindings,
      };
    }
  }
  return { version: 1, roots };
}

function isBinding(value: unknown): value is RuntimeBinding {
  return isObject(value) &&
    value["version"] === 1 &&
    typeof value["rootId"] === "string" &&
    /^[0-9a-f]{64}$/u.test(value["rootId"]) &&
    typeof value["workspaceURI"] === "string" &&
    value["workspaceURI"].length > 0 &&
    typeof value["workspaceRoot"] === "string" &&
    value["workspaceRoot"].length > 0 &&
    typeof value["sessionId"] === "string" &&
    value["sessionId"].length > 0 &&
    typeof value["threadId"] === "string" &&
    value["threadId"].length > 0 &&
    typeof value["lastSeq"] === "number" &&
    Number.isSafeInteger(value["lastSeq"]) &&
    value["lastSeq"] >= 0;
}

function isObject(value: unknown): value is Readonly<Record<string, unknown>> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}
