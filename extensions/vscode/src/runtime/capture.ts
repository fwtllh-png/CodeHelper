import { randomUUID } from "node:crypto";
import {
  access,
  mkdir,
  open,
  unlink,
  writeFile,
  type FileHandle,
} from "node:fs/promises";
import { join } from "node:path";

const captureMarkerName = "runtime-capture.enabled";

export interface RuntimeCaptureOptions {
  readonly now?: () => Date;
  readonly onError?: (error: Error) => void;
}

interface CaptureEnvelope {
  readonly version: 1;
  readonly capture_id: string;
  readonly capture_sequence: number;
  readonly captured_at: string;
  readonly kind: string;
  readonly data: unknown;
}

export class RuntimeCapture {
  readonly #handle: FileHandle;
  readonly #captureId: string;
  readonly #now: () => Date;
  readonly #onError: (error: Error) => void;
  #sequence = 0;
  #pending: Promise<void> = Promise.resolve();
  #closed = false;
  #failed = false;

  private constructor(
    public readonly path: string,
    handle: FileHandle,
    captureId: string,
    options: RuntimeCaptureOptions,
  ) {
    this.#handle = handle;
    this.#captureId = captureId;
    this.#now = options.now ?? (() => new Date());
    this.#onError = options.onError ?? (() => undefined);
  }

  public static async open(
    dataDirectory: string,
    metadata: unknown,
    options: RuntimeCaptureOptions = {},
  ): Promise<RuntimeCapture> {
    const directory = join(dataDirectory, "runtime-captures");
    await mkdir(directory, { recursive: true, mode: 0o700 });
    const captureId = randomUUID();
    const timestamp = (options.now?.() ?? new Date())
      .toISOString()
      .replaceAll(/[:.]/gu, "-");
    const path = join(directory, `runtime-${timestamp}-${captureId}.jsonl`);
    const handle = await open(path, "wx", 0o600);
    const capture = new RuntimeCapture(
      path,
      handle,
      captureId,
      options,
    );
    capture.record("capture.started", metadata);
    return capture;
  }

  public record(kind: string, data: unknown): void {
    if (this.#closed || this.#failed) return;
    let line: string;
    try {
      const envelope: CaptureEnvelope = {
        version: 1,
        capture_id: this.#captureId,
        capture_sequence: ++this.#sequence,
        captured_at: this.#now().toISOString(),
        kind,
        data,
      };
      line = `${JSON.stringify(envelope)}\n`;
    } catch (error) {
      this.#fail(error);
      return;
    }
    this.#pending = this.#pending
      .then(async () => this.#handle.appendFile(line, "utf8"))
      .catch((error: unknown) => {
        this.#fail(error);
      });
  }

  public async flush(): Promise<void> {
    await this.#pending;
    if (!this.#failed) await this.#handle.sync();
  }

  public async close(reason: string): Promise<void> {
    if (this.#closed) return;
    this.record("capture.stopped", { reason });
    this.#closed = true;
    await this.#pending;
    await this.#handle.close();
  }

  #fail(value: unknown): void {
    if (this.#failed) return;
    this.#failed = true;
    const error = value instanceof Error ? value : new Error(String(value));
    this.#onError(error);
  }
}

export async function requestRuntimeCapture(
  dataDirectory: string,
): Promise<void> {
  await mkdir(dataDirectory, { recursive: true, mode: 0o700 });
  await writeFile(
    join(dataDirectory, captureMarkerName),
    "CodeHelper VS Code Runtime Capture\n",
    { encoding: "utf8", mode: 0o600 },
  );
}

export async function clearRuntimeCaptureRequest(
  dataDirectory: string,
): Promise<void> {
  try {
    await unlink(join(dataDirectory, captureMarkerName));
  } catch (error) {
    if (!isNodeError(error) || error.code !== "ENOENT") throw error;
  }
}

export async function runtimeCaptureRequested(
  dataDirectory: string,
): Promise<boolean> {
  try {
    await access(join(dataDirectory, captureMarkerName));
    return true;
  } catch (error) {
    if (isNodeError(error) && error.code === "ENOENT") return false;
    throw error;
  }
}

function isNodeError(value: unknown): value is NodeJS.ErrnoException {
  return value instanceof Error;
}
