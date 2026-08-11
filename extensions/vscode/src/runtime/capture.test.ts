import assert from "node:assert/strict";
import test from "node:test";
import { mkdtemp, readFile, rm, stat } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";

import {
  clearRuntimeCaptureRequest,
  requestRuntimeCapture,
  RuntimeCapture,
  runtimeCaptureRequested,
} from "./capture.js";

void test("RuntimeCapture writes an ordered private JSONL timeline", async () => {
  const root = await mkdtemp(join(tmpdir(), "codehelper-capture-"));
  try {
    const now = () => new Date("2026-08-10T01:02:03.004Z");
    const capture = await RuntimeCapture.open(
      root,
      { workspace_root_id: "root-1" },
      { now },
    );
    capture.record("runtime.state", { state: "ready", restartAttempt: 0 });
    capture.record("runtime.event", {
      session_id: "session-1",
      replayed: false,
      event: { sequence: 7, kind: "tool.start" },
    });
    await capture.flush();
    await capture.close("user_stopped");
    capture.record("runtime.event", { event: "ignored after close" });

    const lines = (await readFile(capture.path, "utf8"))
      .trim()
      .split("\n")
      .map((line) => JSON.parse(line) as Record<string, unknown>);
    assert.deepEqual(
      lines.map((line) => line["kind"]),
      [
        "capture.started",
        "runtime.state",
        "runtime.event",
        "capture.stopped",
      ],
    );
    assert.deepEqual(
      lines.map((line) => line["capture_sequence"]),
      [1, 2, 3, 4],
    );
    assert.equal(new Set(lines.map((line) => line["capture_id"])).size, 1);
    assert.equal((await stat(capture.path)).mode & 0o777, 0o600);
  } finally {
    await rm(root, { recursive: true, force: true });
  }
});

void test("RuntimeCapture reports serialization failures without throwing", async () => {
  const root = await mkdtemp(join(tmpdir(), "codehelper-capture-error-"));
  const errors: Error[] = [];
  try {
    const capture = await RuntimeCapture.open(
      root,
      { workspace_root_id: "root-1" },
      { onError: (error) => errors.push(error) },
    );
    capture.record("invalid", { unsupported: 1n });
    await capture.close("test_finished");
    assert.equal(errors.length, 1);
    assert.match(errors[0]?.message ?? "", /BigInt/u);
  } finally {
    await rm(root, { recursive: true, force: true });
  }
});

void test("Runtime Capture request marker survives restart until cleared", async () => {
  const root = await mkdtemp(join(tmpdir(), "codehelper-capture-marker-"));
  try {
    assert.equal(await runtimeCaptureRequested(root), false);
    await requestRuntimeCapture(root);
    assert.equal(await runtimeCaptureRequested(root), true);
    await clearRuntimeCaptureRequest(root);
    assert.equal(await runtimeCaptureRequested(root), false);
    await clearRuntimeCaptureRequest(root);
  } finally {
    await rm(root, { recursive: true, force: true });
  }
});
