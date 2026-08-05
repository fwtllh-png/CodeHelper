import { execFile, spawn } from "node:child_process";
import { Buffer } from "node:buffer";
import { createHash } from "node:crypto";
import {
  chmod,
  cp,
  mkdir,
  mkdtemp,
  readFile,
  readdir,
  rm,
  writeFile,
} from "node:fs/promises";
import { homedir, tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { setTimeout } from "node:timers";
import { promisify } from "node:util";

import { createVSIX } from "@vscode/vsce";
import { build, stop } from "esbuild";

const execFileAsync = promisify(execFile);
const startedAt = Date.now();
const extensionRoot = resolve(import.meta.dirname, "..");
const repositoryRoot = resolve(extensionRoot, "..", "..");
const binary = process.env["CODEHELPER_VSCODE_REMOTE_BINARY"] ??
  join(repositoryRoot, "bin", "codehelper-linux-arm64");
const fixture = process.env["CODEHELPER_VSCODE_SELECTION_FIXTURE"] ??
  join(repositoryRoot, "testdata", "providers", "selection-commands");
const authority = process.env["CODEHELPER_VSCODE_REMOTE_AUTHORITY"] ?? "colima";
const scenario = process.env["CODEHELPER_VSCODE_REMOTE_SCENARIO"] ?? "single";
const binarySource =
  process.env["CODEHELPER_VSCODE_REMOTE_BINARY_SOURCE"] ?? "external";
if (!["single", "multi"].includes(scenario) ||
  !["external", "managed"].includes(binarySource)) {
  throw new Error("remote scenario/source must be single|multi and external|managed");
}
const vscodeExecutablePath =
  process.env["CODEHELPER_REMOTE_VSCODE_EXECUTABLE"] ??
  "/Applications/Visual Studio Code.app/Contents/MacOS/Code";
const resolverExtension =
  process.env["CODEHELPER_REMOTE_SSH_EXTENSION"] ??
  await findInstalledExtension(
    join(extensionRoot, ".vscode-test", "remote-extensions"),
    "ms-vscode-remote.remote-ssh-",
  );
const resolverID = "ms-vscode-remote.remote-ssh";
const remoteWorkspace = join(
  repositoryRoot,
  ".tmp",
  `vscode-remote-${String(process.pid)}`,
);
const testOutput = join(extensionRoot, ".tmp-remote");
const localRoot = await mkdtemp(join(tmpdir(), "codehelper-vscode-remote-"));
const runVersion = `0.0.${Math.floor(Date.now() / 1_000)}`;
let serverCLI;
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

  const wrapper = `${remoteWorkspace}/codehelper-fixture`;
  const settings = JSON.stringify({
    "codehelper.runtime.autoStart": true,
    ...(binarySource === "external" ? { "codehelper.binaryPath": wrapper } : {}),
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
    `if [ "$1" = "version" ]; then exec ${shellQuote(binary)} "$@"; fi\n` +
    `exec ${shellQuote(binary)} "$@" --provider-fixture ${shellQuote(fixture)}\n`;
  const workspaceSetup = scenario === "multi"
    ? [
        `mkdir -p ${shellQuote(`${remoteWorkspace}/root-a/.vscode`)}`,
        `mkdir -p ${shellQuote(`${remoteWorkspace}/root-b/.vscode`)}`,
        writeBase64(
          `${remoteWorkspace}/root-a/.vscode/settings.json`,
          settings,
        ),
        writeBase64(
          `${remoteWorkspace}/root-b/.vscode/settings.json`,
          settings,
        ),
        writeBase64(`${remoteWorkspace}/root-a/context.ts`, context),
        writeBase64(`${remoteWorkspace}/root-b/context.ts`, context),
        writeBase64(
          `${remoteWorkspace}/multi.code-workspace`,
          JSON.stringify({
            folders: [
              { path: "root-a", name: "root-a" },
              { path: "root-b", name: "root-b" },
            ],
            settings: JSON.parse(settings),
          }),
        ),
      ]
    : [
        `mkdir -p ${shellQuote(`${remoteWorkspace}/.vscode`)}`,
        writeBase64(`${remoteWorkspace}/.vscode/settings.json`, settings),
        writeBase64(`${remoteWorkspace}/context.ts`, context),
      ];
  await remote(authority, [
    `rm -rf ${shellQuote(remoteWorkspace)}`,
    `mkdir -p ${shellQuote(remoteWorkspace)}`,
    ...workspaceSetup,
    writeBase64(wrapper, wrapperContent),
    `chmod 700 ${shellQuote(wrapper)}`,
  ].join(" && "));
  await mkdir(join(localRoot, "data", "User"), { recursive: true });
  await mkdir(join(localRoot, "extensions"), { recursive: true });
  const sshWrapper = join(localRoot, "ssh-no-control");
  await writeFile(
    sshWrapper,
    `#!/bin/sh\nHOME=${shellQuote(homedir())} ` +
      "exec /usr/bin/ssh -o ControlMaster=no " +
      "-o ControlPath=none \"$@\"\n",
  );
  await chmod(sshWrapper, 0o700);
  const isolatedResolver = join(localRoot, "remote-ssh");
  await cp(resolverExtension, isolatedResolver, { recursive: true });
  await writeFile(
    join(localRoot, "data", "User", "settings.json"),
    JSON.stringify({
      "remote.SSH.remotePlatform": { [authority]: "linux" },
      "remote.SSH.connectTimeout": 30,
      "remote.SSH.path": sshWrapper,
      "codehelper.binarySource": binarySource,
    }),
  );

  serverCLI = await findRemoteServerCLI(authority);
  if (serverCLI === "") {
    const bootstrap = launchVSCode(isolatedResolver);
    const bootstrapExit = childExit(bootstrap);
    await Promise.race([bootstrapExit, delay(120_000)]);
    await terminateChild(bootstrap, bootstrapExit);
    serverCLI = await findRemoteServerCLI(authority);
  }
  if (binarySource === "managed") {
    const remoteHome = (await remote(authority, "printf %s \"$HOME\"")).trim();
    const digest = createHash("sha256")
      .update(wrapperContent)
      .digest("hex");
    const managedRoot =
      `${remoteHome}/.vscode-server/data/User/globalStorage/` +
      "codehelper.codehelper-vscode/managed-binary";
    const state = JSON.stringify({
      schema_version: 1,
      max_sequence: 1,
      active: { digest, version: "dev", sequence: 1 },
      revoked_versions: [],
      revoked_digests: [],
      receipts: [],
    });
    await remote(authority, [
      `rm -rf "${managedRoot}"`,
      `mkdir -p "${managedRoot}/artifacts/${digest}"`,
      writeBase64(
        `${managedRoot}/artifacts/${digest}/codehelper`,
        wrapperContent,
      ),
      `chmod 500 "${managedRoot}/artifacts/${digest}/codehelper"`,
      writeBase64(`${managedRoot}/state.json`, state),
    ].join(" && "));
  }
  if (serverCLI === "") {
    throw new Error(
      "VS Code Remote SSH server could not be bootstrapped",
    );
  }
  const installedExtensions = await remote(
    authority,
    `${shellQuote(serverCLI)} --list-extensions --show-versions`,
  );
  const codehelperInstall = installedExtensions.split(/\r?\n/u).find(
    (value) => value.startsWith("codehelper.codehelper-vscode@"),
  );
  if (codehelperInstall !== undefined &&
    !/^codehelper\.codehelper-vscode@0\.0\.\d{10}$/u.test(codehelperInstall)) {
    throw new Error(
      "remote authority already has CodeHelper installed; refusing to replace it",
    );
  }
  if (codehelperInstall !== undefined) {
    await remote(
      authority,
      `${shellQuote(serverCLI)} --uninstall-extension ` +
        "codehelper.codehelper-remote-test || true; " +
        `${shellQuote(serverCLI)} --uninstall-extension ` +
        "codehelper.codehelper-vscode",
    );
  }
  await remote(
    authority,
    `${shellQuote(serverCLI)} --install-extension ${shellQuote(testVSIX)} ` +
      "--force",
  );
  installed = true;
  await remote(
    authority,
    `${shellQuote(serverCLI)} --install-extension ${shellQuote(runnerVSIX)} ` +
      "--force",
  );
  runnerInstalled = true;

  const resultPath = scenario === "multi"
    ? `${remoteWorkspace}/root-a/.codehelper-remote-result.json`
    : `${remoteWorkspace}/.codehelper-remote-result.json`;
  const firstResult = await runRemoteWindow(isolatedResolver, resultPath);
  const markerPath = scenario === "multi"
    ? `${remoteWorkspace}/root-a/.codehelper-reconnect`
    : `${remoteWorkspace}/.codehelper-reconnect`;
  const resetContext = scenario === "multi"
    ? [
        `rm -rf ${shellQuote(`${remoteWorkspace}/root-a/alpha.txt`)} ` +
          `${shellQuote(`${remoteWorkspace}/root-a/nested`)}`,
        `rm -rf ${shellQuote(`${remoteWorkspace}/root-b/alpha.txt`)} ` +
          `${shellQuote(`${remoteWorkspace}/root-b/nested`)}`,
        writeBase64(`${remoteWorkspace}/root-a/context.ts`, context),
        writeBase64(`${remoteWorkspace}/root-b/context.ts`, context),
      ]
    : [
        `rm -rf ${shellQuote(`${remoteWorkspace}/alpha.txt`)} ` +
          `${shellQuote(`${remoteWorkspace}/nested`)}`,
        writeBase64(`${remoteWorkspace}/context.ts`, context),
      ];
  await remote(authority, [
    `rm -f ${shellQuote(resultPath)}`,
    writeBase64(markerPath, "reconnect\n"),
    ...resetContext,
  ].join(" && "));
  await remote(
    authority,
    "pkill -f '[t]ype=extensionHost' || true",
  ).catch(() => undefined);
  const secondResult = await runRemoteWindow(isolatedResolver, resultPath);
  assertReconnect(firstResult, secondResult);
} finally {
  if (runnerInstalled && serverCLI !== undefined) {
    await remote(
      authority,
      `${shellQuote(serverCLI)} --uninstall-extension ` +
        "codehelper.codehelper-remote-test",
    ).catch(() => undefined);
  }
  if (installed && serverCLI !== undefined) {
    await remote(
      authority,
      `${shellQuote(serverCLI)} --uninstall-extension ` +
        "codehelper.codehelper-vscode",
    ).catch(() => undefined);
  }
  await buildExtension(false).catch(() => undefined);
  stop();
  await remote(
    authority,
    `rm -rf ${shellQuote(remoteWorkspace)}`,
  ).catch(() => undefined);
  await rm(testOutput, { recursive: true, force: true });
  await rm(localRoot, { recursive: true, force: true });
}
const matrixRoot = join(extensionRoot, "dist", "matrix", "evidence");
await mkdir(matrixRoot, { recursive: true });
const matrixResult = process.env["CODEHELPER_MATRIX_RESULT"] ??
  join(
    matrixRoot,
    `remote-ssh-linux-arm64-${scenario}-${binarySource}.json`,
  );
await writeFile(matrixResult, `${JSON.stringify({
    schema_version: 1,
    job: `remote-ssh-linux-arm64-${scenario}-${binarySource}`,
    host: "remote-ssh",
    target: "linux-arm64",
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
      "disconnect_reattach",
      ...(scenario === "multi" ? ["root_remove_readd"] : []),
    ],
  }, null, 2)}\n`);
process.stdout.write("Remote SSH integration passed\n");
process.exit(0);

async function runRemoteWindow(isolatedResolver, resultPath) {
  const child = launchVSCode(isolatedResolver);
  let diagnostics = "";
  for (const stream of [child.stdout, child.stderr]) {
    stream.setEncoding("utf8");
    stream.on("data", (chunk) => {
      diagnostics = `${diagnostics}${chunk}`.slice(-(1 << 20));
    });
  }
  const exited = childExit(child);
  try {
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
          `Remote SSH window exited before tests completed: ` +
            `${JSON.stringify(state)}\n${diagnostics}`,
        );
      }
    }
    if (rawResult === undefined || rawResult.trim() === "") {
      throw new Error(`Remote SSH tests timed out\n${diagnostics}`);
    }
    const result = JSON.parse(rawResult);
    if (result?.ok !== true) {
      throw new Error(
        `Remote SSH tests failed: ${String(result?.error)}\n${diagnostics}`,
      );
    }
    await Promise.race([exited, delay(2_000)]);
    return result;
  } finally {
    await terminateChild(child, exited);
  }
}

