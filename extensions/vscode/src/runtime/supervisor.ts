export type SupervisorState =
  | "stopped"
  | "starting"
  | "ready"
  | "recovering"
  | "failed";

export interface ManagedRuntime {
  readonly exited: Promise<unknown>;
  stop(): Promise<void>;
}

export interface SupervisorSnapshot {
  readonly state: SupervisorState;
  readonly restartAttempt: number;
  readonly error?: string;
}

export interface RuntimeSupervisorOptions {
  readonly restartDelaysMS?: readonly number[];
  readonly onStateChange?: (snapshot: SupervisorSnapshot) => void;
}

export class RuntimeSupervisor<T extends ManagedRuntime> {
  readonly #launch: () => Promise<T>;
  readonly #restartDelaysMS: readonly number[];
  readonly #onStateChange: (snapshot: SupervisorSnapshot) => void;
  #runtime: T | undefined;
  #generation = 0;
  #restartAttempt = 0;
  #restartTimer: NodeJS.Timeout | undefined;
  #transition: Promise<void> = Promise.resolve();
  #snapshot: SupervisorSnapshot = { state: "stopped", restartAttempt: 0 };

  public constructor(
    launch: () => Promise<T>,
    options: RuntimeSupervisorOptions = {},
  ) {
    this.#launch = launch;
    this.#restartDelaysMS = options.restartDelaysMS ?? [250, 500, 1_000];
    this.#onStateChange = options.onStateChange ?? (() => undefined);
  }

  public get snapshot(): SupervisorSnapshot {
    return this.#snapshot;
  }

  public get runtime(): T | undefined {
    return this.#runtime;
  }

  public start(): Promise<void> {
    return this.#enqueue(async () => {
      if (this.#runtime !== undefined ||
        this.#snapshot.state === "starting" ||
        this.#snapshot.state === "ready") {
        return;
      }
      this.#generation++;
      this.#restartAttempt = 0;
      await this.#launchCurrent(this.#generation, false);
    });
  }

  public restart(): Promise<void> {
    return this.#enqueue(async () => {
      await this.#stopCurrent();
      this.#generation++;
      this.#restartAttempt = 0;
      await this.#launchCurrent(this.#generation, false);
    });
  }

  public stop(): Promise<void> {
    return this.#enqueue(async () => {
      this.#generation++;
      if (this.#restartTimer !== undefined) {
        clearTimeout(this.#restartTimer);
        this.#restartTimer = undefined;
      }
      await this.#stopCurrent();
      this.#setSnapshot({ state: "stopped", restartAttempt: 0 });
    });
  }

  async #launchCurrent(generation: number, recovering: boolean): Promise<void> {
    this.#setSnapshot({
      state: recovering ? "recovering" : "starting",
      restartAttempt: this.#restartAttempt,
    });
    try {
      const runtime = await this.#launch();
      if (generation !== this.#generation) {
        await runtime.stop();
        return;
      }
      this.#runtime = runtime;
      this.#setSnapshot({ state: "ready", restartAttempt: this.#restartAttempt });
      void runtime.exited.then(
        () => {
          this.#runtimeExited(runtime, generation);
        },
        () => {
          this.#runtimeExited(runtime, generation);
        },
      );
    } catch (error) {
      if (generation !== this.#generation) {
        return;
      }
      this.#setSnapshot({
        state: "failed",
        restartAttempt: this.#restartAttempt,
        error: asError(error).message,
      });
      throw error;
    }
  }

  #runtimeExited(runtime: T, generation: number): void {
    if (generation !== this.#generation || this.#runtime !== runtime) {
      return;
    }
    this.#runtime = undefined;
    this.#scheduleRestart(generation);
  }

  #scheduleRestart(generation: number): void {
    if (generation !== this.#generation) {
      return;
    }
    if (this.#restartAttempt >= this.#restartDelaysMS.length) {
      this.#setSnapshot({
        state: "failed",
        restartAttempt: this.#restartAttempt,
        error: "CodeHelper Runtime exhausted its automatic restart budget",
      });
      return;
    }
    const delay = this.#restartDelaysMS[this.#restartAttempt];
    if (delay === undefined) {
      return;
    }
    this.#restartAttempt++;
    this.#setSnapshot({ state: "recovering", restartAttempt: this.#restartAttempt });
    this.#restartTimer = setTimeout(() => {
      this.#restartTimer = undefined;
      void this.#enqueue(async () => {
        if (generation !== this.#generation || this.#runtime !== undefined) {
          return;
        }
        try {
          await this.#launchCurrent(generation, true);
        } catch {
          this.#scheduleRestart(generation);
        }
      });
    }, delay);
  }

  #enqueue(task: () => Promise<void>): Promise<void> {
    const operation = this.#transition.then(task, task);
    this.#transition = operation.catch(() => undefined);
    return operation;
  }

  async #stopCurrent(): Promise<void> {
    if (this.#restartTimer !== undefined) {
      clearTimeout(this.#restartTimer);
      this.#restartTimer = undefined;
    }
    const runtime = this.#runtime;
    this.#runtime = undefined;
    if (runtime !== undefined) {
      await runtime.stop();
    }
  }

  #setSnapshot(snapshot: SupervisorSnapshot): void {
    this.#snapshot = snapshot;
    this.#onStateChange(snapshot);
  }
}

function asError(value: unknown): Error {
  return value instanceof Error ? value : new Error(String(value));
}
