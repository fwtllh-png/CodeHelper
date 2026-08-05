import { execFile, spawn } from "node:child_process";
import { Buffer } from "node:buffer";
import { createHash } from "node:crypto";
import {
  chmod,
  copyFile,
  cp,
  mkdir,
  mkdtemp,
  readFile,
  readdir,
  rm,
  writeFile,
} from "node:fs/promises";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { setTimeout } from "node:timers";
import { promisify } from "node:util";

import { createVSIX } from "@vscode/vsce";
import { build, stop } from "esbuild";

const execFileAsync = promisify(execFile);
const startedAt = Date.now();
const extensionRoot = resolve(import.meta.dirname, "..");
const repositoryRoot = resolve(extensionRoot, "..", "..");
const binary = process.env["CODEHELPER_VSCODE_CONTAINER_BINARY"] ??
  join(repositoryRoot, "bin", "codehelper-linux-arm64");
const fixture = process.env["CODEHELPER_VSCODE_SELECTION_FIXTURE"] ??
  join(repositoryRoot, "testdata", "providers", "selection-commands");
const scenario = process.env["CODEHELPER_VSCODE_CONTAINER_SCENARIO"] ?? "single";
const binarySource =
  process.env["CODEHELPER_VSCODE_CONTAINER_BINARY_SOURCE"] ?? "external";
const containerPlatform =
  process.env["CODEHELPER_VSCODE_CONTAINER_PLATFORM"] ?? "linux/arm64";
if (!["single", "multi"].includes(scenario) ||
  !["external", "managed"].includes(binarySource) ||
  !["linux/arm64", "linux/amd64"].includes(containerPlatform)) {
  throw new Error("container scenario, source, or platform is invalid");
}
const vscodeExecutablePath =
  process.env["CODEHELPER_CONTAINER_VSCODE_EXECUTABLE"] ??
  "/Applications/Visual Studio Code.app/Contents/MacOS/Code";
const resolverExtension =
  process.env["CODEHELPER_DEV_CONTAINERS_EXTENSION"] ??
  await findInstalledExtension(
    join(extensionRoot, ".vscode-test", "remote-extensions"),
    "ms-vscode-remote.remote-containers-",
  );
const resolverID = "ms-vscode-remote.remote-containers";
const dockerHost = process.env["CODEHELPER_DOCKER_HOST"] ??
  (await docker(
    "context",
    "inspect",
    "--format",
    "{{.Endpoints.docker.Host}}",
  )).trim();
const workspaceRoot = join(repositoryRoot, ".tmp");
await mkdir(workspaceRoot, { recursive: true });
const localWorkspace = await mkdtemp(
  join(workspaceRoot, "vscode-container-"),
);
const containerWorkspace = "/workspace";
const authority = Buffer.from(localWorkspace, "utf8").toString("hex");
const testOutput = join(extensionRoot, ".tmp-container");
const localRoot = await mkdtemp(join(tmpdir(), "codehelper-vscode-ui-"));
const runVersion = `0.0.${Math.floor(Date.now() / 1_000)}`;
let serverCLI;
let containerID;
let installed = false;
let runnerInstalled = false;

