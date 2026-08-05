import assert from "node:assert/strict";
import test from "node:test";
import { PassThrough } from "node:stream";

import { AcpClient, RpcError } from "./client.js";

void test("AcpClient correlates split responses and forwards notifications", async () => {
  const serverOutput = new PassThrough();
  const serverInput = new PassThrough();
  const client = new AcpClient(serverOutput, serverInput);
  const notifications: unknown[] = [];
  client.onNotification((notification) => {
    notifications.push(notification);
  });

  const request = client.request("initialize", { protocolVersion: 2 });
  const frame = await readLine(serverInput);
  assert.equal(frame["method"], "initialize");
  assert.equal(frame["id"], "1");

  serverOutput.write('{"jsonrpc":"2.0","method":"session/update","params":{"ok":');
  serverOutput.write('true}}\n{"jsonrpc":"2.0","id":"1","result":{"ready":true}}\n');
  assert.deepEqual(await request, { ready: true });
  assert.deepEqual(notifications, [{
    method: "session/update",
    params: { ok: true },
  }]);
  client.close();
});

void test("AcpClient surfaces JSON-RPC errors", async () => {
  const serverOutput = new PassThrough();
  const serverInput = new PassThrough();
  const client = new AcpClient(serverOutput, serverInput);
  const request = client.request("session/load");
  await readLine(serverInput);
  serverOutput.write(
    '{"jsonrpc":"2.0","id":"1","error":{"code":-32602,"message":"bad params"}}\n',
  );
  await assert.rejects(request, (error: unknown) => {
    assert.ok(error instanceof RpcError);
    assert.equal(error.code, -32602);
    return true;
  });
  client.close();
});

void test("AcpClient rejects pending requests after an oversized frame", async () => {
  const serverOutput = new PassThrough();
  const serverInput = new PassThrough();
  const client = new AcpClient(serverOutput, serverInput, {
    maxFrameBytes: 32,
    requestTimeoutMS: 1_000,
  });
  const request = client.request("initialize");
  await readLine(serverInput);
  serverOutput.write("x".repeat(33));
  await assert.rejects(request, /frame exceeds/);
});

async function readLine(stream: PassThrough): Promise<Readonly<Record<string, unknown>>> {
  let data = "";
  for await (const chunk of stream) {
    data += String(chunk);
    const newline = data.indexOf("\n");
    if (newline >= 0) {
      return JSON.parse(data.slice(0, newline)) as Readonly<Record<string, unknown>>;
    }
  }
  throw new Error("stream ended before a line was available");
}
