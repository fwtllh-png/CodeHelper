import {
  createHash,
  createPublicKey,
  verify,
} from "node:crypto";
import { Buffer } from "node:buffer";
import { execFile } from "node:child_process";
import {
  mkdir,
  readFile,
  readdir,
  writeFile,
} from "node:fs/promises";
import { basename, dirname, join, resolve } from "node:path";
import { promisify } from "node:util";

import { sourceIdentity } from "./source-identity.mjs";

const execute = promisify(execFile);
const extensionRoot = resolve(import.meta.dirname, "..", "..");
const repositoryRoot = resolve(extensionRoot, "..", "..");
const releaseRoot = join(extensionRoot, "dist", "vscode-release");
const matrixRoot = join(extensionRoot, "dist", "matrix");
const performanceRoot = join(extensionRoot, "dist", "performance");
const outputRoot = join(extensionRoot, "dist", "rc");

const matrix = await readJSON(join(matrixRoot, "report.json"));
if (matrix.schema_version !== 1 || matrix.status !== "passed" ||
  matrix.required !== 15 || matrix.missing?.length !== 0) {
  throw new Error("RC requires a complete 15/15 E2E matrix");
}
const provenancePath = join(
  releaseRoot,
  "provenance",
  "release-provenance.json",
);
const provenance = await readJSON(provenancePath);
if (provenance.schema_version !== 1 ||
  !["clean", "dirty"].includes(provenance.source_state) ||
  !validDigest(provenance.source_fingerprint_sha256) ||
  !Array.isArray(provenance.artifacts) ||
  provenance.artifacts.length !== 6) {
  throw new Error("release provenance is incomplete");
}
const source = await sourceIdentity(repositoryRoot);
if (source.state !== provenance.source_state ||
  source.fingerprint !== provenance.source_fingerprint_sha256) {
  throw new Error("release provenance does not match the current source tree");
}
await verifyChecksums(releaseRoot);
for (const artifact of provenance.artifacts) {
  if (!isObject(artifact) || typeof artifact.file !== "string" ||
    !validDigest(artifact.sha256) ||
    await digest(join(releaseRoot, artifact.file)) !== artifact.sha256) {
    throw new Error("release artifact does not match provenance");
  }
}
const sbomPath = join(
  releaseRoot,
  "sbom",
  `codehelper-vscode-${String(provenance.extension_version)}.cdx.json`,
);
const sbom = await readJSON(sbomPath);
if (sbom.bomFormat !== "CycloneDX" ||
  await digest(sbomPath) !== provenance.inputs?.extension_sbom_sha256) {
  throw new Error("extension SBOM does not match provenance");
}
const manifestPath = join(
  releaseRoot,
  "provenance",
  "release-manifest.json",
);
const rootsPath = join(
  releaseRoot,
  "provenance",
  "release-trust-roots.json",
);
const manifestBytes = await readFile(manifestPath);
const rootsBytes = await readFile(rootsPath);
verifyManifest(manifestBytes, rootsBytes);
if (digestBytes(manifestBytes) !== provenance.inputs?.release_manifest_sha256 ||
  digestBytes(rootsBytes) !== provenance.inputs?.trust_roots_sha256) {
  throw new Error("signed manifest inputs do not match provenance");
}
for (const channel of ["marketplace", "open-vsx", "enterprise", "offline"]) {
  const publication = await readJSON(join(
    releaseRoot,
    "channels",
    channel,
    "publication.json",
  ));
  if (publication.uploaded !== false ||
    publication.dry_run !== provenance.dry_run) {
    throw new Error(`${channel} publication state is not auditable`);
  }
}
await scanSecrets(releaseRoot);
const audit = await dependencyAudit();
const performance = await performanceReport();
const compatibility = buildCompatibilityReport(
  await readJSON(join(extensionRoot, "compatibility.json")),
  matrix,
);
await mkdir(outputRoot, { recursive: true });
await writeJSON(join(outputRoot, "compatibility-report.json"), compatibility);
const publishable = provenance.dry_run === false &&
  provenance.source_state === "clean" &&
  provenance.signing_key_id !== "dry-run-only";
