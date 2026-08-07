import assert from "node:assert/strict";
import test from "node:test";

import { SessionCommands, type SessionTransport } from "./session.js";
import { createWorkspaceIdentity } from "../workspace/identity.js";

void test("SessionCommands submits structured editor context", async () => {
  const transport = new FakeTransport();
  const identity = createWorkspaceIdentity("file:///workspace", "/workspace");
  const session = new SessionCommands(
    transport,
    "session_1",
    () => true,
    identity,
  );
  const receipt = await session.submitPrompt(" inspect ", [{
    kind: "selection",
    uri: "file:///workspace/value.go",
    path: "value.go",
    document_version: 2,
    digest: "a".repeat(64),
    explicit: true,
    range: {
      start: { line: 1, character: 0 },
      end: { line: 1, character: 5 },
    },
  }]);
  assert.equal(receipt.turnId, "turn_1");
  const params = transport.calls[0]?.params as {
    readonly sessionId: string;
    readonly operation: {
      readonly kind: string;
      readonly payload: Readonly<Record<string, unknown>>;
    };
  };
  assert.equal(params.sessionId, "session_1");
  assert.equal(params.operation.kind, "turn.start");
  assert.equal(params.operation.payload["prompt"], "inspect");
  assert.equal(Array.isArray(params.operation.payload["context"]), true);
  assert.deepEqual(params.operation.payload["workspace_identity"], identity);
});

void test("SessionCommands refuses approve when workspace is untrusted", async () => {
  const session = new SessionCommands(new FakeTransport(), "session_1", () => false);
  await assert.rejects(
    session.decideApproval(
      "turn_1",
      "approval_1",
      "approve",
      "once",
      "2026-08-04T12:00:00Z",
    ),
    /untrusted workspace/,
  );
});

void test("SessionCommands binds approval to an edit plan identity", async () => {
  const transport = new FakeTransport();
  const session = new SessionCommands(transport, "session_1", () => true);
  await session.decideApproval(
    "turn_1",
    "approval_1",
    "approve",
    "once",
    "2026-08-04T12:00:00Z",
    "a".repeat(64),
  );
  const params = transport.calls[0]?.params as {
    readonly operation: {
      readonly payload: Readonly<Record<string, unknown>>;
    };
  };
  assert.equal(params.operation.payload["plan_id"], "a".repeat(64));
});

void test("SessionCommands updates Runtime-owned profile by revision", async () => {
  const transport: SessionTransport = {
    request(method, params) {
      assert.equal(method, "session/profile/update");
      assert.deepEqual(params, {
        sessionId: "session_1",
        expectedRevision: 3,
        patch: { mode: "plan" },
      });
      return Promise.resolve({
        profile: {
          version: 1,
          revision: 4,
          mode: "plan",
          provider: "fixture",
          model: "fixture-model",
          approval_posture: "suggest",
          execution_target: "local",
          max_steps: 32,
          prompt_cache_revision: 2,
        },
        prompt_cache_reset: true,
        reset_reason: "mode",
      });
    },
  };
  const session = new SessionCommands(transport, "session_1", () => true);
  const updated = await session.updateProfile(3, { mode: "plan" });
  assert.equal(updated.profile.revision, 4);
  assert.equal(updated.prompt_cache_reset, true);
});

void test("SessionCommands blocks untrusted profile escalation", async () => {
  const session = new SessionCommands(new FakeTransport(), "session_1", () => false);
  await assert.rejects(
    session.updateProfile(1, { approval_posture: "bypass" }),
    /untrusted workspace/u,
  );
});

class FakeTransport implements SessionTransport {
  public readonly calls: { readonly method: string; readonly params: unknown }[] = [];

  public request(method: string, params?: unknown): Promise<unknown> {
    this.calls.push({ method, params });
    return Promise.resolve({
      operationId: "operation_1",
      accepted: true,
      turnId: "turn_1",
      itemId: "item_1",
    });
  }
}
