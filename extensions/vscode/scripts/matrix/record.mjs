import { mkdir, writeFile } from "node:fs/promises";
import { join, resolve } from "node:path";

const [job, host, target, root, binarySource, ...scenarios] =
  process.argv.slice(2);
if ([job, host, target, root, binarySource].some(
  (value) => value === undefined || value === "",
)) {
  throw new Error(
    "usage: record.mjs JOB HOST TARGET ROOT SOURCE [SCENARIO...]",
  );
}
const outputRoot = resolve(
  process.env["CODEHELPER_MATRIX_ROOT"] ??
    join(import.meta.dirname, "..", "..", "dist", "matrix"),
);
const evidenceRoot = join(outputRoot, "evidence");
await mkdir(evidenceRoot, { recursive: true });
await writeFile(join(evidenceRoot, `${job}.json`), `${JSON.stringify({
  schema_version: 1,
  job,
  host,
  target,
  root,
  binary_source: binarySource,
  status: "passed",
  duration_ms: Number(process.env["CODEHELPER_MATRIX_DURATION_MS"] ?? "0"),
  scenarios,
}, null, 2)}\n`, { mode: 0o600 });