try {
  await rm(testOutput, { recursive: true, force: true });
  await mkdir(testOutput, { recursive: true });
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
  await buildExtension(true);
  const testVSIX = join(testOutput, "codehelper-vscode-remote-test.vsix");
  await createVSIX({
    cwd: extensionRoot,
    packagePath: testVSIX,
    dependencies: false,
    allowMissingRepository: true,
    version: runVersion,
    updatePackageJson: false,
  });
  const runnerRoot = join(testOutput, "runner");
  await mkdir(join(runnerRoot, "dist"), { recursive: true });
  await writeFile(join(runnerRoot, "package.json"), JSON.stringify({
    name: "codehelper-remote-test",
    displayName: "CodeHelper Remote Test",
    version: runVersion,
    publisher: "codehelper",
    engines: { vscode: "^1.96.0" },
    extensionKind: ["workspace"],
    activationEvents: ["onStartupFinished"],
    main: "./dist/runner.js",
  }));
  await build({
    entryPoints: [
      join(extensionRoot, "src", "test", "remote-runner.ts"),
    ],
    outfile: join(runnerRoot, "dist", "runner.js"),
    bundle: true,
    external: ["vscode"],
    format: "cjs",
    platform: "node",
    target: "node20",
    logLevel: "silent",
  });
  const runnerVSIX = join(testOutput, "codehelper-remote-test.vsix");
  await createVSIX({
    cwd: runnerRoot,
    packagePath: runnerVSIX,
    dependencies: false,
    allowMissingRepository: true,
    allowStarActivation: true,
  });

  const wrapper = join(localWorkspace, "codehelper-fixture");
  const settings = JSON.stringify({
    "codehelper.runtime.autoStart": true,
    ...(binarySource === "external"
      ? { "codehelper.binaryPath": `${containerWorkspace}/codehelper-fixture` }
      : {}),
  });
  const context = [
    "export function outer(): string {",
    "  function inner(value: string): string {",
    "    return value;",
    "  }",
    "  return inner('ok');",
    "}",
    "",
  ].join("\n");
  const wrapperContent =
    "#!/bin/sh\n" +
    `if [ "$1" = "version" ]; then exec ${containerWorkspace}/codehelper "$@"; fi\n` +
    `exec ${containerWorkspace}/codehelper "$@" --provider-fixture ` +
      `${containerWorkspace}/selection-commands\n`;
  await mkdir(join(localWorkspace, ".vscode"), { recursive: true });
  await mkdir(join(localWorkspace, ".devcontainer"), { recursive: true });
  await writeFile(
    join(localWorkspace, ".devcontainer", "devcontainer.json"),
    JSON.stringify({
      name: "CodeHelper VS Code integration",
      image: "ubuntu:24.04",
      remoteUser: "root",
      workspaceMount:
        "source=${localWorkspaceFolder},target=/workspace,type=bind",
      workspaceFolder: containerWorkspace,
      shutdownAction: "stopContainer",
      runArgs: ["--platform", containerPlatform],
    }),
  );
  if (scenario === "multi") {
    for (const root of ["root-a", "root-b"]) {
      await mkdir(join(localWorkspace, root, ".vscode"), { recursive: true });
      await writeFile(
        join(localWorkspace, root, ".vscode", "settings.json"),
        settings,
      );
      await writeFile(join(localWorkspace, root, "context.ts"), context);
    }
    await writeFile(
      join(localWorkspace, "multi.code-workspace"),
      JSON.stringify({
        folders: [
          { path: "root-a", name: "root-a" },
          { path: "root-b", name: "root-b" },
        ],
        settings: JSON.parse(settings),
      }),
    );
  } else {
    await writeFile(
      join(localWorkspace, ".vscode", "settings.json"),
      settings,
    );
    await writeFile(join(localWorkspace, "context.ts"), context);
  }
  await writeFile(wrapper, wrapperContent);
  await copyFile(binary, join(localWorkspace, "codehelper"));
  await cp(fixture, join(localWorkspace, "selection-commands"), {
    recursive: true,
  });
  await copyFile(testVSIX, join(localWorkspace, "codehelper-test.vsix"));
  await copyFile(runnerVSIX, join(localWorkspace, "runner-test.vsix"));
  await chmod(wrapper, 0o700);
  await chmod(join(localWorkspace, "codehelper"), 0o700);
  await mkdir(join(localRoot, "data", "User"), { recursive: true });
  await mkdir(join(localRoot, "extensions"), { recursive: true });
  const isolatedResolver = join(localRoot, "remote-containers");
  await cp(resolverExtension, isolatedResolver, { recursive: true });
  await writeFile(
    join(localRoot, "data", "User", "settings.json"),
    JSON.stringify({
      "dev.containers.showReopenInContainerNotification": false,
      "codehelper.binarySource": binarySource,
    }),
  );

  containerID = await findContainer(localWorkspace);
  serverCLI = containerID === "" ? "" : await findServerCLI(containerID);
  if (serverCLI === "") {
    const bootstrap = launchVSCode(isolatedResolver);
    const bootstrapExit = childExit(bootstrap);
    await Promise.race([bootstrapExit, delay(120_000)]);
    await terminateChild(bootstrap, bootstrapExit);
    containerID = await findContainer(localWorkspace);
    serverCLI = containerID === "" ? "" : await findServerCLI(containerID);
  }
  if (containerID === "" || serverCLI === "") {
    throw new Error(
      "VS Code Dev Container server could not be bootstrapped",
    );
  }
  if (binarySource === "managed") {
    const digest = createHash("sha256")
      .update(wrapperContent)
      .digest("hex");
    const managedRoot =
      "/root/.vscode-server/data/User/globalStorage/" +
      "codehelper.codehelper-vscode/managed-binary";
    const state = JSON.stringify({
      schema_version: 1,
      max_sequence: 1,
      active: { digest, version: "dev", sequence: 1 },
      revoked_versions: [],
      revoked_digests: [],
      receipts: [],
    });
    await containerExec(containerID, [
      `rm -rf ${shellQuote(managedRoot)}`,
      `mkdir -p ${shellQuote(`${managedRoot}/artifacts/${digest}`)}`,
      `cp ${containerWorkspace}/codehelper-fixture ` +
        `${shellQuote(`${managedRoot}/artifacts/${digest}/codehelper`)}`,
      `chmod 500 ${shellQuote(`${managedRoot}/artifacts/${digest}/codehelper`)}`,
      `printf %s ${shellQuote(Buffer.from(state).toString("base64"))} | ` +
        `base64 -d > ${shellQuote(`${managedRoot}/state.json`)}`,
    ].join(" && "));
  }
  const installedExtensions = await containerExec(
    containerID,
    `${shellQuote(serverCLI)} --list-extensions --show-versions`,
  );
  const codehelperInstall = installedExtensions.split(/\r?\n/u).find(
    (value) => value.startsWith("codehelper.codehelper-vscode@"),
  );
  if (codehelperInstall !== undefined &&
    !/^codehelper\.codehelper-vscode@0\.0\.\d{10}$/u.test(codehelperInstall)) {
    throw new Error(
      "Dev Container already has CodeHelper installed; refusing to replace it",
    );
  }
  if (codehelperInstall !== undefined) {
    await containerExec(
      containerID,
      `${shellQuote(serverCLI)} --uninstall-extension ` +
        "codehelper.codehelper-remote-test || true; " +
        `${shellQuote(serverCLI)} --uninstall-extension ` +
        "codehelper.codehelper-vscode",
    );
  }
  await containerExec(
    containerID,
    `${shellQuote(serverCLI)} --install-extension ` +
      `${containerWorkspace}/codehelper-test.vsix --force`,
  );
  installed = true;
  await containerExec(
    containerID,
    `${shellQuote(serverCLI)} --install-extension ` +
      `${containerWorkspace}/runner-test.vsix --force`,
  );
  runnerInstalled = true;

  const resultPath = scenario === "multi"
    ? join(localWorkspace, "root-a", ".codehelper-remote-result.json")
    : join(localWorkspace, ".codehelper-remote-result.json");
  await runContainerWindow(isolatedResolver, resultPath);
} finally {
  if (runnerInstalled && serverCLI !== undefined &&
    containerID !== undefined && containerID !== "") {
    await containerExec(
      containerID,
      `${shellQuote(serverCLI)} --uninstall-extension ` +
        "codehelper.codehelper-remote-test",
    ).catch(() => undefined);
  }
  if (installed && serverCLI !== undefined &&
    containerID !== undefined && containerID !== "") {
    await containerExec(
      containerID,
      `${shellQuote(serverCLI)} --uninstall-extension ` +
        "codehelper.codehelper-vscode",
    ).catch(() => undefined);
  }
  await buildExtension(false).catch(() => undefined);
  stop();
  if (containerID !== undefined && containerID !== "") {
    await docker("rm", "-f", containerID).catch(() => undefined);
  }
  await rm(testOutput, { recursive: true, force: true });
  await rm(localWorkspace, { recursive: true, force: true });
  await rm(localRoot, { recursive: true, force: true });
}
const matrixRoot = join(extensionRoot, "dist", "matrix", "evidence");
await mkdir(matrixRoot, { recursive: true });
const matrixResult = process.env["CODEHELPER_MATRIX_RESULT"] ??
  join(
    matrixRoot,
    `dev-container-${containerPlatform.replace("/", "-")}-` +
      `${scenario}-${binarySource}.json`,
  );
