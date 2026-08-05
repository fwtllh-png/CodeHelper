import { createHash, createPrivateKey, sign } from "node:crypto";
import { Buffer } from "node:buffer";
import { copyFile, mkdir, readFile, writeFile } from "node:fs/promises";
import { basename, join, relative, resolve } from "node:path";

const repositoryRoot = resolve(import.meta.dirname, "..", "..", "..", "..");
const specPath = requiredEnvironment("CODEHELPER_RELEASE_SPEC");
const privateKeyPath = resolve(requiredEnvironment(
  "CODEHELPER_RELEASE_PRIVATE_KEY",
));
const keyID = requiredEnvironment("CODEHELPER_RELEASE_KEY_ID");
const outputRoot = resolve(
  process.env["CODEHELPER_RELEASE_OUTPUT"] ??
    join(repositoryRoot, "dist", "binary-release"),
);
if (!relative(repositoryRoot, privateKeyPath).startsWith("..")) {
  throw new Error("release private key must not be stored in the repository");
}

const spec = JSON.parse(await readFile(specPath, "utf8"));
if (!spec || !Array.isArray(spec.artifacts) || spec.artifacts.length === 0) {
  throw new Error("binary release spec has no artifacts");
}
await mkdir(outputRoot, { recursive: true, mode: 0o700 });
const artifacts = [];
for (const source of spec.artifacts) {
  const binary = await readFile(source.path);
  const sbom = await readFile(source.sbom_path);
  const provenance = await readFile(source.provenance_path);
  const name = basename(source.path);
  await copyFile(source.path, join(outputRoot, name));
  await copyFile(source.sbom_path, join(outputRoot, basename(source.sbom_path)));
  await copyFile(
    source.provenance_path,
    join(outputRoot, basename(source.provenance_path)),
  );
  artifacts.push({
    version: source.version,
    commit: source.commit,
    build_time: source.build_time,
    target: source.target,
    bytes: binary.byteLength,
    sha256: sha256(binary),
    url: source.url,
    acp_protocol: source.acp_protocol,
    operation_schema_version: source.operation_schema_version,
    required_features: source.required_features,
    extension_version_range: source.extension_version_range,
    sbom_sha256: sha256(sbom),
    provenance_sha256: sha256(provenance),
  });
}
const manifest = {
  schema_version: 1,
  manifest_version: 1,
  channel: spec.channel,
  sequence: spec.sequence,
  generated_at: spec.generated_at,
  artifacts,
  revoked_versions: spec.revoked_versions ?? [],
  revoked_digests: spec.revoked_digests ?? [],
  key_statements: spec.key_statements ?? [],
};
const privateKey = createPrivateKey(await readFile(privateKeyPath));
const signature = sign(
  null,
  Buffer.from(canonicalJSON(manifest)),
  privateKey,
).toString("base64");
const signed = {
  ...manifest,
  signature: { key_id: keyID, value: signature },
};
await writeFile(
  join(outputRoot, "release-manifest.json"),
  `${JSON.stringify(signed, null, 2)}\n`,
  { mode: 0o600 },
);
process.stdout.write(`${join(outputRoot, "release-manifest.json")}\n`);

function requiredEnvironment(name) {
  const value = process.env[name];
  if (!value) throw new Error(`${name} is required`);
  return value;
}

function sha256(value) {
  return createHash("sha256").update(value).digest("hex");
}

function canonicalJSON(value) {
  if (value === null || typeof value === "boolean" ||
    typeof value === "string") {
    return JSON.stringify(value);
  }
  if (typeof value === "number") {
    if (!Number.isSafeInteger(value)) throw new Error("unsafe manifest number");
    return String(value);
  }
  if (Array.isArray(value)) {
    return `[${value.map(canonicalJSON).join(",")}]`;
  }
  if (typeof value === "object") {
    return `{${Object.keys(value).sort().map((key) =>
      `${JSON.stringify(key)}:${canonicalJSON(value[key])}`).join(",")}}`;
  }
  throw new Error("unsupported manifest value");
}
