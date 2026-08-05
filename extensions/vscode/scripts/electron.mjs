import {
  chmod,
  mkdir,
  mkdtemp,
  readFile,
  rm,
  writeFile,
} from "node:fs/promises";
import { execFile as execFileCallback } from "node:child_process";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { promisify } from "node:util";
import { build } from "esbuild";
import { runTests } from "@vscode/test-electron";

const execFile = promisify(execFileCallback);
const extensionRoot = resolve(import.meta.dirname, "..");
const startedAt = Date.now();
const testOutput = join(extensionRoot, ".tmp-electron");
const fixtureRoot = await mkdtemp(join(tmpdir(), "codehelper-vscode-electron-"));
const workspace = join(fixtureRoot, "workspace");
const nativeWorkspace = join(fixtureRoot, "workspace-native");
const workspaceA = join(fixtureRoot, "workspace-a");
const workspaceB = join(fixtureRoot, "workspace-b");
const multiWorkspace = join(fixtureRoot, "multi.code-workspace");
const nativeBinary = process.env["CODEHELPER_VSCODE_BINARY"];
const nativeFixture = process.env["CODEHELPER_VSCODE_SELECTION_FIXTURE"];
const testPlatform = process.env["CODEHELPER_VSCODE_TEST_PLATFORM"];
const matrixTarget = process.env["CODEHELPER_MATRIX_TARGET"] ??
  `${process.platform}-${process.arch}`;
let electronPerformance;

