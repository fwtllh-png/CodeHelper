import {
  createHash,
  createPrivateKey,
  createPublicKey,
  generateKeyPairSync,
} from "node:crypto";
import {
  chmod,
  mkdir,
  mkdtemp,
  readFile,
  rm,
  writeFile,
} from "node:fs/promises";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { promisify } from "node:util";
import { execFile } from "node:child_process";
import { sourceIdentity } from "./source-identity.mjs";

const execute = promisify(execFile);
const extensionRoot = resolve(import.meta.dirname, "..", "..");
const repositoryRoot = resolve(extensionRoot, "..", "..");
const packageJSON = JSON.parse(
  await readFile(join(extensionRoot, "package.json"), "utf8"),
);
const compatibility = JSON.parse(
  await readFile(join(extensionRoot, "compatibility.json"), "utf8"),
);
const version = process.env["CODEHELPER_RELEASE_VERSION"] ?? packageJSON.version;
if (!/^\d+\.\d+\.\d+$/u.test(version)) {
  throw new Error("CODEHELPER_RELEASE_VERSION must be strict SemVer");
}
const sequence = Number(process.env["CODEHELPER_RELEASE_SEQUENCE"] ?? "1");
if (!Number.isSafeInteger(sequence) || sequence <= 0) {
  throw new Error("CODEHELPER_RELEASE_SEQUENCE must be a positive integer");
}
const dryRun = process.env["CODEHELPER_RELEASE_DRY_RUN"] === "1";
const temporaryRoot = await mkdtemp(join(tmpdir(), "codehelper-release-"));
const binaryRoot = resolve(
  process.env["CODEHELPER_BINARY_RELEASE_ROOT"] ??
    join(extensionRoot, "dist", "binary-release"),
);
const outputRoot = resolve(
  process.env["CODEHELPER_VSCODE_RELEASE_OUTPUT"] ??
    join(extensionRoot, "dist", "vscode-release"),
);
let privateKeyPath = process.env["CODEHELPER_RELEASE_PRIVATE_KEY"];
let trustRootsPath = process.env["CODEHELPER_RELEASE_TRUST_ROOTS"];
let keyID = process.env["CODEHELPER_RELEASE_KEY_ID"];
try {
  if (dryRun && (privateKeyPath === undefined ||
    trustRootsPath === undefined || keyID === undefined)) {
    const pair = generateKeyPairSync("ed25519");
    privateKeyPath = join(temporaryRoot, "dry-run-private.pem");
    trustRootsPath = join(temporaryRoot, "dry-run-trust-roots.json");
    keyID = "dry-run-only";
    await writeFile(
      privateKeyPath,
      pair.privateKey.export({ type: "pkcs8", format: "pem" }),
      { mode: 0o600 },
    );
    await writeJSON(trustRootsPath, {
      schema_version: 1,
      keys: {
        [keyID]: pair.publicKey.export({
          type: "spki",
          format: "pem",
        }).toString(),
      },
    });
  }
  if (privateKeyPath === undefined ||
    trustRootsPath === undefined || keyID === undefined) {
    throw new Error(
      "production release requires CODEHELPER_RELEASE_PRIVATE_KEY, " +
      "CODEHELPER_RELEASE_TRUST_ROOTS, and CODEHELPER_RELEASE_KEY_ID",
    );
  }
  await assertSigningIdentity(privateKeyPath, trustRootsPath, keyID);
  const commit = (process.env["CODEHELPER_RELEASE_COMMIT"] ??
    (await execute("git", ["rev-parse", "--short=12", "HEAD"], {
      cwd: repositoryRoot,
    })).stdout.trim());
  const source = await sourceIdentity(repositoryRoot);
  if (!dryRun && source.state !== "clean") {
    throw new Error("production release requires a clean source worktree");
  }
  const builtAt = process.env["CODEHELPER_RELEASE_BUILT_AT"] ??
    new Date().toISOString().replace(/\.\d{3}Z$/u, "Z");
  const buildRoot = join(temporaryRoot, "build");
  await mkdir(buildRoot, { recursive: true });
  const sbomPath = join(temporaryRoot, `codehelper-${version}.cdx.json`);
  await writeJSON(sbomPath, await buildGoSBOM(version));
  const targets = [
    ["linux", "amd64"],
    ["linux", "arm64"],
    ["darwin", "amd64"],
    ["darwin", "arm64"],
    ["windows", "amd64"],
  ];
  const specArtifacts = [];
  for (const [os, arch] of targets) {
    const suffix = os === "windows" ? ".exe" : "";
    const name = `codehelper-${version}-${os}-${arch}${suffix}`;
    const binaryPath = join(buildRoot, name);
    await execute("go", [
      "build",
      "-trimpath",
      "-ldflags",
      [
        "-s", "-w",
        `-X github.com/fwtllh-png/CodeHelper/internal/buildinfo.Version=${version}`,
        `-X github.com/fwtllh-png/CodeHelper/internal/buildinfo.Commit=${commit}`,
        `-X github.com/fwtllh-png/CodeHelper/internal/buildinfo.Date=${builtAt}`,
      ].join(" "),
      "-o", binaryPath,
      "./cmd/codehelper",
    ], {
      cwd: repositoryRoot,
      env: {
        ...process.env,
        CGO_ENABLED: "0",
        GOOS: os,
        GOARCH: arch,
      },
      maxBuffer: 10 << 20,
    });
    if (os !== "windows") await chmod(binaryPath, 0o500);
    const provenancePath = join(
      temporaryRoot,
      `codehelper-${version}-${os}-${arch}.provenance.json`,
    );
    await writeJSON(provenancePath, {
      _type: "https://in-toto.io/Statement/v1",
      subject: [{
        name,
        digest: { sha256: await digest(binaryPath) },
      }],
      predicateType: "https://slsa.dev/provenance/v1",
      predicate: {
        buildDefinition: {
          buildType: "https://github.com/fwtllh-png/CodeHelper/vscode-binary@v1",
          externalParameters: { version, commit, os, arch },
          internalParameters: {
            source_state: source.state,
            source_fingerprint_sha256: source.fingerprint,
          },
        },
        runDetails: {
          builder: { id: "codehelper-release-ci" },
          metadata: { invocationId: `${commit}-${os}-${arch}` },
        },
      },
    });
    specArtifacts.push({
      path: binaryPath,
      url: `https://github.com/fwtllh-png/CodeHelper/releases/download/` +
        `v${version}/${name}`,
      version,
      commit,
      build_time: builtAt,
      target: { os, arch },
      sbom_path: sbomPath,
      provenance_path: provenancePath,
      acp_protocol: compatibility.acp_protocol,
      operation_schema_version: compatibility.operation_schema_version,
      required_features: compatibility.required_features,
      extension_version_range: `>=${packageJSON.version} <0.1.0`,
    });
  }
  const specPath = join(temporaryRoot, "binary-release-spec.json");
  await writeJSON(specPath, {
    channel: process.env["CODEHELPER_RELEASE_CHANNEL"] ?? "stable",
    sequence,
    generated_at: builtAt,
    artifacts: specArtifacts,
    revoked_versions: [],
    revoked_digests: [],
    key_statements: [],
  });
  await rm(binaryRoot, { recursive: true, force: true });
  await execute(process.execPath, [
    join(import.meta.dirname, "binary-release.mjs"),
  ], {
    cwd: extensionRoot,
    env: {
      ...process.env,
      CODEHELPER_RELEASE_SPEC: specPath,
      CODEHELPER_RELEASE_PRIVATE_KEY: privateKeyPath,
      CODEHELPER_RELEASE_KEY_ID: keyID,
      CODEHELPER_RELEASE_OUTPUT: binaryRoot,
    },
    maxBuffer: 10 << 20,
  });
  await execute(process.execPath, [
    join(import.meta.dirname, "vscode-matrix.mjs"),
  ], {
    cwd: extensionRoot,
    env: {
      ...process.env,
      CODEHELPER_BINARY_RELEASE_ROOT: binaryRoot,
      CODEHELPER_RELEASE_TRUST_ROOTS: trustRootsPath,
      CODEHELPER_VSCODE_RELEASE_OUTPUT: outputRoot,
      CODEHELPER_RELEASE_COMMIT: commit,
      CODEHELPER_RELEASE_BUILT_AT: builtAt,
      CODEHELPER_RELEASE_KEY_ID: keyID,
      CODEHELPER_RELEASE_SOURCE_STATE: source.state,
      CODEHELPER_RELEASE_SOURCE_FINGERPRINT: source.fingerprint,
    },
    maxBuffer: 20 << 20,
  });
  await execute(process.execPath, [
    join(import.meta.dirname, "channel-dry-run.mjs"),
  ], {
    cwd: extensionRoot,
    env: {
      ...process.env,
      CODEHELPER_VSCODE_RELEASE_OUTPUT: outputRoot,
    },
    maxBuffer: 10 << 20,
  });
  await execute(process.execPath, [
    join(import.meta.dirname, "vscode-release-smoke.mjs"),
  ], {
    cwd: extensionRoot,
    env: {
      ...process.env,
      CODEHELPER_VSCODE_RELEASE_OUTPUT: outputRoot,
    },
    maxBuffer: 20 << 20,
  });
  process.stdout.write(
    `VS Code release prepared; dry_run=${String(dryRun)}; ` +
    `uploaded=false; root=${outputRoot}\n`,
  );
} finally {
  await rm(temporaryRoot, { recursive: true, force: true });
}