await writeFile(matrixResult, `${JSON.stringify({
    schema_version: 1,
    job: `dev-container-${containerPlatform.replace("/", "-")}-` +
      `${scenario}-${binarySource}`,
    host: "dev-container",
    target: containerPlatform.replace("/", "-"),
    root: scenario,
    binary_source: binarySource,
    status: "passed",
    duration_ms: Date.now() - startedAt,
    scenarios: [
      "selection",
      "code_action",
      "changes",
      "approval",
      "restart_recovery",
      "fresh_container_attach",
      ...(scenario === "multi" ? ["root_remove_readd"] : []),
    ],
  }, null, 2)}\n`);
process.stdout.write("Dev Container integration passed\n");
process.exit(0);

async function runContainerWindow(
  isolatedResolver,
  resultPath,
) {
  const child = launchVSCode(isolatedResolver);
  const diagnostics = { value: "" };
  for (const stream of [child.stdout, child.stderr]) {
    stream.setEncoding("utf8");
    stream.on("data", (chunk) => {
      diagnostics.value = `${diagnostics.value}${chunk}`.slice(-(1 << 20));
    });
  }
  const exited = childExit(child);
  try {
    const result = await waitContainerResult(exited, resultPath, diagnostics);
    await Promise.race([exited, delay(2_000)]);
    return result;
  } catch (error) {
    await terminateChild(child, exited);
    throw error;
  }
}