try {
  await rm(testOutput, { recursive: true, force: true });
  await mkdir(testOutput, { recursive: true });
  await mkdir(join(workspace, ".vscode"), { recursive: true });
  await mkdir(join(nativeWorkspace, ".vscode"), { recursive: true });
  await mkdir(join(workspaceA, ".vscode"), { recursive: true });
  await mkdir(join(workspaceB, ".vscode"), { recursive: true });
  if ((nativeBinary === undefined) !== (nativeFixture === undefined)) {
    throw new Error(
      "CODEHELPER_VSCODE_BINARY and CODEHELPER_VSCODE_SELECTION_FIXTURE must be set together",
    );
  }
  const nativeWrapper = nativeBinary === undefined
    ? undefined
    : await fixtureWrapper(fixtureRoot, nativeBinary, nativeFixture);
  await build({
    entryPoints: [join(extensionRoot, "src", "test", "suite", "index.ts")],
    outfile: join(testOutput, "index.js"),
    bundle: true,
    external: ["vscode"],
    format: "cjs",
    platform: "node",
    target: "node20",
    sourcemap: true,
    logLevel: "silent",
  });

  const availableScenarios = [
    "empty",
    "workspace",
    ...(nativeWrapper === undefined ? [] : ["native"]),
    ...(nativeWrapper === undefined ? [] : ["multi"]),
  ];
  const requestedScenarios = process.env["CODEHELPER_ELECTRON_SCENARIOS"]
    ?.split(",")
    .filter((value) => value !== "");
  const scenarios = requestedScenarios ?? availableScenarios;
  if (scenarios.some((scenario) => !availableScenarios.includes(scenario))) {
    throw new Error("CODEHELPER_ELECTRON_SCENARIOS contains an unavailable scenario");
  }
  for (const scenario of scenarios) {
    if (scenario !== "empty") {
      const native = scenario === "native" || scenario === "multi";
      const settings = {
        "codehelper.runtime.autoStart": native,
        ...(native ? { "codehelper.binaryPath": nativeWrapper } : {}),
      };
      const scenarioWorkspace = scenario === "native"
        ? nativeWorkspace
        : workspace;
      await writeFile(
        join(scenarioWorkspace, ".vscode", "settings.json"),
        JSON.stringify(settings),
      );
      const content = [
          "export function outer(): string {",
          "  function inner(value: string): string {",
          "    return value;",
          "  }",
          "  return inner('ok');",
          "}",
          "",
        ].join("\n");
      await writeFile(join(scenarioWorkspace, "context.ts"), content);
      if (scenario === "native") {
        await execFile("git", ["init", "--quiet"], { cwd: nativeWorkspace });
        await execFile(
          "git",
          ["config", "user.email", "electron@codehelper.invalid"],
          { cwd: nativeWorkspace },
        );
        await execFile(
          "git",
          ["config", "user.name", "CodeHelper Electron"],
          { cwd: nativeWorkspace },
        );
        await execFile("git", ["add", "."], { cwd: nativeWorkspace });
        await execFile(
          "git",
          ["commit", "--quiet", "-m", "Electron fixture baseline"],
          { cwd: nativeWorkspace },
        );
      }
      if (scenario === "multi") {
        await writeFile(
          join(workspaceA, ".vscode", "settings.json"),
          JSON.stringify(settings),
        );
        await writeFile(join(workspaceA, "context.ts"), content);
        await writeFile(
          join(workspaceB, ".vscode", "settings.json"),
          JSON.stringify(settings),
        );
        await writeFile(join(workspaceB, "context.ts"), content);
        await writeFile(multiWorkspace, JSON.stringify({
          folders: [
            { path: workspaceA, name: "root-a" },
            { path: workspaceB, name: "root-b" },
          ],
          settings,
        }));
      }
    }
    const hostRoot = await mkdtemp(join(tmpdir(), `cdt-${scenario}-`));
    try {
      const launchArgs = [
        ...(scenario === "empty"
          ? []
          : [scenario === "multi"
            ? multiWorkspace
            : scenario === "native" ? nativeWorkspace : workspace]),
        "--user-data-dir", join(hostRoot, "data"),
        "--extensions-dir", join(hostRoot, "extensions"),
        "--password-store=basic",
        "--use-inmemory-secretstorage",
        "--disable-updates",
        "--disable-workspace-trust",
        "--skip-release-notes",
        "--skip-welcome",
      ];
      const exitCode = await runTests({
        version: process.env["CODEHELPER_VSCODE_TEST_VERSION"] ?? "1.96.4",
        ...(testPlatform === undefined ? {} : { platform: testPlatform }),
        extensionDevelopmentPath: extensionRoot,
        extensionTestsPath: testOutput,
        extensionTestsEnv: { CODEHELPER_ELECTRON_SCENARIO: scenario },
        launchArgs,
      });
      if (exitCode !== 0) {
        throw new Error(`VS Code ${scenario} scenario exited with ${String(exitCode)}`);
      }
      if (scenario === "workspace") {
        electronPerformance = JSON.parse(await readFile(
          join(workspace, ".codehelper-performance.json"),
          "utf8",
        ));
      }
    } finally {
      await rm(hostRoot, { recursive: true, force: true });
    }
  }
  const matrixRoot = join(extensionRoot, "dist", "matrix", "evidence");
  await mkdir(matrixRoot, { recursive: true });
  const matrixResult = process.env["CODEHELPER_MATRIX_RESULT"] ??
    join(matrixRoot, `local-${matrixTarget}-external.json`);
  await writeFile(matrixResult, `${JSON.stringify({
      schema_version: 1,
      job: `local-${matrixTarget}-external`,
      host: "local",
      target: matrixTarget,
      root: scenarios.includes("multi") ? "single+multi" : "single",
      binary_source: "external",
      status: "passed",
      duration_ms: Date.now() - startedAt,
      scenarios,
    }, null, 2)}\n`);
  if (electronPerformance !== undefined) {
    const performanceRoot = join(extensionRoot, "dist", "performance");
    await mkdir(performanceRoot, { recursive: true });
    await writeFile(
      join(performanceRoot, "electron.json"),
      `${JSON.stringify(electronPerformance, null, 2)}\n`,
    );
  }
} finally {
  await rm(testOutput, { recursive: true, force: true });
  await rm(fixtureRoot, { recursive: true, force: true });
}

async function fixtureWrapper(root, binary, fixture) {
  const wrapper = join(root, "codehelper-selection-fixture");
  await writeFile(
    wrapper,
    `#!/bin/sh\n` +
    `if [ "$1" = "version" ]; then exec ${shellQuote(binary)} "$@"; fi\n` +
    `exec ${shellQuote(binary)} "$@" --provider-fixture ${shellQuote(fixture)}\n`,
  );
  await chmod(wrapper, 0o700);
  return wrapper;
}

function shellQuote(value) {
  return `'${value.replaceAll("'", "'\\''")}'`;
}
