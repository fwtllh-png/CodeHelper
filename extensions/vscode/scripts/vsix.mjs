import { mkdir } from "node:fs/promises";
import { join, resolve } from "node:path";
import { createVSIX, listFiles } from "@vscode/vsce";

const extensionRoot = resolve(import.meta.dirname, "..");
const output = join(extensionRoot, "dist", "codehelper-vscode-0.0.1.vsix");
const files = await listFiles({
  cwd: extensionRoot,
  packagedDependencies: [],
});
const forbidden = files.filter((file) =>
  file.startsWith("src/") ||
  file.startsWith("scripts/") ||
  file.startsWith("node_modules/") ||
  file.endsWith(".map") ||
  file.includes(".env")
  || file.startsWith("dist/") && ![
    "dist/extension.js",
    "dist/chat-webview.js",
    "dist/chat-webview.css",
  ].includes(file)
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
  "RELEASE-EVIDENCE.md",
  "SECURITY.md",
  "PRIVACY.md",
  "SUPPORT.md",
  "LICENSE",
]) {
  if (!files.includes(required)) {
    throw new Error(`VSIX is missing ${required}`);
  }
}
await mkdir(join(extensionRoot, "dist"), { recursive: true });
await createVSIX({
  cwd: extensionRoot,
  packagePath: output,
  dependencies: false,
  allowMissingRepository: true,
});
process.stdout.write(`${output}\n`);
