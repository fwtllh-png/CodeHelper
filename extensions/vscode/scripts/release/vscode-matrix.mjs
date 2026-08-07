import {
  createHash,
  createPublicKey,
  verify,
} from "node:crypto";
import { Buffer } from "node:buffer";
import {
  chmod,
  copyFile,
  lstat,
  mkdir,
  mkdtemp,
  realpath,
  readFile,
  readdir,
  rm,
  writeFile,
} from "node:fs/promises";
import { tmpdir } from "node:os";
import { basename, dirname, join, resolve } from "node:path";
import { createVSIX, listFiles } from "@vscode/vsce";

const extensionRoot = resolve(import.meta.dirname, "..", "..");
const releaseRoot = resolve(requiredEnvironment("CODEHELPER_BINARY_RELEASE_ROOT"));
const outputRoot = resolve(
  process.env["CODEHELPER_VSCODE_RELEASE_OUTPUT"] ??
    join(extensionRoot, "dist", "vscode-release"),
);
const sourceTrustRoots = resolve(
  process.env["CODEHELPER_RELEASE_TRUST_ROOTS"] ??
    join(extensionRoot, "resources", "release-trust-roots.json"),
);
const manifestPath = join(releaseRoot, "release-manifest.json");
const manifestBytes = await readFile(manifestPath);
const trustRootBytes = await readFile(sourceTrustRoots);
const manifest = verifyReleaseManifest(manifestBytes, trustRootBytes);
const packageJSON = JSON.parse(
  await readFile(join(extensionRoot, "package.json"), "utf8"),
);
const compatibilityBytes = await readFile(
  join(extensionRoot, "compatibility.json"),
);
const channelsBytes = await readFile(
  join(import.meta.dirname, "channels.json"),
);
const version = requireString(packageJSON.version, "extension version");
const publisher = requireString(packageJSON.publisher, "extension publisher");
const sourceState = requireSourceState(
  process.env["CODEHELPER_RELEASE_SOURCE_STATE"],
);
const sourceFingerprint = requireDigest(
  process.env["CODEHELPER_RELEASE_SOURCE_FINGERPRINT"],
  "source fingerprint",
);
const targets = [
  { os: "linux", arch: "amd64", vscode: "linux-x64", executable: "codehelper" },
  { os: "linux", arch: "arm64", vscode: "linux-arm64", executable: "codehelper" },
  { os: "darwin", arch: "amd64", vscode: "darwin-x64", executable: "codehelper" },
  { os: "darwin", arch: "arm64", vscode: "darwin-arm64", executable: "codehelper" },
  { os: "windows", arch: "amd64", vscode: "win32-x64", executable: "codehelper.exe" },
];
const artifacts = new Map();
for (const target of targets) {
  const artifact = manifest.artifacts.find(
    (candidate) =>
      candidate.target.os === target.os && candidate.target.arch === target.arch,
  );
  if (artifact === undefined) {
    throw new Error(`signed manifest has no ${target.os}/${target.arch} artifact`);
  }
  if (manifest.revoked_versions.includes(artifact.version) ||
    manifest.revoked_digests.includes(artifact.sha256)) {
    throw new Error(`signed manifest artifact ${target.vscode} is revoked`);
  }
  artifacts.set(target.vscode, {
    metadata: artifact,
    path: await findDigestFile(releaseRoot, artifact.sha256, artifact.bytes),
  });
}

