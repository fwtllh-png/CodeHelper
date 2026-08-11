import assert from "node:assert/strict";
import test from "node:test";

import {
  AcpRequestCapture,
  type RequestObservation,
  type RequestTransport,
} from "./request-capture.js";

void test("AcpRequestCapture correlates submit request and response identities", async () => {
  const observations: Array<{ kind: string; data: unknown }> = [];
  const times = [100, 125];
  const capture = new AcpRequestCapture(
    observer(observations),
    () => times.shift() ?? 125,
  );
  const result = await capture.request(
    new StubTransport({
      accepted: true,
      operationId: "operation-1",
      turnId: "turn-1",
      itemId: "item-1",
    }),
    "session/submit",
    {
      sessionId: "session-1",
      operation: {
        kind: "turn.start",
        payload: { prompt: "must not be copied into capture metadata" },
      },
    },
  );

  assert.equal((result as { accepted: boolean }).accepted, true);
  assert.deepEqual(observations, [
    {
      kind: "acp.request.started",
      data: {
        request_id: 1,
        method: "session/submit",
        session_id: "session-1",
        operation_kind: "turn.start",
      },
    },
    {
      kind: "acp.request.completed",
      data: {
        request_id: 1,
        method: "session/submit",
        duration_ms: 25,
        session_id: "session-1",
        operation_kind: "turn.start",
        operation_id: "operation-1",
        turn_id: "turn-1",
        item_id: "item-1",
      },
    },
  ]);
  assert.doesNotMatch(JSON.stringify(observations), /must not be copied/u);
});

void test("AcpRequestCapture records bounded request failures and rethrows", async () => {
  const observations: Array<{ kind: string; data: unknown }> = [];
  const capture = new AcpRequestCapture(
    observer(observations),
    () => 10,
  );
  const failure = new Error("transport disconnected");

  await assert.rejects(
    capture.request(new StubTransport(failure), "session/status", {
      sessionId: "session-2",
    }),
    failure,
  );
  assert.deepEqual(observations.at(-1), {
    kind: "acp.request.failed",
    data: {
      request_id: 1,
      method: "session/status",
      duration_ms: 0,
      session_id: "session-2",
      error: "transport disconnected",
    },
  });
});

function observer(
  observations: Array<{ kind: string; data: unknown }>,
): RequestObservation {
  return (kind, data) => {
    observations.push({ kind, data });
  };
}

class StubTransport implements RequestTransport {
  readonly #result: unknown;

  public constructor(result: unknown) {
    this.#result = result;
  }

  public request(): Promise<unknown> {
    return this.#result instanceof Error
      ? Promise.reject(this.#result)
      : Promise.resolve(this.#result);
  }
}
