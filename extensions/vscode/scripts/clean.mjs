import { rm } from "node:fs/promises";

await Promise.all([
  rm("dist", { force: true, recursive: true }),
  rm(".tmp-tests", { force: true, recursive: true }),
  rm(".tmp-electron", { force: true, recursive: true }),
  rm(".vscode-test", { force: true, recursive: true }),
]);
