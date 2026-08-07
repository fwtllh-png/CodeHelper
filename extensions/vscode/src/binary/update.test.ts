import assert from "node:assert/strict";
import { createHash, generateKeyPairSync, sign } from "node:crypto";
import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";

import {
  canonicalJSON,
  type ReleaseArtifact,
} from "./manifest.js";
import { ManagedBinaryStore } from "./store.js";
import { BinaryUpdateClient, officialManifestURL } from "./update.js";
import type { BinaryVersion } from "../runtime/process.js";

void test("signed update persists revocation before rejecting target", async () => {
  const root = await mkdtemp(join(tmpdir(), "codehelper-update-revoke-"));
  try {
    const content = Buffer.from("managed-v1");
    const artifact = releaseArtifact(content);
    const keys = generateKeyPairSync("ed25519");
    let manifest = signedManifest(1, artifact, keys.privateKey);
    const store = new ManagedBinaryStore(root, {
      platform: process.platform,
      architecture: process.arch,
      verify: () => Promise.resolve(binaryVersion(artifact)),
    });
    const client = new BinaryUpdateClient({
      store,
      trustRoots: {
        "test-root": keys.publicKey.export({
          type: "spki",
          format: "pem",
        }).toString(),
      },
      fetch: fakeFetch(() => manifest, content),
    });
    const installed = await client.installLatest("stable", true);
    await store.markHealthy(installed.digest);
    assert.equal((await store.resolve())?.digest, artifact.sha256);

    manifest = signedManifest(2, artifact, keys.privateKey, [artifact.sha256]);
    await assert.rejects(client.check("stable"), /revoked/u);
    await assert.rejects(store.resolve(), /revoked/u);
  } finally {
    await rm(root, { recursive: true, force: true });
  }
});

void test("update downloader rejects cross-origin redirect and truncation", async () => {
  const content = Buffer.from("managed-v1");
  const artifact = releaseArtifact(content);
  const keys = generateKeyPairSync("ed25519");
  const manifest = signedManifest(1, artifact, keys.privateKey);
  const trustRoots = {
    "test-root": keys.publicKey.export({
      type: "spki",
      format: "pem",
    }).toString(),
  };
  for (const fixture of [
    fakeFetch(() => manifest, content, "redirect"),
    fakeFetch(() => manifest, content.subarray(0, content.length - 1)),
  ]) {
    const root = await mkdtemp(join(tmpdir(), "codehelper-update-download-"));
    try {
      const store = new ManagedBinaryStore(root, {
        platform: process.platform,
        architecture: process.arch,
        verify: () => Promise.resolve(binaryVersion(artifact)),
      });
      const client = new BinaryUpdateClient({
        store,
        trustRoots,
        fetch: fixture,
      });
      await assert.rejects(
        client.installLatest("stable", true),
        /(?:fixed HTTPS origin|size does not match)/u,
      );
      assert.equal(await store.resolve(), undefined);
    } finally {
      await rm(root, { recursive: true, force: true });
    }
  }
});

void test("update manifest cache revalidates with ETag and signed bytes", async () => {
  const root = await mkdtemp(join(tmpdir(), "codehelper-update-cache-"));
  try {
    const content = Buffer.from("managed-v1");
    const artifact = releaseArtifact(content);
    const keys = generateKeyPairSync("ed25519");
    const manifest = signedManifest(1, artifact, keys.privateKey);
    let cached: { readonly etag: string; readonly bytes: Uint8Array } | undefined;
    let calls = 0;
    const fetcher: typeof fetch = (_input, init) => {
      calls++;
      const headers = new Headers(init?.headers);
      if (calls === 1) {
        assert.equal(headers.get("if-none-match"), null);
        return Promise.resolve(new Response(JSON.stringify(manifest), {
          status: 200,
          headers: { etag: "\"manifest-v1\"" },
        }));
      }
      assert.equal(headers.get("if-none-match"), "\"manifest-v1\"");
      return Promise.resolve(new Response(null, { status: 304 }));
    };
    const client = new BinaryUpdateClient({
      store: new ManagedBinaryStore(root),
      trustRoots: {
        "test-root": keys.publicKey.export({
          type: "spki",
          format: "pem",
        }).toString(),
      },
      manifestCache: {
        load: () => Promise.resolve(cached),
        save: (entry) => {
          cached = entry;
          return Promise.resolve();
        },
      },
      fetch: fetcher,
    });
    assert.equal((await client.check("stable")).artifact.sha256, artifact.sha256);
    assert.equal((await client.check("stable")).artifact.sha256, artifact.sha256);
    assert.equal(calls, 2);
  } finally {
    await rm(root, { recursive: true, force: true });
  }
});

