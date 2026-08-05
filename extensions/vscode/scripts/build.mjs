import { build } from "esbuild";

await build({
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
});