async function waitContainerResult(exited, resultPath, diagnostics) {
  const deadline = Date.now() + 120_000;
  let rawResult;
  while (Date.now() < deadline) {
    rawResult = await readFile(resultPath, "utf8").catch(() => undefined);
    if (rawResult !== undefined && rawResult.trim() !== "") break;
    const state = await Promise.race([
      exited,
      delay(1_000).then(() => undefined),
    ]);
    if (state !== undefined) {
      throw new Error(
        `Dev Container window exited before tests completed: ` +
          `${JSON.stringify(state)}\n${diagnostics.value}`,
      );
    }
  }
  if (rawResult === undefined || rawResult.trim() === "") {
    throw new Error(`Dev Container tests timed out\n${diagnostics.value}`);
  }
  const result = JSON.parse(rawResult);
  if (result?.ok !== true) {
    throw new Error(
      `Dev Container tests failed: ${String(result?.error)}\n` +
        diagnostics.value,
    );
  }
  return result;
}

async function docker(...args) {
  const result = await execFileAsync("docker", args, {
    maxBuffer: 1 << 20,
    timeout: 60_000,
  });
  return result.stdout;
}

async function containerExec(id, command) {
  return docker("exec", id, "/bin/sh", "-lc", command);
}

async function findContainer(workspace) {
  return (await docker(
    "ps",
    "-aq",
    "--filter",
    `label=devcontainer.local_folder=${workspace}`,
  ).catch(() => "")).trim().split(/\r?\n/u)[0] ?? "";
}

async function findServerCLI(id) {
  const serverArch = containerPlatform === "linux/amd64"
    ? "linux-x64"
    : "linux-arm64";
  return (await containerExec(
    id,
    `find /vscode/vscode-server/bin/${serverArch} -type f -name code-server ` +
      "2>/dev/null | head -n 1",
  ).catch(() => "")).trim();
}

function launchVSCode(isolatedResolver) {
  const workspaceArguments = scenario === "multi"
    ? [
        "--file-uri",
        `vscode-remote://dev-container+${authority}` +
          `${containerWorkspace}/multi.code-workspace`,
      ]
    : [
        "--folder-uri",
        `vscode-remote://dev-container+${authority}${containerWorkspace}`,
      ];
  return spawn(vscodeExecutablePath, [
    "--extensionDevelopmentPath", isolatedResolver,
    "--enable-proposed-api", resolverID,
    ...workspaceArguments,
    "--user-data-dir", join(localRoot, "data"),
    "--extensions-dir", join(localRoot, "extensions"),
    "--password-store=basic",
    "--use-inmemory-secretstorage",
    "--disable-gpu",
    "--disable-chromium-sandbox",
    "--disable-updates",
    "--disable-telemetry",
    "--disable-crash-reporter",
    "--disable-workspace-trust",
    "--skip-release-notes",
    "--skip-welcome",
    "--wait",
  ], {
    env: {
      ...process.env,
      DOCKER_HOST: dockerHost,
      ELECTRON_DISABLE_CRASH_REPORTING: "1",
      ELECTRON_NO_CRASHPAD: "1",
      HOME: localRoot,
      VSCODE_PORTABLE: join(localRoot, "portable"),
    },
    stdio: ["ignore", "pipe", "pipe"],
  });
}

function childExit(child) {
  return new Promise((resolve) => {
    child.once("exit", (code, signal) => {
      resolve({ code, signal });
    });
  });
}

async function terminateChild(child, exited) {
  if (child.exitCode === null) child.kill("SIGKILL");
  await Promise.race([exited, delay(5_000)]);
}

async function findInstalledExtension(root, prefix) {
  const entries = await readdir(root, { withFileTypes: true }).catch(() => []);
  const matches = entries
    .filter((entry) => entry.isDirectory() && entry.name.startsWith(prefix))
    .map((entry) => entry.name)
    .sort();
  const selected = matches.at(-1);
  if (selected === undefined) {
    throw new Error(
      `required VS Code extension ${prefix}* is not installed under ${root}`,
    );
  }
  return join(root, selected);
}

async function buildExtension(test) {
  await build({
    entryPoints: [join(extensionRoot, "src", "extension.ts")],
    outfile: join(extensionRoot, "dist", "extension.js"),
    bundle: true,
    external: ["vscode"],
    format: "cjs",
    platform: "node",
    target: "node20",
    define: {
      __CODEHELPER_TEST_BUILD__: test ? "true" : "false",
    },
    logLevel: "silent",
  });
}

function shellQuote(value) {
  return `'${value.replaceAll("'", "'\\''")}'`;
}

function delay(milliseconds) {
  return new Promise((resolve) => {
    setTimeout(resolve, milliseconds);
  });
}
