import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { runVSCodeCommand } from "@vscode/test-electron";

const extensionRoot = resolve(import.meta.dirname, "..");
const vsix = join(extensionRoot, "dist", "codehelper-vscode-0.0.1.vsix");
const root = await mkdtemp(join(tmpdir(), "codehelper-vsix-install-"));
const extensions = join(root, "extensions");
const data = join(root, "data");
const common = [
  "--extensions-dir", extensions,
  "--user-data-dir", data,
];
const options = {
  version: process.env["CODEHELPER_VSCODE_TEST_VERSION"] ?? "1.96.4",
  spawn: {
    env: {
      ...process.env,
      VSCODE_PORTABLE: join(root, "portable"),
    },
  },
};
try {
  await runVSCodeCommand([
    "--install-extension", vsix, "--force", ...common,
  ], options);
  const result = await runVSCodeCommand([
    "--list-extensions", "--show-versions", ...common,
  ], options);
  if (!result.stdout.split(/\r?\n/u).includes("codehelper.codehelper-vscode@0.0.1")) {
    throw new Error(`installed extension was not listed:\n${result.stdout}`);
  }
} finally {
  await rm(root, { recursive: true, force: true });
}
