import { readdir, rm } from "node:fs/promises";
import { build } from "esbuild";
import { spawn } from "node:child_process";
import { join } from "node:path";

const outputDirectory = join(".tmp-tests", String(process.pid));
await rm(outputDirectory, { force: true, recursive: true });

const entryPoints = [];
async function collectTests(directory) {
  const entries = await readdir(directory, { withFileTypes: true });
  for (const entry of entries) {
    const path = join(directory, entry.name);
    if (entry.isDirectory()) {
      await collectTests(path);
    } else if (entry.isFile() && entry.name.endsWith(".test.ts")) {
      entryPoints.push(path);
    }
  }
}
await collectTests("src");
const selector = process.argv[2];
const selectedEntryPoints = selector === undefined
  ? entryPoints
  : entryPoints.filter((path) => path.includes(`/${selector}/`));
if (selectedEntryPoints.length === 0) {
  throw new Error("no TypeScript tests found");
}

await build({
  entryPoints: selectedEntryPoints,
  outdir: outputDirectory,
  outbase: "src",
  bundle: true,
  format: "cjs",
  platform: "node",
  sourcemap: true,
  target: "node20",
  logLevel: "silent",
});

const child = spawn(
  process.execPath,
  ["--test", ...selectedEntryPoints.map((path) =>
    `${outputDirectory}/${path.slice("src/".length, -".ts".length)}.js`
  )],
  { stdio: "inherit" },
);

const exitCode = await new Promise((resolve, reject) => {
  child.once("error", reject);
  child.once("exit", (code, signal) => {
    if (signal !== null) {
      reject(new Error(`test process terminated by ${signal}`));
      return;
    }
    resolve(code ?? 1);
  });
});
await rm(outputDirectory, { force: true, recursive: true });
process.exitCode = exitCode;
