import type { Readable, Writable } from "node:stream";

const defaultMaxFrameBytes = 4 << 20;
const defaultRequestTimeoutMS = 30_000;

type JsonObject = Readonly<Record<string, unknown>>;

interface PendingRequest {
  readonly resolve: (value: unknown) => void;
  readonly reject: (error: Error) => void;
  readonly timer: NodeJS.Timeout;
}

export interface AcpClientOptions {
  readonly maxFrameBytes?: number;
  readonly requestTimeoutMS?: number;
}

export interface RpcNotification {
  readonly method: string;
  readonly params: unknown;
}

export class RpcError extends Error {
  public constructor(
    public readonly code: number,
    message: string,
    public readonly data?: unknown,
  ) {
    super(message);
    this.name = "RpcError";
  }
}

export class AcpClient {
  readonly #output: Writable;
  readonly #maxFrameBytes: number;
  readonly #requestTimeoutMS: number;
  readonly #pending = new Map<string, PendingRequest>();
  readonly #notificationListeners = new Set<(notification: RpcNotification) => void>();
  readonly #closeListeners = new Set<(error: Error) => void>();
  #nextID = 0;
  #buffer = Buffer.alloc(0);
  #closedError: Error | undefined;

  public constructor(input: Readable, output: Writable, options: AcpClientOptions = {}) {
    this.#output = output;
    this.#maxFrameBytes = options.maxFrameBytes ?? defaultMaxFrameBytes;
    this.#requestTimeoutMS = options.requestTimeoutMS ?? defaultRequestTimeoutMS;
    input.on("data", (chunk: Buffer | string) => {
      this.#accept(Buffer.isBuffer(chunk) ? chunk : Buffer.from(chunk));
    });
    input.once("end", () => {
      this.#close(new Error("ACP stdout closed"));
    });
    input.once("error", (error: Error) => {
      this.#close(new Error(`ACP stdout failed: ${error.message}`));
    });
    output.once("error", (error: Error) => {
      this.#close(new Error(`ACP stdin failed: ${error.message}`));
    });
  }

  public onNotification(listener: (notification: RpcNotification) => void): () => void {
    this.#notificationListeners.add(listener);
    return () => {
      this.#notificationListeners.delete(listener);
    };
  }

  public onClose(listener: (error: Error) => void): () => void {
    this.#closeListeners.add(listener);
    return () => {
      this.#closeListeners.delete(listener);
    };
  }

  public async request(method: string, params: unknown = {}): Promise<unknown> {
    if (method.length === 0) {
      throw new TypeError("RPC method is required");
    }
    if (this.#closedError !== undefined) {
      throw this.#closedError;
    }
    const id = String(++this.#nextID);
    const response = new Promise<unknown>((resolve, reject) => {
      const timer = setTimeout(() => {
        this.#pending.delete(id);
        reject(new Error(`ACP request ${method} timed out`));
      }, this.#requestTimeoutMS);
      this.#pending.set(id, { resolve, reject, timer });
    });
    try {
      await this.#write({ jsonrpc: "2.0", id, method, params });
    } catch (error) {
      const pending = this.#pending.get(id);
      if (pending !== undefined) {
        clearTimeout(pending.timer);
        this.#pending.delete(id);
        pending.reject(asError(error));
      }
    }
    return response;
  }

  public close(reason = "ACP client closed"): void {
    this.#close(new Error(reason));
  }

  async #write(frame: unknown): Promise<void> {
    const data = `${JSON.stringify(frame)}\n`;
    await new Promise<void>((resolve, reject) => {
      this.#output.write(data, (error) => {
        if (error !== null && error !== undefined) {
          reject(error);
          return;
        }
        resolve();
      });
    });
  }

  #accept(chunk: Buffer): void {
    if (this.#closedError !== undefined) {
      return;
    }
    this.#buffer = Buffer.concat([this.#buffer, chunk]);
    if (this.#buffer.length > this.#maxFrameBytes && !this.#buffer.includes(0x0a)) {
      this.#close(new Error("ACP frame exceeds the configured size limit"));
      return;
    }
    for (;;) {
      const newline = this.#buffer.indexOf(0x0a);
      if (newline < 0) {
        return;
      }
      const line = this.#buffer.subarray(0, newline);
      this.#buffer = this.#buffer.subarray(newline + 1);
      if (line.length === 0) {
        continue;
      }
      if (line.length > this.#maxFrameBytes) {
        this.#close(new Error("ACP frame exceeds the configured size limit"));
        return;
      }
      try {
        this.#dispatch(JSON.parse(line.toString("utf8")) as unknown);
      } catch (error) {
        this.#close(new Error(`invalid ACP frame: ${asError(error).message}`));
        return;
      }
    }
  }

  #dispatch(value: unknown): void {
    if (!isObject(value) || value["jsonrpc"] !== "2.0") {
      throw new TypeError("JSON-RPC frame must be a 2.0 object");
    }
    if (typeof value["method"] === "string") {
      if (Object.hasOwn(value, "id")) {
        throw new TypeError("server-to-client requests are unsupported");
      }
      const notification = {
        method: value["method"],
        params: value["params"] ?? {},
      };
      for (const listener of this.#notificationListeners) {
        listener(notification);
      }
      return;
    }
    const id = value["id"];
    if (typeof id !== "string" && typeof id !== "number") {
      throw new TypeError("JSON-RPC response id is invalid");
    }
    const pending = this.#pending.get(String(id));
    if (pending === undefined) {
      return;
    }
    clearTimeout(pending.timer);
    this.#pending.delete(String(id));
    if (Object.hasOwn(value, "error")) {
      const rpcError = value["error"];
      if (!isObject(rpcError) ||
        typeof rpcError["code"] !== "number" ||
        typeof rpcError["message"] !== "string") {
        pending.reject(new TypeError("JSON-RPC error object is invalid"));
        return;
      }
      pending.reject(new RpcError(rpcError["code"], rpcError["message"], rpcError["data"]));
      return;
    }
    if (!Object.hasOwn(value, "result")) {
      pending.reject(new TypeError("JSON-RPC response has neither result nor error"));
      return;
    }
    pending.resolve(value["result"]);
  }

  #close(error: Error): void {
    if (this.#closedError !== undefined) {
      return;
    }
    this.#closedError = error;
    for (const pending of this.#pending.values()) {
      clearTimeout(pending.timer);
      pending.reject(error);
    }
    this.#pending.clear();
    for (const listener of this.#closeListeners) {
      listener(error);
    }
  }
}

function isObject(value: unknown): value is JsonObject {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function asError(value: unknown): Error {
  return value instanceof Error ? value : new Error(String(value));
}