function fakeFetch(
  manifest: () => unknown,
  content: Uint8Array,
  behavior: "content" | "redirect" = "content",
): typeof fetch {
  const handler: typeof fetch = (input) => {
    const url = input instanceof Request ? input.url : input.toString();
    if (url === officialManifestURL) {
      return Promise.resolve(
        new Response(JSON.stringify(manifest()), { status: 200 }),
      );
    }
    if (behavior === "redirect") {
      return Promise.resolve(new Response(null, {
        status: 302,
        headers: { location: "https://attacker.invalid/binary" },
      }));
    }
    return Promise.resolve(new Response(content, {
      status: 200,
      headers: { "content-length": String(content.byteLength) },
    }));
  };
  return handler;
}

function releaseArtifact(content: Uint8Array): ReleaseArtifact {
  const os = process.platform === "win32" ? "windows" : process.platform;
  const arch = process.arch === "x64" ? "amd64" : process.arch;
  return {
    version: "0.0.1",
    commit: "update-test",
    buildTime: "2026-08-05T00:00:00Z",
    target: { os, arch },
    bytes: content.byteLength,
    sha256: digest(content),
    url: "https://github.com/fwtllh-png/CodeHelper/releases/download/" +
      "v0.0.1/codehelper",
    acpProtocolMin: 1,
    acpProtocolMax: 2,
    operationSchemaVersion: 1,
    requiredFeatures: [
      "editor_context_v2",
      "session_profile_v1",
      "session_lifecycle_v1",
      "unified_tool_catalog_v1",
      "workspace_identity_v1",
    ],
    extensionVersionRange: ">=0.0.1 <0.1.0",
    sbomSha256: "b".repeat(64),
    provenanceSha256: "c".repeat(64),
  };
}

function signedManifest(
  sequence: number,
  artifact: ReleaseArtifact,
  privateKey: ReturnType<typeof generateKeyPairSync>["privateKey"],
  revokedDigests: readonly string[] = [],
): unknown {
  const raw = {
    schema_version: 1,
    manifest_version: 1,
    channel: "stable",
    sequence,
    generated_at: "2026-08-05T00:00:00Z",
    artifacts: [{
      version: artifact.version,
      commit: artifact.commit,
      build_time: artifact.buildTime,
      target: artifact.target,
      bytes: artifact.bytes,
      sha256: artifact.sha256,
      url: artifact.url,
      acp_protocol: {
        min: artifact.acpProtocolMin,
        max: artifact.acpProtocolMax,
      },
      operation_schema_version: artifact.operationSchemaVersion,
      required_features: artifact.requiredFeatures,
      extension_version_range: artifact.extensionVersionRange,
      sbom_sha256: artifact.sbomSha256,
      provenance_sha256: artifact.provenanceSha256,
    }],
    revoked_versions: [],
    revoked_digests: revokedDigests,
    key_statements: [],
  };
  return {
    ...raw,
    signature: {
      key_id: "test-root",
      value: sign(
        null,
        Buffer.from(canonicalJSON(raw)),
        privateKey,
      ).toString("base64"),
    },
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

function digest(content: Uint8Array): string {
  return createHash("sha256").update(content).digest("hex");
}
