import { copyFile, mkdir } from "node:fs/promises";

import { build } from "esbuild";

await mkdir("dist", { recursive: true });
await Promise.all([
  build({
    entryPoints: ["src/extension.ts"],
    outfile: "dist/extension.js",
    bundle: true,
    external: ["vscode"],
    format: "cjs",
    platform: "node",
    sourcemap: true,
    target: "node20",
    define: {
      __CODEHELPER_TEST_BUILD__: "false",
    },
    logLevel: "info",
  }),
  build({
    entryPoints: ["src/chat/webview/client.ts"],
    outfile: "dist/chat-webview.js",
    bundle: true,
    format: "iife",
    platform: "browser",
    sourcemap: false,
    target: "es2022",
    logLevel: "info",
  }),
  build({
    entryPoints: ["src/chat/webview/mermaid-renderer.ts"],
    outfile: "dist/mermaid-renderer.js",
    bundle: true,
    format: "iife",
    platform: "browser",
    sourcemap: false,
    target: "es2022",
    logLevel: "info",
  }),
  copyFile(
    "src/chat/webview/styles.css",
    "dist/chat-webview.css",
  ),
]);