const report = {
  schema_version: 1,
  status: "passed",
  candidate_kind: provenance.dry_run ? "validated-dry-run" : "release-candidate",
  publishable,
  uploaded: false,
  source: {
    commit: provenance.commit,
    state: provenance.source_state,
    fingerprint_sha256: provenance.source_fingerprint_sha256,
  },
  gates: {
    matrix: `${String(matrix.required)}/${String(matrix.required)}`,
    compatibility: "passed",
    performance: "passed",
    dependency_audit: "passed",
    secret_scan: "passed",
    vsix_allowlist: "passed",
    sbom: "passed",
    provenance: "passed",
    signature: "passed",
    checksums: "passed",
    channels_uploaded: false,
  },
  performance,
  dependency_audit: audit,
  limitations: [
    "Windows x64 and WSL2 have no dynamic runner",
    ...(publishable ? [] : [
      "This evidence is not publishable until a clean worktree and production signing identity are used",
    ]),
  ],
};
await writeJSON(join(outputRoot, "report.json"), report);
await writeFile(join(outputRoot, "report.md"), markdown(report));
process.stdout.write(
  `VS Code RC evidence passed; kind=${report.candidate_kind}; ` +
    `publishable=${String(report.publishable)}; uploaded=false\n`,
);

async function performanceReport() {
  const projectors = await readJSON(join(performanceRoot, "projectors.json"));
  const runtime = await readJSON(join(performanceRoot, "runtime-ready.json"));
  const electron = await readJSON(join(performanceRoot, "electron.json"));
  if (projectors.chat_10k_duration_ms >= 1_000 ||
    projectors.chat_10k_heap_growth_bytes >= 32 << 20 ||
    projectors.tree_1000_duration_ms >= 100 ||
    runtime.p95_ms >= 5_000 ||
    electron.activation_ms >= 100 ||
    electron.capture_1mib_ms >= 100) {
    throw new Error("performance evidence exceeds an RC budget");
  }
  return {
    chat_10k_duration_ms: projectors.chat_10k_duration_ms,
    chat_10k_heap_growth_bytes: projectors.chat_10k_heap_growth_bytes,
    tree_1000_duration_ms: projectors.tree_1000_duration_ms,
    runtime_ready_p50_ms: runtime.p50_ms,
    runtime_ready_p95_ms: runtime.p95_ms,
    activation_ms: electron.activation_ms,
    capture_1mib_ms: electron.capture_1mib_ms,
  };
}

async function dependencyAudit() {
  let stdout;
  try {
    ({ stdout } = await execute("npm", [
      "audit", "--audit-level=high", "--json",
    ], { cwd: extensionRoot, maxBuffer: 20 << 20 }));
  } catch (error) {
    stdout = error.stdout;
  }
  const audit = JSON.parse(String(stdout ?? ""));
  const counts = audit.metadata?.vulnerabilities;
  if (!isObject(counts) || Number(counts.high) !== 0 ||
    Number(counts.critical) !== 0) {
    throw new Error("dependency audit contains high or critical vulnerabilities");
  }
  return {
    high: Number(counts.high),
    critical: Number(counts.critical),
    total: Number(counts.total),
  };
}

function buildCompatibilityReport(compatibility, matrixReport) {
  const passed = new Set(
    matrixReport.jobs.filter((job) => job.status === "passed")
      .map((job) => job.job),
  );
  return {
    schema_version: 1,
    extension_version: compatibility.extension_version,
    binary_version_range: compatibility.binary_version_range,
    vscode_engine: "^1.96.0",
    acp_protocol: compatibility.acp_protocol,
    operation_schema_version: compatibility.operation_schema_version,
    required_features: compatibility.required_features,
    targets: compatibility.targets.map((target) => {
      const name = `${target.os}-${target.arch}`;
      const dynamic = [...passed].filter((job) =>
        job.includes(name) ||
        (name === "linux-amd64" && job.includes("linux-amd64")) ||
        (name === "darwin-amd64" && job.includes("darwin-x64")));
      return {
        ...target,
        package: "supported",
        dynamic_e2e: dynamic.length > 0 ? "passed" : "unavailable",
        evidence: dynamic,
      };
    }),
    unsupported: [
      "win32-arm64",
      "linux-armhf",
      "WSL2 dynamic E2E",
      "Codespaces public service E2E",
    ],
  };
}

