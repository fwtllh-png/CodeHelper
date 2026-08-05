import { createPublicKey, verify } from "node:crypto";

export interface ReleaseTarget {
  readonly os: string;
  readonly arch: string;
}

export interface ReleaseArtifact {
  readonly version: string;
  readonly commit: string;
  readonly buildTime: string;
  readonly target: ReleaseTarget;
  readonly bytes: number;
  readonly sha256: string;
  readonly url: string;
  readonly acpProtocolMin: number;
  readonly acpProtocolMax: number;
  readonly operationSchemaVersion: number;
  readonly requiredFeatures: readonly string[];
  readonly extensionVersionRange: string;
  readonly sbomSha256: string;
  readonly provenanceSha256: string;
}

export interface ReleaseManifest {
  readonly schemaVersion: 1;
  readonly manifestVersion: 1;
  readonly channel: "stable" | "preview";
  readonly sequence: number;
  readonly generatedAt: string;
  readonly artifacts: readonly ReleaseArtifact[];
  readonly revokedVersions: readonly string[];
  readonly revokedDigests: readonly string[];
  readonly keyStatements: readonly KeyStatement[];
  readonly signature: {
    readonly keyId: string;
    readonly value: string;
  };
}

export type TrustRoots = Readonly<Record<string, string>>;
export interface KeyStatement {
  readonly keyId: string;
  readonly publicKey: string;
  readonly signedBy: string;
  readonly signature: string;
}

const digestPattern = /^[0-9a-f]{64}$/u;
const identifierPattern = /^[A-Za-z0-9._-]{1,128}$/u;
const artifactKeys = [
  "version", "commit", "build_time", "target", "bytes", "sha256", "url",
  "acp_protocol", "operation_schema_version", "required_features",
  "extension_version_range", "sbom_sha256", "provenance_sha256",
] as const;
const manifestKeys = [
  "schema_version", "manifest_version", "channel", "sequence", "generated_at",
  "artifacts", "revoked_versions", "revoked_digests", "key_statements",
  "signature",
] as const;

export function decodeAndVerifyManifest(
  data: Uint8Array,
  trustRoots: TrustRoots,
): ReleaseManifest {
  if (data.byteLength === 0 || data.byteLength > (1 << 20)) {
    throw new Error("release manifest exceeds size limit");
  }
  let raw: unknown;
  try {
    raw = JSON.parse(new TextDecoder("utf-8", { fatal: true }).decode(data));
  } catch (error) {
    throw new Error(`release manifest is invalid JSON: ${asError(error).message}`);
  }
  const object = requireObject(raw, "release manifest");
  requireExactKeys(object, manifestKeys, "release manifest");
  const signature = requireObject(object["signature"], "release signature");
  requireExactKeys(signature, ["key_id", "value"], "release signature");
  const keyId = requireIdentifier(signature["key_id"], "signature key_id");
  const encodedSignature = requireString(signature["value"], "signature value");
  const keyStatements = decodeKeyStatements(object["key_statements"]);
  const keyring: Record<string, string> = { ...trustRoots };
  for (const statement of keyStatements) {
    const signer = keyring[statement.signedBy];
    if (signer === undefined || keyring[statement.keyId] !== undefined ||
      !verify(
        null,
        Buffer.from(canonicalJSON({
          key_id: statement.keyId,
          public_key: statement.publicKey,
          signed_by: statement.signedBy,
        }), "utf8"),
        createPublicKey(signer),
        Buffer.from(statement.signature, "base64"),
      )) {
      throw new Error("release key rotation statement is invalid");
    }
    keyring[statement.keyId] = statement.publicKey;
  }
  const publicKey = keyring[keyId];
  if (publicKey === undefined) {
    throw new Error(`release manifest signing key ${keyId} is not trusted`);
  }
  const unsigned = { ...object };
  delete unsigned["signature"];
  let signatureBytes: Buffer;
  try {
    signatureBytes = Buffer.from(encodedSignature, "base64");
  } catch (error) {
    throw new Error(`release signature is invalid: ${asError(error).message}`);
  }
  if (signatureBytes.byteLength !== 64 ||
    !verify(
      null,
      Buffer.from(canonicalJSON(unsigned), "utf8"),
      createPublicKey(publicKey),
      signatureBytes,
    )) {
    throw new Error("release manifest signature verification failed");
  }
  const artifacts = requireArray(object["artifacts"], "artifacts")
    .map((value, index) => decodeArtifact(value, index));
  if (artifacts.length === 0 || artifacts.length > 16) {
    throw new Error("release manifest artifact count is invalid");
  }
  const sequence = requirePositiveInteger(object["sequence"], "sequence");
  const channel = object["channel"];
  if (channel !== "stable" && channel !== "preview") {
    throw new Error("release manifest channel is invalid");
  }
  return {
    schemaVersion: requireOne(object["schema_version"], "schema_version"),
    manifestVersion: requireOne(object["manifest_version"], "manifest_version"),
    channel,
    sequence,
    generatedAt: requireTimestamp(object["generated_at"], "generated_at"),
    artifacts,
    revokedVersions: decodeStringSet(
      object["revoked_versions"], "revoked_versions", false,
    ),
    revokedDigests: decodeStringSet(
      object["revoked_digests"], "revoked_digests", true,
    ),
    keyStatements,
    signature: { keyId, value: encodedSignature },
  };
}

