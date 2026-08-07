import {
  mkdir,
  readFile,
  readdir,
  writeFile,
} from "node:fs/promises";
import { join, resolve } from "node:path";

const outputRoot = resolve(
  process.env["CODEHELPER_MATRIX_ROOT"] ??
    join(import.meta.dirname, "..", "..", "dist", "matrix"),
);
const evidenceRoot = join(outputRoot, "evidence");
const expected = [
  required("local-darwin-arm64-external", "macOS arm64 external single+multi"),
  required("local-darwin-arm64-bundled", "macOS arm64 bundled handshake"),
  required("local-darwin-x64-external", "macOS x64 external single+multi (Rosetta)"),
  required("update-integration", "signed update, rollback, revocation"),
  required("distribution", "universal and target VSIX distribution"),
  required("security", "extension security gate"),
  required("performance", "extension performance gate"),
  optional("local-win32-x64", "Windows x64 runner unavailable"),
];

const evidence = new Map();
for (const entry of await readdir(evidenceRoot, {
  withFileTypes: true,
}).catch(() => [])) {
  if (!entry.isFile() || !entry.name.endsWith(".json")) continue;
  const value = JSON.parse(await readFile(join(evidenceRoot, entry.name), "utf8"));
  if (value.schema_version !== 1 || typeof value.job !== "string" ||
    value.status !== "passed" || evidence.has(value.job)) {
    throw new Error(`invalid or duplicate matrix evidence ${entry.name}`);
  }
  evidence.set(value.job, value);
}
const jobs = expected.map((entry) => {
  const observed = evidence.get(entry.job);
  if (observed !== undefined) {
    return { ...entry, status: "passed", evidence: observed };
  }
  return {
    ...entry,
    status: entry.required ? "missing" : "unavailable",
  };
});
const missing = jobs.filter(
  (job) => job.required && job.status !== "passed",
);
const report = {
  schema_version: 1,
  generated_at: new Date().toISOString(),
  platform: `${process.platform}/${process.arch}`,
  status: missing.length === 0 ? "passed" : "incomplete",
  passed: jobs.filter((job) => job.status === "passed").length,
  required: jobs.filter((job) => job.required).length,
  missing: missing.map((job) => job.job),
  jobs,
};
await mkdir(outputRoot, { recursive: true });
await writeFile(
  join(outputRoot, "report.json"),
  `${JSON.stringify(report, null, 2)}\n`,
  { mode: 0o600 },
);
await writeFile(join(outputRoot, "report.md"), markdown(report), {
  mode: 0o600,
});
process.stdout.write(
  `VS Code matrix ${report.status}: ${String(report.passed)}/` +
    `${String(report.required)} required evidence present\n`,
);
if (missing.length > 0 &&
  process.env["CODEHELPER_MATRIX_ALLOW_INCOMPLETE"] !== "1") {
  process.exitCode = 1;
}

function required(job, description) {
  return { job, description, required: true };
}

function optional(job, description) {
  return { job, description, required: false };
}

function markdown(report) {
  const lines = [
    "# VS Code V3 E2E Matrix",
    "",
    `- Status: **${report.status}**`,
    `- Platform: \`${report.platform}\``,
    `- Required evidence: ${report.passed}/${report.required}`,
    "",
    "| Job | Required | Status | Description |",
    "| --- | --- | --- | --- |",
  ];
  for (const job of report.jobs) {
    lines.push(
      `| \`${job.job}\` | ${job.required ? "yes" : "no"} | ` +
        `${job.status} | ${job.description} |`,
    );
  }
  lines.push("");
  return `${lines.join("\n")}\n`;
}