function assertReconnect(first, second) {
  const firstSessions = new Map(
    (first.sessions ?? []).map((value) => [value.rootId, value.sessionId]),
  );
  if (firstSessions.size !== (scenario === "multi" ? 2 : 1) ||
    !Array.isArray(second.sessions) ||
    second.sessions.some((value) =>
      firstSessions.get(value.rootId) !== value.sessionId)) {
    throw new Error("Remote SSH reconnect did not preserve durable sessions");
  }
}

async function remote(host, command) {
  const result = await execFileAsync("ssh", [
    "-o", "BatchMode=yes",
    "-o", "ConnectTimeout=10",
    "-o", "ControlMaster=no",
    "-o", "ControlPath=none",
    host,
    command,
  ], {
    maxBuffer: 1 << 20,
    timeout: 30_000,
  });
  return result.stdout;
}

async function findRemoteServerCLI(host) {
  return (await remote(
    host,
    "find ~/.vscode-server/cli/servers -type f " +
      "-path '*/server/bin/code-server' -print0 2>/dev/null | " +
      "xargs -0 ls -t 2>/dev/null | head -n 1",
  ).catch(() => "")).trim();
}

function launchVSCode(isolatedResolver) {
  const workspaceArguments = scenario === "multi"
    ? [
        "--file-uri",
        `vscode-remote://ssh-remote+${authority}` +
          `${remoteWorkspace}/multi.code-workspace`,
      ]
    : [
        "--folder-uri",
        `vscode-remote://ssh-remote+${authority}${remoteWorkspace}`,
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

function writeBase64(path, content) {
  const encoded = Buffer.from(content, "utf8").toString("base64");
  return `printf %s ${shellQuote(encoded)} | base64 -d > ${shellQuote(path)}`;
}

function shellQuote(value) {
  return `'${value.replaceAll("'", "'\\''")}'`;
}

function delay(milliseconds) {
  return new Promise((resolve) => {
    setTimeout(resolve, milliseconds);
  });
}