await rm(outputRoot, { recursive: true, force: true });
await mkdir(join(outputRoot, "artifacts"), { recursive: true });
await mkdir(join(outputRoot, "sbom"), { recursive: true });
await mkdir(join(outputRoot, "provenance"), { recursive: true });
await copyFile(
  manifestPath,
  join(outputRoot, "provenance", "release-manifest.json"),
);
await copyFile(
  sourceTrustRoots,
  join(outputRoot, "provenance", "release-trust-roots.json"),
);
const stagingRoot = await realpath(
  await mkdtemp(join(tmpdir(), "codehelper-vscode-release-")),
);
const packaged = [];
try {
  const universal = await packageVariant({
    stage: join(stagingRoot, "universal"),
    output: join(
      outputRoot,
      "artifacts",
      `codehelper-vscode-${version}-universal.vsix`,
    ),
  });
  packaged.push({
    kind: "universal",
    target: "universal",
    ...universal,
  });
  for (const target of targets) {
    const artifact = artifacts.get(target.vscode);
    if (artifact === undefined) throw new Error("internal target lookup failed");
    const stage = join(stagingRoot, target.vscode);
    await stageExtension(stage);
    const binaryPath = join(stage, "bin", target.vscode, target.executable);
    await mkdir(dirname(binaryPath), { recursive: true, mode: 0o700 });
    await copyFile(artifact.path, binaryPath);
    if (target.os !== "windows") await chmod(binaryPath, 0o500);
    await copyFile(manifestPath, join(stage, "bin", "release-manifest.json"));
    const result = await packageVariant({
      stage,
      output: join(
        outputRoot,
        "artifacts",
        `codehelper-vscode-${version}-${target.vscode}.vsix`,
      ),
      target: target.vscode,
      expectedBinary: `bin/${target.vscode}/${target.executable}`,
    });
    packaged.push({
      kind: "bundled",
      target: target.vscode,
      binary_sha256: artifact.metadata.sha256,
      binary_version: artifact.metadata.version,
      ...result,
    });
  }
} finally {
  await rm(stagingRoot, { recursive: true, force: true });
}

const extensionSBOM = buildExtensionSBOM(
  packageJSON,
  JSON.parse(await readFile(join(extensionRoot, "package-lock.json"), "utf8")),
);
const sbomPath = join(
  outputRoot,
  "sbom",
  `codehelper-vscode-${version}.cdx.json`,
);
await writeJSON(sbomPath, extensionSBOM);
const sbomDigest = await fileDigest(sbomPath);
const provenance = {
  schema_version: 1,
  product: "codehelper-vscode",
  extension_version: version,
  publisher,
  dry_run: process.env["CODEHELPER_RELEASE_DRY_RUN"] === "1",
  signing_key_id: process.env["CODEHELPER_RELEASE_KEY_ID"] ?? "unknown",
  commit: process.env["CODEHELPER_RELEASE_COMMIT"] ?? "unknown",
  source_state: sourceState,
  source_fingerprint_sha256: sourceFingerprint,
  built_at: process.env["CODEHELPER_RELEASE_BUILT_AT"] ??
    new Date().toISOString().replace(/\.\d{3}Z$/u, "Z"),
  inputs: {
    compatibility_sha256: sha256(compatibilityBytes),
    release_manifest_sha256: sha256(manifestBytes),
    trust_roots_sha256: sha256(trustRootBytes),
    channel_mapping_sha256: sha256(channelsBytes),
    extension_sbom_sha256: sbomDigest,
    release_sequence: manifest.sequence,
    release_channel: manifest.channel,
  },
  artifacts: packaged,
};
await writeJSON(
  join(outputRoot, "provenance", "release-provenance.json"),
  provenance,
);
await writeChecksums(outputRoot);
process.stdout.write(`${outputRoot}\n`);

async function packageVariant(options) {
  if (options.target === undefined) await stageExtension(options.stage);
  const files = await listFiles({
    cwd: options.stage,
    packagedDependencies: [],
  });
  auditFiles(files, options.expectedBinary);
  await createVSIX({
    cwd: options.stage,
    packagePath: options.output,
    dependencies: false,
    allowMissingRepository: false,
    ...(options.target === undefined ? {} : {
      target: options.target,
      ignoreOtherTargetFolders: true,
    }),
  });
  return {
    file: `artifacts/${basename(options.output)}`,
    sha256: await fileDigest(options.output),
    bytes: (await lstat(options.output)).size,
    files: files.length,
  };
}

