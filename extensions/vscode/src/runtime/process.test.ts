import assert from "node:assert/strict";
import test from "node:test";
import { mkdtemp, mkdir, rm, writeFile } from "node:fs/promises";
import { join } from "node:path";
import { tmpdir } from "node:os";

import {
  discoverBinary,
  enrichRuntimeError,
  runtimeArguments,
  verifyBinary,
  type RuntimeLaunchOptions,
} from "./process.js";
import { createWorkspaceIdentity } from "../workspace/identity.js";

void test("discoverBinary honors configured, development, then PATH order", async () => {
  const root = await mkdtemp(join(tmpdir(), "codehelper-discovery-"));
  try {
    const configured = await executable(root, "configured");
    const developmentRoot = join(root, "development");
    const developmentBin = join(developmentRoot, "bin");
    await mkdir(developmentBin, { recursive: true });
    const development = await executable(developmentBin, "codehelper");
    const pathBin = join(root, "path");
    await mkdir(pathBin);
    await executable(pathBin, "codehelper");

    assert.equal(await discoverBinary({
      configuredPath: configured,
      developmentRoot,
      pathEnvironment: pathBin,
    }), configured);
    assert.equal(await discoverBinary({
      developmentRoot,
      pathEnvironment: pathBin,
    }), development);
  } finally {
    await rm(root, { recursive: true, force: true });
  }
});

void test("discoverBinary rejects relative configured paths", async () => {
  await assert.rejects(
    discoverBinary({ configuredPath: "./codehelper" }),
    /absolute path/,
  );
});

void test("verifyBinary validates CodeHelper JSON identity", async () => {
  const root = await mkdtemp(join(tmpdir(), "codehelper-version-"));
  try {
    const binary = await executable(
      root,
      "codehelper",
      "#!/bin/sh\nprintf '%s\\n' " +
      `'{"name":"codehelper","version":"dev","commit":"abc",` +
      `"os":"darwin","arch":"arm64","acp_protocol_min":2,` +
      `"acp_protocol_max":2,"operation_schema_version":1}'\n`,
    );
    assert.deepEqual(await verifyBinary(binary), {
      name: "codehelper",
      version: "dev",
      commit: "abc",
      os: "darwin",
      arch: "arm64",
      acpProtocolMin: 2,
      acpProtocolMax: 2,
      operationSchemaVersion: 1,
    });
  } finally {
    await rm(root, { recursive: true, force: true });
  }
});

void test("runtimeArguments forwards an explicit trusted config path", () => {
  const options = runtimeOptions("/Users/test/.config/codehelper/config.toml");
  const args = runtimeArguments(options);
  const index = args.indexOf("--config");
  assert.notEqual(index, -1);
  assert.equal(args[index + 1], options.configPath);
});

void test("runtimeArguments omits config when environment configuration is used", () => {
  assert.equal(runtimeArguments(runtimeOptions()).includes("--config"), false);
});

void test("enrichRuntimeError retains the final Runtime stderr diagnostic", () => {
  assert.equal(
    enrichRuntimeError(
      new Error("ACP stdout closed"),
      "starting\ncodehelper: --provider and --model are required\n",
    ).message,
    "ACP stdout closed: codehelper: --provider and --model are required",
  );
});

async function executable(
  directory: string,
  name: string,
  contents = "#!/bin/sh\nexit 0\n",
): Promise<string> {
  const path = join(directory, name);
  await writeFile(path, contents, { mode: 0o755 });
  return path;
}

function runtimeOptions(configPath?: string): RuntimeLaunchOptions {
  const workspaceRoot = "/workspace";
  return {
    binaryPath: "/bin/codehelper",
    workspaceRoot,
    dataDirectory: "/data",
    ...(configPath === undefined ? {} : { configPath }),
    posture: "suggest",
    maxSteps: 8,
    workspaceIdentity: createWorkspaceIdentity(
      "file:///workspace",
      workspaceRoot,
    ),
    diagnostics: () => undefined,
  };
}
