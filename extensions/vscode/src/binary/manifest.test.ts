import {
  generateKeyPairSync,
  sign,
} from "node:crypto";
import assert from "node:assert/strict";
import test from "node:test";

import {
  decodeAndVerifyManifest,
  selectArtifact,
  signedManifestPayload,
} from "./manifest.js";

void test("signed release manifest verifies canonical payload and target", () => {
  const fixture = signedFixture();
  const manifest = decodeAndVerifyManifest(
    Buffer.from(JSON.stringify(fixture.manifest)),
    { "release-2026": fixture.publicKey },
  );
  assert.equal(manifest.sequence, 7);
  assert.equal(
    selectArtifact(manifest, "linux", "arm64").sha256,
    "a".repeat(64),
  );
});

void test("release manifest rejects tampering, unknown keys, and fields", () => {
  const fixture = signedFixture();
  const tampered = structuredClone(fixture.manifest);
  tampered.sequence = 8;
  assert.throws(
    () => decodeAndVerifyManifest(
      Buffer.from(JSON.stringify(tampered)),
      { "release-2026": fixture.publicKey },
    ),
    /signature verification failed/u,
  );
  assert.throws(
    () => decodeAndVerifyManifest(
      Buffer.from(JSON.stringify(fixture.manifest)),
      {},
    ),
    /is not trusted/u,
  );
  const unknown = structuredClone(fixture.manifest) as unknown as
    Record<string, unknown>;
  unknown["feed_url"] = "https://attacker.invalid";
  assert.throws(
    () => decodeAndVerifyManifest(
      Buffer.from(JSON.stringify(unknown)),
      { "release-2026": fixture.publicKey },
    ),
    /missing or unknown fields/u,
  );
});

void test("release manifest rejects unsafe URL and revoked target", () => {
  const unsafe = signedFixture((manifest) => {
    const artifact = manifest.artifacts[0];
    assert.ok(artifact);
    artifact.url = "http://releases.example.test/codehelper";
  });
  assert.throws(
    () => decodeAndVerifyManifest(
      Buffer.from(JSON.stringify(unsafe.manifest)),
      { "release-2026": unsafe.publicKey },
    ),
    /HTTPS artifact URL/u,
  );

  const revoked = signedFixture((manifest) => {
    manifest.revoked_digests = ["a".repeat(64)];
  });
  const decoded = decodeAndVerifyManifest(
    Buffer.from(JSON.stringify(revoked.manifest)),
    { "release-2026": revoked.publicKey },
  );
  assert.throws(() => selectArtifact(decoded, "linux", "arm64"), /revoked/u);
  assert.throws(
    () => selectArtifact(decoded, "darwin", "arm64"),
    /no artifact/u,
  );
});

void test("release manifest accepts only root-authorized key rotation", () => {
  const fixture = signedFixture(undefined, true);
  assert.equal(
    decodeAndVerifyManifest(
      Buffer.from(JSON.stringify(fixture.manifest)),
      { "release-2026": fixture.publicKey },
    ).signature.keyId,
    "release-2027",
  );
  const tampered = structuredClone(fixture.manifest);
  const statement = tampered.key_statements[0] as Record<string, unknown>;
  statement["signed_by"] = "missing";
  assert.throws(
    () => decodeAndVerifyManifest(
      Buffer.from(JSON.stringify(tampered)),
      { "release-2026": fixture.publicKey },
    ),
    /rotation statement is invalid/u,
  );
});

interface FixtureArtifact {
  version: string;
  commit: string;
  build_time: string;
  target: { os: string; arch: string };
  bytes: number;
  sha256: string;
  url: string;
  acp_protocol: { min: number; max: number };
  operation_schema_version: number;
  required_features: string[];
  extension_version_range: string;
  sbom_sha256: string;
  provenance_sha256: string;
}

interface FixtureManifest {
  schema_version: number;
  manifest_version: number;
  channel: string;
  sequence: number;
  generated_at: string;
  artifacts: FixtureArtifact[];
  revoked_versions: string[];
  revoked_digests: string[];
  key_statements: unknown[];
  signature?: { key_id: string; value: string };
}

function signedFixture(
  mutate?: (manifest: FixtureManifest) => void,
  rotate = false,
): { manifest: FixtureManifest; publicKey: string } {
  const root = generateKeyPairSync("ed25519");
  const signing = rotate ? generateKeyPairSync("ed25519") : root;
  const manifest: FixtureManifest = {
    schema_version: 1,
    manifest_version: 1,
    channel: "stable",
    sequence: 7,
    generated_at: "2026-08-05T00:00:00Z",
    artifacts: [{
      version: "0.0.2",
      commit: "abc123",
      build_time: "2026-08-05T00:00:00Z",
      target: { os: "linux", arch: "arm64" },
      bytes: 1024,
      sha256: "a".repeat(64),
      url: "https://releases.example.test/codehelper-linux-arm64",
      acp_protocol: { min: 1, max: 2 },
      operation_schema_version: 1,
      required_features: ["editor_context_v2", "workspace_identity_v1"],
      extension_version_range: ">=0.0.1 <0.1.0",
      sbom_sha256: "b".repeat(64),
      provenance_sha256: "c".repeat(64),
    }],
    revoked_versions: [],
    revoked_digests: [],
    key_statements: [],
  };
  if (rotate) {
    const rotatedPublic = signing.publicKey.export({
      type: "spki",
      format: "pem",
    }).toString();
    const statement = {
      key_id: "release-2027",
      public_key: rotatedPublic,
      signed_by: "release-2026",
    };
    manifest.key_statements.push({
      ...statement,
      signature: sign(
        null,
        Buffer.from(JSON.stringify(statement, Object.keys(statement).sort())),
        root.privateKey,
      ).toString("base64"),
    });
  }
  mutate?.(manifest);
  manifest.signature = {
    key_id: rotate ? "release-2027" : "release-2026",
    value: sign(
      null,
      Buffer.from(signedManifestPayload(manifest)),
      signing.privateKey,
    ).toString("base64"),
  };
  return {
    manifest,
    publicKey: root.publicKey.export({ type: "spki", format: "pem" }).toString(),
  };
}
