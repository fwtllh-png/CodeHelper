import { createHash } from "node:crypto";
import assert from "node:assert/strict";
import { chmod, mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";

import type { ReleaseArtifact, ReleaseManifest } from "./manifest.js";
import { ManagedBinaryStore } from "./store.js";
import type { BinaryVersion } from "../runtime/process.js";

void test("managed binary install publishes pending then healthy atomically", async () => {
  const root = await mkdtemp(join(tmpdir(), "codehelper-managed-"));
  try {
    const content = Buffer.from("binary-v1");
    const artifact = releaseArtifact("0.0.1", "commit1", content);
    const store = testStore(root, artifact);
    const installed = await store.install(releaseManifest(1, artifact), artifact, content);
    assert.equal((await store.resolve())?.digest, installed.digest);
    await store.markHealthy(installed.digest);
    const state: unknown = JSON.parse(
      await readFile(join(root, "state.json"), "utf8"),
    );
    assert.ok(isObject(state));
    assert.equal(state.pending_digest, undefined);
    const receipts = state["receipts"];
    assert.ok(Array.isArray(receipts));
    const receipt: unknown = receipts.at(-1);
    assert.ok(isObject(receipt));
    assert.equal(receipt["result"], "healthy");
  } finally {
    await rm(root, { recursive: true, force: true });
  }
});

void test("managed binary rolls failed pending update to last-known-good", async () => {
  const root = await mkdtemp(join(tmpdir(), "codehelper-rollback-"));
  try {
    const firstContent = Buffer.from("binary-v1");
    const secondContent = Buffer.from("binary-v2");
    const first = releaseArtifact("0.0.1", "commit1", firstContent);
    const second = releaseArtifact("0.0.2", "commit2", secondContent);
    const versions = new Map([
      [sha256(firstContent), binaryVersion(first)],
      [sha256(secondContent), binaryVersion(second)],
    ]);
    const store = new ManagedBinaryStore(root, {
      platform: "linux",
      architecture: "arm64",
      verify: (path) => {
        return readFile(path).then((content) => {
          const version = versions.get(sha256(content));
          if (version === undefined) throw new Error("unknown binary");
          return version;
        });
      },
    });
    const installed = await store.install(
      releaseManifest(1, first), first, firstContent,
    );
    await store.markHealthy(installed.digest);
    await store.install(releaseManifest(2, second), second, secondContent);
    assert.equal((await store.resolve())?.version, "0.0.2");
    assert.equal(await store.rollbackPending(), true);
    assert.equal((await store.resolve())?.version, "0.0.1");
  } finally {
    await rm(root, { recursive: true, force: true });
  }
});

void test("managed binary fails closed on replay, corruption, and revocation", async () => {
  const root = await mkdtemp(join(tmpdir(), "codehelper-corrupt-"));
  try {
    const content = Buffer.from("binary-v1");
    const artifact = releaseArtifact("0.0.1", "commit1", content);
    const store = testStore(root, artifact);
    const installed = await store.install(releaseManifest(1, artifact), artifact, content);
    await assert.rejects(
      store.install(releaseManifest(1, artifact), artifact, content),
      /not monotonic/u,
    );
    await store.markHealthy(installed.digest);
    await chmod(installed.path, 0o700);
    await writeFile(installed.path, "tampered");
    await assert.rejects(store.resolve(), /corrupt/u);
  } finally {
    await rm(root, { recursive: true, force: true });
  }
});

void test("managed binary serializes concurrent install and cleans failed staging", async () => {
  const root = await mkdtemp(join(tmpdir(), "codehelper-concurrent-"));
  try {
    const content = Buffer.from("binary-v1");
    const artifact = releaseArtifact("0.0.1", "commit1", content);
    const store = testStore(root, artifact);
    const results = await Promise.allSettled([
      store.install(releaseManifest(1, artifact), artifact, content),
      store.install(releaseManifest(1, artifact), artifact, content),
    ]);
    assert.equal(results.filter((result) => result.status === "fulfilled").length, 1);
    assert.equal(results.filter((result) => result.status === "rejected").length, 1);

    const failedRoot = await mkdtemp(join(tmpdir(), "codehelper-failed-stage-"));
    try {
      const failed = new ManagedBinaryStore(failedRoot, {
        platform: "linux",
        architecture: "arm64",
        verify: () => Promise.reject(new Error("injected pre-pointer crash")),
      });
      await assert.rejects(
        failed.install(releaseManifest(1, artifact), artifact, content),
        /pre-pointer crash/u,
      );
      assert.equal(await failed.resolve(), undefined);
    } finally {
      await rm(failedRoot, { recursive: true, force: true });
    }
  } finally {
    await rm(root, { recursive: true, force: true });
  }
});

void test("managed binary rejects insufficient disk before staging", async () => {
  const root = await mkdtemp(join(tmpdir(), "codehelper-disk-budget-"));
  try {
    const content = Buffer.from("binary-v1");
    const artifact = releaseArtifact("0.0.1", "commit1", content);
    const store = new ManagedBinaryStore(root, {
      platform: "linux",
      architecture: "arm64",
      availableBytes: () => Promise.resolve(0),
      verify: () => Promise.resolve(binaryVersion(artifact)),
    });
    await assert.rejects(
      store.install(releaseManifest(1, artifact), artifact, content),
      /insufficient disk space/u,
    );
    assert.equal(await store.resolve(), undefined);
  } finally {
    await rm(root, { recursive: true, force: true });
  }
});

function testStore(root: string, artifact: ReleaseArtifact): ManagedBinaryStore {
  return new ManagedBinaryStore(root, {
    platform: "linux",
    architecture: "arm64",
    verify: () => Promise.resolve(binaryVersion(artifact)),
  });
}

function releaseManifest(
  sequence: number,
  artifact: ReleaseArtifact,
): ReleaseManifest {
  return {
    schemaVersion: 1,
    manifestVersion: 1,
    channel: "stable",
    sequence,
    generatedAt: "2026-08-05T00:00:00Z",
    artifacts: [artifact],
    revokedVersions: [],
    revokedDigests: [],
    keyStatements: [],
    signature: { keyId: "test", value: "signed" },
  };
}

function releaseArtifact(
  version: string,
  commit: string,
  content: Uint8Array,
): ReleaseArtifact {
  return {
    version,
    commit,
    buildTime: "2026-08-05T00:00:00Z",
    target: { os: "linux", arch: "arm64" },
    bytes: content.byteLength,
    sha256: sha256(content),
    url: "https://releases.example.test/codehelper-linux-arm64",
    acpProtocolMin: 1,
    acpProtocolMax: 2,
    operationSchemaVersion: 1,
    requiredFeatures: ["editor_context_v2", "workspace_identity_v1"],
    extensionVersionRange: ">=0.0.1 <0.1.0",
    sbomSha256: "b".repeat(64),
    provenanceSha256: "c".repeat(64),
  };
}

function binaryVersion(artifact: ReleaseArtifact): BinaryVersion {
  return {
    name: "codehelper",
    version: artifact.version,
    commit: artifact.commit,
    os: artifact.target.os,
    arch: artifact.target.arch,
    acpProtocolMin: artifact.acpProtocolMin,
    acpProtocolMax: artifact.acpProtocolMax,
    operationSchemaVersion: artifact.operationSchemaVersion,
  };
}

function sha256(value: Uint8Array): string {
  return createHash("sha256").update(value).digest("hex");
}

function isObject(value: unknown): value is Readonly<Record<string, unknown>> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}
