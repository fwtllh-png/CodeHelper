import { createHash } from "node:crypto";
import { execFile } from "node:child_process";
import { lstat, readFile } from "node:fs/promises";
import { join } from "node:path";
import { promisify } from "node:util";

const execute = promisify(execFile);

export async function sourceIdentity(root) {
  const status = (await execute("git", [
    "status", "--porcelain=v1", "--untracked-files=all",
  ], { cwd: root, maxBuffer: 20 << 20 })).stdout;
  const files = (await execute("git", [
    "ls-files", "-c", "-o", "--exclude-standard", "-z",
  ], {
    cwd: root,
    encoding: "buffer",
    maxBuffer: 50 << 20,
  })).stdout.toString("utf8").split("\0").filter(Boolean).sort();
  const hash = createHash("sha256");
  for (const file of files) {
    hash.update(file);
    hash.update("\0");
    const path = join(root, file);
    const info = await lstat(path).catch(() => undefined);
    if (info === undefined) {
      hash.update("deleted\0");
      continue;
    }
    hash.update(String(info.mode));
    hash.update("\0");
    hash.update(await readFile(path));
    hash.update("\0");
  }
  return {
    state: status === "" ? "clean" : "dirty",
    fingerprint: hash.digest("hex"),
  };
}
