import assert from "node:assert/strict";
import test from "node:test";

import { BackgroundQuery, type BackgroundTransport } from "./query.js";

void test("BackgroundQuery decodes workspace-scoped read models", async () => {
  const transport = new FixtureTransport();
  const query = new BackgroundQuery(transport);
  assert.equal((await query.threads())[0]?.id, "thread_1");
  assert.equal((await query.agents())[0]?.workspace, "/workspace");
  assert.equal((await query.tasks())[0]?.executor, "agent_turn");
  assert.equal((await query.usage()).totalTokens, 15);
  assert.deepEqual(
    transport.calls.map((call) => call.method),
    ["thread/list", "agent/list", "task/list", "usage/query"],
  );
  assert.equal(
    (transport.calls[0]?.params as Record<string, unknown>)["sessionId"],
    undefined,
  );
});

void test("BackgroundQuery rejects malformed query responses", async () => {
  const query = new BackgroundQuery({
    request: () => Promise.resolve({ threads: [{ id: 3 }] }),
  });
  await assert.rejects(query.threads(), /thread.id/);
});

class FixtureTransport implements BackgroundTransport {
  public readonly calls: {
    readonly method: string;
    readonly params: unknown;
  }[] = [];

  public request(method: string, params?: unknown): Promise<unknown> {
    this.calls.push({ method, params });
    switch (method) {
      case "thread/list":
        return Promise.resolve({ threads: [{
          id: "thread_1", session_id: "session_1", title: "Work",
          status: "open", updated_at: "2026-08-04T10:00:00Z",
        }] });
      case "agent/list":
        return Promise.resolve({ agents: [{
          id: "agent-1", workspace: "/workspace", session_id: "session_1",
          role: "explore", status: "running", closed: false,
        }] });
      case "task/list":
        return Promise.resolve({ tasks: [{
          id: "task_1", session_id: "session_1", kind: "agent",
          state: "running", executor: "agent_turn", attempt: 1,
          max_attempts: 3, updated_at: "2026-08-04T10:00:00Z",
        }] });
      case "usage/query":
        return Promise.resolve({ rollup: {
          turns: 1, calls: 2, input_tokens: 10, output_tokens: 4,
          reasoning_tokens: 1, cached_tokens: 3, total_tokens: 15,
          cost_microunits: 20, cost_known: true,
        } });
      default:
        return Promise.reject(new Error(`unexpected method ${method}`));
    }
  }
}