function decodeKeyStatements(value: unknown): readonly KeyStatement[] {
  const statements = requireArray(value, "key_statements");
  if (statements.length > 8) {
    throw new Error("release key rotation statements are excessive");
  }
  return statements.map((item, index) => {
    const name = `key_statements[${String(index)}]`;
    const object = requireObject(item, name);
    requireExactKeys(
      object,
      ["key_id", "public_key", "signed_by", "signature"],
      name,
    );
    const publicKey = object["public_key"];
    if (typeof publicKey !== "string" ||
      !publicKey.startsWith("-----BEGIN PUBLIC KEY-----\n") ||
      !publicKey.endsWith("-----END PUBLIC KEY-----\n") ||
      publicKey.length > 4096) {
      throw new Error(`${name}.public_key is invalid`);
    }
    const signature = requireString(object["signature"], `${name}.signature`);
    if (Buffer.from(signature, "base64").byteLength !== 64) {
      throw new Error(`${name}.signature is invalid`);
    }
    return {
      keyId: requireIdentifier(object["key_id"], `${name}.key_id`),
      publicKey,
      signedBy: requireIdentifier(object["signed_by"], `${name}.signed_by`),
      signature,
    };
  });
}

export function signedManifestPayload(value: unknown): string {
  const object = requireObject(value, "release manifest");
  const unsigned = { ...object };
  delete unsigned["signature"];
  return canonicalJSON(unsigned);
}

export function canonicalJSON(value: unknown): string {
  if (value === null || typeof value === "boolean" ||
    typeof value === "string") {
    return JSON.stringify(value);
  }
  if (typeof value === "number") {
    if (!Number.isSafeInteger(value)) {
      throw new Error("canonical JSON accepts only safe integers");
    }
    return String(value);
  }
  if (Array.isArray(value)) {
    return `[${value.map((item) => canonicalJSON(item)).join(",")}]`;
  }
  if (typeof value === "object") {
    const object = value as Readonly<Record<string, unknown>>;
    return `{${Object.keys(object).sort().map((key) =>
      `${JSON.stringify(key)}:${canonicalJSON(object[key])}`).join(",")}}`;
  }
  throw new Error("canonical JSON contains an unsupported value");
}

export function selectArtifact(
  manifest: ReleaseManifest,
  platform = process.platform,
  architecture = process.arch,
): ReleaseArtifact {
  const os = platform === "win32" ? "windows" : platform;
  const arch = architecture === "x64" ? "amd64" : architecture;
  const artifact = manifest.artifacts.find(
    (candidate) => candidate.target.os === os && candidate.target.arch === arch,
  );
  if (artifact === undefined) {
    throw new Error(`release manifest has no artifact for ${os}/${arch}`);
  }
  if (manifest.revokedVersions.includes(artifact.version) ||
    manifest.revokedDigests.includes(artifact.sha256)) {
    throw new Error("release artifact is revoked");
  }
  return artifact;
}