async function stageExtension(stage) {
  await mkdir(stage, { recursive: true, mode: 0o700 });
  for (const file of [
    "compatibility.json",
    "README.md",
    "CHANGELOG.md",
    "SECURITY.md",
    "PRIVACY.md",
    "SUPPORT.md",
    "LICENSE",
  ]) {
    await copyFile(join(extensionRoot, file), join(stage, file));
  }
  await mkdir(join(stage, "dist"), { recursive: true });
  await copyFile(
    join(extensionRoot, "dist", "extension.js"),
    join(stage, "dist", "extension.js"),
  );
  await mkdir(join(stage, "media"), { recursive: true });
  await copyFile(
    join(extensionRoot, "media", "codehelper.svg"),
    join(stage, "media", "codehelper.svg"),
  );
  await copyFile(
    join(extensionRoot, "media", "codehelper.png"),
    join(stage, "media", "codehelper.png"),
  );
  await mkdir(join(stage, "resources"), { recursive: true });
  await copyFile(
    sourceTrustRoots,
    join(stage, "resources", "release-trust-roots.json"),
  );
  const releasePackage = { ...packageJSON };
  delete releasePackage.private;
  await writeJSON(join(stage, "package.json"), releasePackage);
  await writeFile(
    join(stage, ".vscodeignore"),
    [
      "src/**",
      "scripts/**",
      "node_modules/**",
      "dist/**/*.map",
      ".vscodeignore",
      "",
    ].join("\n"),
  );
}

function auditFiles(files, expectedBinary) {
  const forbidden = files.filter((file) =>
    file.startsWith("src/") ||
    file.startsWith("scripts/") ||
    file.startsWith("node_modules/") ||
    file.endsWith(".map") ||
    file.includes(".env") ||
    file.includes("PRIVATE"),
  );
  if (forbidden.length > 0) {
    throw new Error(`VSIX contains forbidden files: ${forbidden.join(", ")}`);
  }
  for (const required of [
    "package.json",
    "compatibility.json",
    "resources/release-trust-roots.json",
    "dist/extension.js",
    "dist/chat-webview.js",
    "dist/chat-webview.css",
    "media/codehelper.svg",
    "media/codehelper.png",
    "README.md",
    "CHANGELOG.md",
    "SECURITY.md",
    "PRIVACY.md",
    "SUPPORT.md",
    "LICENSE",
  ]) {
    if (!files.includes(required)) {
      throw new Error(
        `VSIX is missing ${required}; files=${JSON.stringify(files)}`,
      );
    }
  }
  const binaries = files.filter((file) => file.startsWith("bin/") &&
    file !== "bin/release-manifest.json");
  if (expectedBinary === undefined) {
    if (binaries.length !== 0 || files.includes("bin/release-manifest.json")) {
      throw new Error("universal VSIX contains a bundled binary");
    }
  } else if (binaries.length !== 1 || binaries[0] !== expectedBinary ||
    !files.includes("bin/release-manifest.json")) {
    throw new Error("target VSIX does not contain exactly its target binary");
  }
}

async function findDigestFile(root, digest, bytes) {
  for (const path of await regularFiles(root)) {
    const info = await lstat(path);
    if (info.size === bytes && await fileDigest(path) === digest) return path;
  }
  throw new Error(`release artifact ${digest} is absent or corrupt`);
}

async function regularFiles(root) {
  const output = [];
  for (const entry of await readdir(root, { withFileTypes: true })) {
    const path = join(root, entry.name);
    if (entry.isSymbolicLink()) {
      throw new Error(`release root contains symlink ${entry.name}`);
    }
    if (entry.isDirectory()) output.push(...await regularFiles(path));
    if (entry.isFile()) output.push(path);
  }
  return output;
}

