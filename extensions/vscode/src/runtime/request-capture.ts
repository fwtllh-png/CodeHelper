export interface RequestTransport {
  request(method: string, params?: unknown): Promise<unknown>;
}

export type RequestObservation = (kind: string, data: unknown) => void;

export class AcpRequestCapture {
  readonly #observe: RequestObservation;
  readonly #now: () => number;
  #sequence = 0;

  public constructor(
    observe: RequestObservation,
    now: () => number = Date.now,
  ) {
    this.#observe = observe;
    this.#now = now;
  }

  public async request(
    transport: RequestTransport,
    method: string,
    params?: unknown,
  ): Promise<unknown> {
    const requestId = ++this.#sequence;
    const started = this.#now();
    const context = acpRequestContext(method, params);
    this.#observe("acp.request.started", {
      request_id: requestId,
      method,
      ...context,
    });
    try {
      const result = await transport.request(method, params);
      this.#observe("acp.request.completed", {
        request_id: requestId,
        method,
        duration_ms: this.#now() - started,
        ...context,
        ...acpResponseIdentity(result),
      });
      return result;
    } catch (error) {
      this.#observe("acp.request.failed", {
        request_id: requestId,
        method,
        duration_ms: this.#now() - started,
        ...context,
        error: error instanceof Error ? error.message : String(error),
      });
      throw error;
    }
  }
}

function acpRequestContext(
  method: string,
  params: unknown,
): Readonly<Record<string, unknown>> {
  if (!isRecord(params)) return {};
  const context: Record<string, unknown> = {};
  if (typeof params["sessionId"] === "string") {
    context["session_id"] = params["sessionId"];
  }
  if (method === "session/submit" && isRecord(params["operation"])) {
    const kind = params["operation"]["kind"];
    if (typeof kind === "string") context["operation_kind"] = kind;
  }
  return context;
}

function acpResponseIdentity(
  result: unknown,
): Readonly<Record<string, unknown>> {
  if (!isRecord(result)) return {};
  const identity: Record<string, unknown> = {};
  for (const [source, target] of [
    ["operationId", "operation_id"],
    ["turnId", "turn_id"],
    ["itemId", "item_id"],
  ] as const) {
    const value = result[source];
    if (typeof value === "string") identity[target] = value;
  }
  return identity;
}

function isRecord(value: unknown): value is Readonly<Record<string, unknown>> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}