function decodeArtifact(value: unknown, index: number): ReleaseArtifact {
  const name = `artifacts[${String(index)}]`;
  const object = requireObject(value, name);
  requireExactKeys(object, artifactKeys, name);
  const target = requireObject(object["target"], `${name}.target`);
  requireExactKeys(target, ["os", "arch"], `${name}.target`);
  const acp = requireObject(object["acp_protocol"], `${name}.acp_protocol`);
  requireExactKeys(acp, ["min", "max"], `${name}.acp_protocol`);
  const minimum = requirePositiveInteger(acp["min"], `${name}.acp_protocol.min`);
  const maximum = requirePositiveInteger(acp["max"], `${name}.acp_protocol.max`);
  if (minimum > maximum) {
    throw new Error(`${name}.acp_protocol range is invalid`);
  }
  const url = requireString(object["url"], `${name}.url`);
  const parsedURL = new URL(url);
  if (parsedURL.protocol !== "https:" || parsedURL.username !== "" ||
    parsedURL.password !== "" || parsedURL.hash !== "") {
    throw new Error(`${name}.url must be an HTTPS artifact URL`);
  }
  return {
    version: requireSemver(object["version"], `${name}.version`),
    commit: requireIdentifier(object["commit"], `${name}.commit`),
    buildTime: requireTimestamp(object["build_time"], `${name}.build_time`),
    target: {
      os: requireIdentifier(target["os"], `${name}.target.os`),
      arch: requireIdentifier(target["arch"], `${name}.target.arch`),
    },
    bytes: requirePositiveInteger(object["bytes"], `${name}.bytes`),
    sha256: requireDigest(object["sha256"], `${name}.sha256`),
    url,
    acpProtocolMin: minimum,
    acpProtocolMax: maximum,
    operationSchemaVersion: requirePositiveInteger(
      object["operation_schema_version"],
      `${name}.operation_schema_version`,
    ),
    requiredFeatures: decodeStringSet(
      object["required_features"], `${name}.required_features`, false,
    ),
    extensionVersionRange: requireString(
      object["extension_version_range"],
      `${name}.extension_version_range`,
    ),
    sbomSha256: requireDigest(
      object["sbom_sha256"], `${name}.sbom_sha256`,
    ),
    provenanceSha256: requireDigest(
      object["provenance_sha256"], `${name}.provenance_sha256`,
    ),
  };
}

function decodeStringSet(
  value: unknown,
  name: string,
  digest: boolean,
): readonly string[] {
  const values = requireArray(value, name).map((item, index) =>
    digest
      ? requireDigest(item, `${name}[${String(index)}]`)
      : requireString(item, `${name}[${String(index)}]`));
  if (values.length > 128 || new Set(values).size !== values.length) {
    throw new Error(`${name} is excessive or contains duplicates`);
  }
  return values;
}

function requireExactKeys(
  object: Readonly<Record<string, unknown>>,
  keys: readonly string[],
  name: string,
): void {
  const actual = Object.keys(object).sort();
  const expected = [...keys].sort();
  if (actual.length !== expected.length ||
    actual.some((key, index) => key !== expected[index])) {
    throw new Error(`${name} contains missing or unknown fields`);
  }
}

function requireObject(
  value: unknown,
  name: string,
): Record<string, unknown> {
  if (typeof value !== "object" || value === null || Array.isArray(value)) {
    throw new Error(`${name} must be an object`);
  }
  return value as Record<string, unknown>;
}

function requireArray(value: unknown, name: string): readonly unknown[] {
  if (!Array.isArray(value)) throw new Error(`${name} must be an array`);
  return value;
}

function requireString(value: unknown, name: string): string {
  if (typeof value !== "string" || value.length === 0 || value.length > 4096 ||
    value.includes("\0") || value.includes("\r") || value.includes("\n")) {
    throw new Error(`${name} is invalid`);
  }
  return value;
}

function requireIdentifier(value: unknown, name: string): string {
  const result = requireString(value, name);
  if (!identifierPattern.test(result)) throw new Error(`${name} is invalid`);
  return result;
}

function requireDigest(value: unknown, name: string): string {
  const result = requireString(value, name);
  if (!digestPattern.test(result)) throw new Error(`${name} is invalid`);
  return result;
}

function requireSemver(value: unknown, name: string): string {
  const result = requireString(value, name);
  if (!/^[0-9]+\.[0-9]+\.[0-9]+$/u.test(result)) {
    throw new Error(`${name} must be strict SemVer`);
  }
  return result;
}

function requireTimestamp(value: unknown, name: string): string {
  const result = requireString(value, name);
  if (!/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?Z$/u.test(result) ||
    Number.isNaN(Date.parse(result))) {
    throw new Error(`${name} must be an RFC3339 UTC timestamp`);
  }
  return result;
}

function requirePositiveInteger(value: unknown, name: string): number {
  if (!Number.isSafeInteger(value) || (value as number) <= 0) {
    throw new Error(`${name} must be a positive integer`);
  }
  return value as number;
}

function requireOne(value: unknown, name: string): 1 {
  if (value !== 1) throw new Error(`${name} must be 1`);
  return 1;
}

function asError(value: unknown): Error {
  return value instanceof Error ? value : new Error(String(value));
}
