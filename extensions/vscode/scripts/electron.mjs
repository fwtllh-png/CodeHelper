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
const subagentWorkspace = join(fixtureRoot, "workspace-subagent");
const workspaceA = join(fixtureRoot, "workspace-a");
const workspaceB = join(fixtureRoot, "workspace-b");
const multiWorkspace = join(fixtureRoot, "multi.code-workspace");
const nativeBinary = process.env["CODEHELPER_VSCODE_BINARY"];
const nativeFixture = process.env["CODEHELPER_VSCODE_SELECTION_FIXTURE"];
const subagentFixture = process.env["CODEHELPER_VSCODE_SUBAGENT_FIXTURE"];
const testPlatform = process.env["CODEHELPER_VSCODE_TEST_PLATFORM"];
const expectedHostArch = process.env["CODEHELPER_EXPECTED_HOST_ARCH"];
const disableGPU = process.env["CODEHELPER_VSCODE_DISABLE_GPU"] === "1";
const keepTemp = process.env["CODEHELPER_ELECTRON_KEEP_TEMP"] === "1";
const approvalEvidenceDir = process.env["CODEHELPER_APPROVAL_EVIDENCE_DIR"];
const approvalDebugPort = "9333";
const matrixTarget = process.env["CODEHELPER_MATRIX_TARGET"] ??
  `${process.platform}-${process.arch}`;
let electronPerformance;
const completedJourneys = new Set(["surface.webview-view"]);

try {
  await rm(testOutput, { recursive: true, force: true });
  await mkdir(testOutput, { recursive: true });
  if (approvalEvidenceDir !== undefined) {
    await mkdir(approvalEvidenceDir, { recursive: true });
  }
  await mkdir(join(workspace, ".vscode"), { recursive: true });
  await mkdir(join(nativeWorkspace, ".vscode"), { recursive: true });
  await mkdir(join(subagentWorkspace, ".vscode"), { recursive: true });
  await writeFile(
    join(subagentWorkspace, "codehelper.toml"),
    "[execution.subagent]\nworkspace = \"worktree\"\n",
  );
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
  if (subagentFixture !== undefined && nativeBinary === undefined) {
    throw new Error(
      "CODEHELPER_VSCODE_SUBAGENT_FIXTURE requires CODEHELPER_VSCODE_BINARY",
    );
  }
  const subagentWrapper = subagentFixture === undefined
    ? undefined
    : await fixtureWrapper(
      fixtureRoot,
      nativeBinary,
      subagentFixture,
      "codehelper-subagent-fixture",
      ["--posture", "suggest"],
    );
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
    "accessibility",
    ...(nativeWrapper === undefined ? [] : ["approval"]),
    ...(nativeWrapper === undefined ? [] : ["native"]),
    ...(nativeWrapper === undefined ? [] : ["multi"]),
    ...(subagentWrapper === undefined ? [] : ["subagent"]),
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
      const native = scenario === "native" ||
        scenario === "approval" ||
        scenario === "multi" ||
        scenario === "subagent";
      const binaryPath = scenario === "subagent"
        ? subagentWrapper
        : nativeWrapper;
      const settings = {
        "codehelper.runtime.autoStart": native,
        ...(native ? { "codehelper.binaryPath": binaryPath } : {}),
        ...(scenario === "subagent"
          ? {
            "codehelper.runtime.configPath": join(
              subagentWorkspace,
              "codehelper.toml",
            ),
          }
          : {}),
      };
      const scenarioWorkspace = scenario === "native"
        || scenario === "approval"
        ? nativeWorkspace
        : scenario === "subagent"
          ? subagentWorkspace
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
      if (scenario === "native" || scenario === "approval" ||
        scenario === "subagent") {
        await writeFile(join(scenarioWorkspace, "risk.txt"), "baseline\n");
        await execFile("git", ["init", "--quiet"], { cwd: scenarioWorkspace });
        await execFile(
          "git",
          ["config", "user.email", "electron@codehelper.invalid"],
          { cwd: scenarioWorkspace },
        );
        await execFile(
          "git",
          ["config", "user.name", "CodeHelper Electron"],
          { cwd: scenarioWorkspace },
        );
        await execFile("git", ["add", "."], { cwd: scenarioWorkspace });
        await execFile(
          "git",
          ["commit", "--quiet", "-m", "Electron fixture baseline"],
          { cwd: scenarioWorkspace },
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
            : scenario === "native"
              || scenario === "approval"
              ? nativeWorkspace
              : scenario === "subagent" ? subagentWorkspace : workspace]),
        "--user-data-dir", join(hostRoot, "data"),
        "--extensions-dir", join(hostRoot, "extensions"),
        "--password-store=basic",
        "--use-inmemory-secretstorage",
        "--disable-updates",
        "--disable-workspace-trust",
        "--skip-release-notes",
        "--skip-welcome",
        ...(scenario === "accessibility" ? ["--force-high-contrast"] : []),
        ...(scenario === "approval" && approvalEvidenceDir !== undefined
          ? [`--remote-debugging-port=${approvalDebugPort}`]
          : []),
        ...(disableGPU ? ["--disable-gpu"] : []),
      ];
      const exitCode = await runTests({
        version: process.env["CODEHELPER_VSCODE_TEST_VERSION"] ?? "1.96.4",
        ...(testPlatform === undefined ? {} : { platform: testPlatform }),
        extensionDevelopmentPath: extensionRoot,
        extensionTestsPath: testOutput,
        extensionTestsEnv: {
          CODEHELPER_ELECTRON_SCENARIO: scenario,
          ...(scenario === "approval" && approvalEvidenceDir !== undefined
            ? {
              CODEHELPER_APPROVAL_EVIDENCE_DIR: approvalEvidenceDir,
              CODEHELPER_APPROVAL_DEBUG_PORT: approvalDebugPort,
            }
            : {}),
          ...(scenario === "subagent"
            ? {
              CODEHELPER_VERIFY_MODE: "soft",
              CODEHELPER_VERIFY_SCOPE: "affected",
              CODEHELPER_VERIFY_COMMAND: "test -f child-note.txt",
            }
            : {}),
          ...(expectedHostArch === undefined
            ? {}
            : { CODEHELPER_EXPECTED_HOST_ARCH: expectedHostArch }),
        },
        launchArgs,
      });
      if (exitCode !== 0) {
        throw new Error(`VS Code ${scenario} scenario exited with ${String(exitCode)}`);
      }
      for (const journey of scenarioJourneys(scenario)) {
        completedJourneys.add(journey);
      }
      if (scenario === "workspace") {
        electronPerformance = JSON.parse(await readFile(
          join(workspace, ".codehelper-performance.json"),
          "utf8",
        ));
      }
    } finally {
      if (keepTemp) {
        process.stderr.write(`preserved Electron host data: ${hostRoot}\n`);
      } else {
        await rm(hostRoot, { recursive: true, force: true });
      }
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
      journeys: [...completedJourneys].sort(),
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
  if (keepTemp) {
    process.stderr.write(`preserved Electron fixtures: ${fixtureRoot}\n`);
  } else {
    await rm(fixtureRoot, { recursive: true, force: true });
  }
}