function verifyReleaseManifest(manifestData, rootData) {
  if (manifestData.byteLength > (1 << 20)) throw new Error("manifest too large");
  const manifest = JSON.parse(manifestData.toString("utf8"));
  const rootsDocument = JSON.parse(rootData.toString("utf8"));
  if (rootsDocument.schema_version !== 1 || !rootsDocument.keys) {
    throw new Error("trust roots are invalid");
  }
  const keyring = { ...rootsDocument.keys };
  for (const statement of manifest.key_statements ?? []) {
    const signer = keyring[statement.signed_by];
    const payload = {
      key_id: statement.key_id,
      public_key: statement.public_key,
      signed_by: statement.signed_by,
    };
    if (!signer || keyring[statement.key_id] ||
      !verifySignature(signer, payload, statement.signature)) {
      throw new Error("release key rotation is invalid");
    }
    keyring[statement.key_id] = statement.public_key;
  }
  const signature = manifest.signature;
  const publicKey = signature && keyring[signature.key_id];
  const unsigned = { ...manifest };
  delete unsigned.signature;
  if (!publicKey || !verifySignature(publicKey, unsigned, signature.value)) {
    throw new Error("release manifest signature is invalid");
  }
  if (manifest.schema_version !== 1 || manifest.manifest_version !== 1 ||
    !Number.isSafeInteger(manifest.sequence) || manifest.sequence <= 0 ||
    !Array.isArray(manifest.artifacts) ||
    !Array.isArray(manifest.revoked_versions) ||
    !Array.isArray(manifest.revoked_digests)) {
    throw new Error("release manifest schema is invalid");
  }
  return manifest;
}

function verifySignature(publicKey, payload, signature) {
  try {
    const bytes = Buffer.from(signature, "base64");
    return bytes.byteLength === 64 && verify(
      null,
      Buffer.from(canonicalJSON(payload)),
      createPublicKey(publicKey),
      bytes,
    );
  } catch {
    return false;
  }
}

function buildExtensionSBOM(manifest, lock) {
  const components = [];
  for (const [path, value] of Object.entries(lock.packages ?? {})) {
    if (!path.startsWith("node_modules/") || !value.version) continue;
    components.push({
      type: "library",
      name: path.slice("node_modules/".length),
      version: value.version,
      purl: `pkg:npm/${encodeURIComponent(path.slice("node_modules/".length))}` +
        `@${value.version}`,
    });
  }
  components.sort((left, right) => left.name.localeCompare(right.name));
  return {
    bomFormat: "CycloneDX",
    specVersion: "1.5",
    version: 1,
    metadata: {
      component: {
        type: "application",
        name: manifest.name,
        version: manifest.version,
      },
    },
    components,
  };
}

async function writeChecksums(root) {
  const entries = [];
  for (const path of await regularFiles(root)) {
    if (basename(path) === "SHA256SUMS") continue;
    entries.push(`${await fileDigest(path)}  ${path.slice(root.length + 1)}`);
  }
  entries.sort();
  await writeFile(join(root, "SHA256SUMS"), `${entries.join("\n")}\n`, {
    mode: 0o600,
  });
}

async function writeJSON(path, value) {
  await mkdir(dirname(path), { recursive: true });
  await writeFile(path, `${JSON.stringify(value, null, 2)}\n`, { mode: 0o600 });
}

async function fileDigest(path) {
  return sha256(await readFile(path));
}

function sha256(value) {
  return createHash("sha256").update(value).digest("hex");
}

function canonicalJSON(value) {
  if (value === null || typeof value === "boolean" ||
    typeof value === "string") return JSON.stringify(value);
  if (typeof value === "number") {
    if (!Number.isSafeInteger(value)) throw new Error("unsafe JSON number");
    return String(value);
  }
  if (Array.isArray(value)) return `[${value.map(canonicalJSON).join(",")}]`;
  if (typeof value === "object") {
    return `{${Object.keys(value).sort().map((key) =>
      `${JSON.stringify(key)}:${canonicalJSON(value[key])}`).join(",")}}`;
  }
  throw new Error("unsupported canonical JSON value");
}

function requireString(value, name) {
  if (typeof value !== "string" || value.length === 0) {
    throw new Error(`${name} is invalid`);
  }
  return value;
}

function requireSourceState(value) {
  if (value !== "clean" && value !== "dirty") {
    throw new Error("release source state is invalid");
  }
  return value;
}

function requireDigest(value, name) {
  if (typeof value !== "string" || !/^[0-9a-f]{64}$/u.test(value)) {
    throw new Error(`${name} is invalid`);
  }
  return value;
}

function requiredEnvironment(name) {
  const value = process.env[name];
  if (!value) throw new Error(`${name} is required`);
  return value;
}