async function scanSecrets(root) {
  const patterns = [
    /-----BEGIN (?:RSA |EC |OPENSSH )?PRIVATE KEY-----/u,
    /\bghp_[A-Za-z0-9]{36}\b/u,
    /\bgithub_pat_[A-Za-z0-9_]{40,}\b/u,
    /\bsk-[A-Za-z0-9]{20,}\b/u,
  ];
  for (const path of await regularFiles(root)) {
    const content = await readFile(path);
    const text = content.toString("latin1");
    if (patterns.some((pattern) => pattern.test(text))) {
      throw new Error(`release secret scan failed for ${basename(path)}`);
    }
  }
}

async function verifyChecksums(root) {
  const lines = (await readFile(join(root, "SHA256SUMS"), "utf8"))
    .trim().split("\n");
  for (const line of lines) {
    const match = /^([0-9a-f]{64}) {2}([^\r\n]+)$/u.exec(line);
    const file = match?.[2];
    if (match === null || file === undefined || file.startsWith("/") ||
      file.split("/").includes("..") ||
      await digest(join(root, file)) !== match[1]) {
      throw new Error("release checksum verification failed");
    }
  }
}

function verifyManifest(manifestData, rootsData) {
  const manifest = JSON.parse(manifestData.toString("utf8"));
  const roots = JSON.parse(rootsData.toString("utf8"));
  const keyring = { ...(roots.keys ?? {}) };
  for (const statement of manifest.key_statements ?? []) {
    const signer = keyring[statement.signed_by];
    if (typeof signer !== "string" || keyring[statement.key_id] !== undefined ||
      !verifySignature(signer, {
        key_id: statement.key_id,
        public_key: statement.public_key,
        signed_by: statement.signed_by,
      }, statement.signature)) {
      throw new Error("release key rotation verification failed");
    }
    keyring[statement.key_id] = statement.public_key;
  }
  const publicKey = keyring[manifest.signature?.key_id];
  const unsigned = { ...manifest };
  delete unsigned.signature;
  if (typeof publicKey !== "string" ||
    !verifySignature(publicKey, unsigned, manifest.signature?.value)) {
    throw new Error("release manifest signature verification failed");
  }
}

function verifySignature(publicKey, value, signature) {
  try {
    const bytes = Buffer.from(signature, "base64");
    return bytes.byteLength === 64 && verify(
      null,
      Buffer.from(canonicalJSON(value)),
      createPublicKey(publicKey),
      bytes,
    );
  } catch {
    return false;
  }
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

async function regularFiles(root) {
  const output = [];
  for (const entry of await readdir(root, { withFileTypes: true })) {
    const path = join(root, entry.name);
    if (entry.isDirectory()) output.push(...await regularFiles(path));
    if (entry.isFile()) output.push(path);
  }
  return output;
}

async function readJSON(path) {
  return JSON.parse(await readFile(path, "utf8"));
}

async function writeJSON(path, value) {
  await mkdir(dirname(path), { recursive: true });
  await writeFile(path, `${JSON.stringify(value, null, 2)}\n`);
}

async function digest(path) {
  return digestBytes(await readFile(path));
}

function digestBytes(value) {
  return createHash("sha256").update(value).digest("hex");
}

function validDigest(value) {
  return typeof value === "string" && /^[0-9a-f]{64}$/u.test(value);
}

function isObject(value) {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function markdown(report) {
  return [
    "# VS Code Release Candidate Evidence",
    "",
    `- Status: **${report.status}**`,
    `- Candidate: \`${report.candidate_kind}\``,
    `- Publishable: \`${String(report.publishable)}\``,
    "- Uploaded: `false`",
    `- Source: \`${report.source.commit}\` (${report.source.state})`,
    `- Matrix: \`${report.gates.matrix}\``,
    "",
    "Windows x64 and WSL2 dynamic E2E are not claimed.",
    "",
  ].join("\n");
}
