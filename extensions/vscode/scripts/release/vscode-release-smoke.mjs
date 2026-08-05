import { execFile } from "node:child_process";
import {
  mkdir,
  mkdtemp,
  readFile,
  rm,
  writeFile,
} from "node:fs/promises";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { promisify } from "node:util";
import { build } from "esbuild";
import { runTests, runVSCodeCommand } from "@vscode/test-electron";

const execute = promisify(execFile);
const startedAt = Date.now();
const extensionRoot = resolve(import.meta.dirname, "..", "..");
const releaseRoot = resolve(
  process.env["CODEHELPER_VSCODE_RELEASE_OUTPUT"] ??
    join(extensionRoot, "dist", "vscode-release"),
);
const provenance = JSON.parse(await readFile(
  join(releaseRoot, "provenance", "release-provenance.json"),
  "utf8",
));
if (!Array.isArray(provenance.artifacts) ||
  provenance.artifacts.length !== 6) {
  throw new Error("release provenance must contain six VSIX artifacts");
}
const root = await mkdtemp(join(tmpdir(), "codehelper-vscode-release-smoke-"));
const extensions = join(root, "extensions");
const data = join(root, "data");
const extracted = join(root, "extracted");
const common = ["--extensions-dir", extensions, "--user-data-dir", data];
const options = {
  version: process.env["CODEHELPER_VSCODE_TEST_VERSION"] ?? "1.96.4",
  spawn: {
    env: {
      ...process.env,
      VSCODE_PORTABLE: join(root, "portable"),
    },
  },
};
let hostExtension;
try {
  for (const artifact of provenance.artifacts) {
    if (artifact.kind !== "bundled") continue;
    const vsix = join(releaseRoot, artifact.file);
    const targetRoot = join(extracted, artifact.target);
    await mkdir(targetRoot, { recursive: true });
    await execute("unzip", ["-q", vsix, "-d", targetRoot]);
    const executable = artifact.target === "win32-x64"
      ? "codehelper.exe"
      : "codehelper";
    const binary = join(
      targetRoot,
      "extension",
      "bin",
      artifact.target,
      executable,
    );
    assertBinaryTarget(await readFile(binary), artifact.target);
    if (artifact.target === hostTarget()) {
      hostExtension = join(targetRoot, "extension");
      const result = await execute(binary, ["version", "--json"], {
        maxBuffer: 1 << 20,
      });
      const version = JSON.parse(result.stdout);
      if (version.name !== "codehelper" ||
        version.version !== artifact.binary_version) {
        throw new Error("host bundled binary identity is invalid");
      }
    }
  }
  const installable = provenance.artifacts.filter((artifact) =>
    artifact.target === "universal" || artifact.target === hostTarget());
  if (installable.length !== 2) {
    throw new Error(`release has no host package for ${hostTarget()}`);
  }
  for (const artifact of installable) {
    const vsix = join(releaseRoot, artifact.file);
    await runVSCodeCommand([
      "--install-extension", vsix, "--force", ...common,
    ], options);
    const listed = await runVSCodeCommand([
      "--list-extensions", "--show-versions", ...common,
    ], options);
    if (!listed.stdout.split(/\r?\n/u)
      .includes(`codehelper.codehelper-vscode@${provenance.extension_version}`)) {
      throw new Error(`${artifact.target} VSIX was not listed after install`);
    }
    await runVSCodeCommand([
      "--uninstall-extension", "codehelper.codehelper-vscode", ...common,
    ], options);
  }
  if (hostExtension === undefined) {
    throw new Error("host extension was not extracted");
  }
  const testOutput = join(root, "test", "index.js");
  await mkdir(join(root, "test"), { recursive: true });
  await build({
    entryPoints: [join(extensionRoot, "src", "test", "suite", "index.ts")],
    outfile: testOutput,
    bundle: true,
    external: ["vscode"],
    format: "cjs",
    platform: "node",
    target: "node20",
    logLevel: "silent",
  });
  const workspace = join(root, "workspace");
  await mkdir(join(workspace, ".vscode"), { recursive: true });
  await writeFile(
    join(workspace, ".vscode", "settings.json"),
    JSON.stringify({ "codehelper.runtime.autoStart": true }),
  );
  await writeFile(join(workspace, "context.ts"), "export const ready = true;\n");
  const exitCode = await runTests({
    version: options.version,
    extensionDevelopmentPath: hostExtension,
    extensionTestsPath: testOutput,
    extensionTestsEnv: {
      CODEHELPER_ELECTRON_SCENARIO: "bundled",
      CODEHELPER_PROVIDER: "openai",
      CODEHELPER_MODEL: "gpt-4.1",
      OPENAI_API_KEY: "release-smoke-not-a-secret",
    },
    launchArgs: [
      workspace,
      "--user-data-dir", join(root, "electron-data"),
      "--extensions-dir", join(root, "electron-extensions"),
      "--password-store=basic",
      "--use-inmemory-secretstorage",
      "--disable-updates",
      "--disable-workspace-trust",
      "--skip-release-notes",
      "--skip-welcome",
    ],
  });
  if (exitCode !== 0) {
    throw new Error("host bundled VSIX activation smoke failed");
  }
  const matrixRoot = join(extensionRoot, "dist", "matrix", "evidence");
  await mkdir(matrixRoot, { recursive: true });
  const matrixResult = process.env["CODEHELPER_MATRIX_RESULT"] ??
    join(matrixRoot, `local-${hostTarget()}-bundled.json`);
  await writeFile(matrixResult, `${JSON.stringify({
      schema_version: 1,
      job: `local-${hostTarget()}-bundled`,
      host: "local",
      target: hostTarget(),
      root: "single",
      binary_source: "bundled",
      status: "passed",
      duration_ms: Date.now() - startedAt,
      scenarios: [
        "install",
        "binary_identity",
        "handshake",
        "restart_recovery",
      ],
    }, null, 2)}\n`);
  process.stdout.write(
    `release smoke passed; host=${hostTarget()}; installed=2; ` +
    "static_targets=5; handshake=ready\n",
  );
} finally {
  if (process.env["CODEHELPER_RELEASE_SMOKE_KEEP"] === "1") {
    process.stderr.write(`release smoke retained ${root}\n`);
  } else {
    await rm(root, { recursive: true, force: true });
  }
}