async function fixtureWrapper(
  root,
  binary,
  fixture,
  name = "codehelper-selection-fixture",
  extraArguments = [],
) {
  const wrapper = join(root, name);
  const suffix = extraArguments.map(shellQuote).join(" ");
  await writeFile(
    wrapper,
    `#!/bin/sh\n` +
    `if [ "$1" = "version" ]; then exec ${shellQuote(binary)} "$@"; fi\n` +
    `exec ${shellQuote(binary)} "$@" --provider-fixture ${shellQuote(fixture)}` +
    `${suffix === "" ? "" : ` ${suffix}`}\n`,
  );
  await chmod(wrapper, 0o700);
  return wrapper;
}

function shellQuote(value) {
  return `'${value.replaceAll("'", "'\\''")}'`;
}

function scenarioJourneys(scenario) {
  switch (scenario) {
    case "empty":
      return ["empty"];
    case "workspace":
      return [
        "context.native",
        "resource.navigation",
        "theme.light-dark-high-contrast",
        "zoom.200",
        "hidden.resume",
      ];
    case "accessibility":
      return ["forced-colors", "ime.composition"];
    case "approval":
      return ["approval-verification-receipt"];
    case "native":
      return [
        "runtime.streaming-stop",
        "runtime.retry-continue",
        "composer.model-thinking",
        "composer.tools",
        "composer.credential",
        "approval-verification-receipt",
        "session.lifecycle",
        "session.search-to-turn",
        "plan.destinations",
        "resource.navigation",
      ];
    case "multi":
      return ["workspace.multi-root", "resource.navigation"];
    case "subagent":
      return ["runtime.explicit-subagent"];
    case "bundled":
      return [];
    default:
      throw new Error(`unknown Electron journey scenario ${String(scenario)}`);
  }
}
