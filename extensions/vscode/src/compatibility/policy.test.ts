import assert from "node:assert/strict";
import test from "node:test";

import { assertCompatibleBinary } from "./policy.js";
import type { BinaryVersion } from "../runtime/process.js";

const compatible: BinaryVersion = {
  name: "codehelper",
  version: "0.0.1",
  commit: "abc",
  os: "darwin",
  arch: "arm64",
  acpProtocolMin: 2,
  acpProtocolMax: 2,
  operationSchemaVersion: 1,
};

void test("binary compatibility binds version, target, and protocol", () => {
  assert.doesNotThrow(() => {
    assertCompatibleBinary(compatible, false, "darwin", "arm64");
  });
  assert.throws(() => {
    assertCompatibleBinary({ ...compatible, arch: "amd64" }, false, "darwin", "arm64");
  },
  /does not match/u);
  assert.throws(() => {
    assertCompatibleBinary({
      ...compatible,
      acpProtocolMin: 3,
      acpProtocolMax: 3,
    }, false, "darwin", "arm64");
  },
  /protocol/u);
  assert.throws(() => {
    assertCompatibleBinary({ ...compatible, version: "0.1.0" }, false, "darwin", "arm64");
  },
  /outside/u);
});

void test("development binary is accepted only in development mode", () => {
  const binary = { ...compatible, version: "dev" };
  assert.doesNotThrow(() => {
    assertCompatibleBinary(binary, true, "darwin", "arm64");
  });
  assert.throws(() => {
    assertCompatibleBinary(binary, false, "darwin", "arm64");
  },
  /development/u);
});