function hostTarget() {
  const platform = process.platform === "win32" ? "win32" : process.platform;
  const architecture = process.arch === "x64" ? "x64" : process.arch;
  return `${platform}-${architecture}`;
}

function assertBinaryTarget(bytes, target) {
  if (target.startsWith("linux-")) {
    if (bytes.subarray(0, 4).toString("hex") !== "7f454c46") {
      throw new Error(`${target} artifact is not ELF`);
    }
    const machine = bytes.readUInt16LE(18);
    const expected = target === "linux-x64" ? 62 : 183;
    if (machine !== expected) throw new Error(`${target} ELF machine mismatch`);
    return;
  }
  if (target.startsWith("darwin-")) {
    if (bytes.subarray(0, 4).toString("hex") !== "cffaedfe") {
      throw new Error(`${target} artifact is not Mach-O 64`);
    }
    const cpu = bytes.readUInt32LE(4);
    const expected = target === "darwin-x64" ? 0x01000007 : 0x0100000c;
    if (cpu !== expected) throw new Error(`${target} Mach-O CPU mismatch`);
    return;
  }
  if (target === "win32-x64") {
    if (bytes.subarray(0, 2).toString("ascii") !== "MZ") {
      throw new Error("win32-x64 artifact is not PE");
    }
    const header = bytes.readUInt32LE(0x3c);
    if (bytes.subarray(header, header + 4).toString("hex") !== "50450000" ||
      bytes.readUInt16LE(header + 4) !== 0x8664) {
      throw new Error("win32-x64 PE machine mismatch");
    }
    return;
  }
  throw new Error(`unsupported VS Code target ${target}`);
}