async function assertSigningIdentity(privatePath, rootsPath, id) {
  const privateKey = createPrivateKey(await readFile(privatePath));
  const actual = createPublicKey(privateKey).export({
    type: "spki",
    format: "der",
  });
  const roots = JSON.parse(await readFile(rootsPath, "utf8"));
  const expectedPEM = roots.keys?.[id];
  if (typeof expectedPEM !== "string") {
    throw new Error(`trust roots do not contain signing key ${id}`);
  }
  const expected = createPublicKey(expectedPEM).export({
    type: "spki",
    format: "der",
  });
  if (!actual.equals(expected)) {
    throw new Error("release private key does not match the public trust root");
  }
}

async function buildGoSBOM(versionValue) {
  const { stdout } = await execute("go", [
    "list", "-m", "-f", "{{.Path}}\t{{.Version}}", "all",
  ], { cwd: repositoryRoot, maxBuffer: 20 << 20 });
  const components = stdout.trim().split("\n").filter(Boolean).map((line) => {
    const [path, moduleVersion = ""] = line.split("\t");
    if (path === undefined || path.length === 0) {
      throw new Error("go list returned an invalid module path");
    }
    return {
      type: "library",
      name: path,
      version: moduleVersion || "workspace",
      ...(moduleVersion
        ? { purl: `pkg:golang/${path}@${moduleVersion}` }
        : {}),
    };
  });
  return {
    bomFormat: "CycloneDX",
    specVersion: "1.5",
    version: 1,
    metadata: {
      component: { type: "application", name: "codehelper", version: versionValue },
    },
    components,
  };
}

async function digest(path) {
  return createHash("sha256").update(await readFile(path)).digest("hex");
}

async function writeJSON(path, value) {
  await mkdir(resolve(path, ".."), { recursive: true });
  await writeFile(path, `${JSON.stringify(value, null, 2)}\n`, { mode: 0o600 });
}
