import {
  mkdir,
  readFile,
  readdir,
  writeFile,
} from "node:fs/promises";
import { join, resolve } from "node:path";

import { matrixJobs as expected } from "./jobs.mjs";
import { journeyEvidence } from "./journeys.mjs";

const extensionRoot = resolve(import.meta.dirname, "..", "..");
const outputRoot = resolve(
  process.env["CODEHELPER_MATRIX_ROOT"] ??
    join(import.meta.dirname, "..", "..", "dist", "matrix"),
);
const evidenceRoot = join(outputRoot, "evidence");
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
const observedJourneys = new Set(
  [...evidence.values()].flatMap((entry) =>
    Array.isArray(entry.journeys)
      ? entry.journeys.filter((value) => typeof value === "string")
      : []),
);
const manualEvidence = await readFile(
  join(extensionRoot, "RELEASE-EVIDENCE.md"),
  "utf8",
).catch(() => "");
const journeys = journeyEvidence.map((journey) => ({
  ...journey,
  status: journey.kind === "automated"
    ? observedJourneys.has(journey.id) ? "passed" : "missing"
    : manualEvidence.includes(`\`${journey.id}\``)
      ? "documented"
      : "missing",
}));
const missingJourneys = journeys.filter((journey) => journey.status === "missing");
const report = {
  schema_version: 1,
  generated_at: new Date().toISOString(),
  platform: `${process.platform}/${process.arch}`,
  status: missing.length === 0 && missingJourneys.length === 0
    ? "passed"
    : "incomplete",
  passed: jobs.filter((job) => job.status === "passed").length,
  required: jobs.filter((job) => job.required).length,
  missing: missing.map((job) => job.job),
  journey_status: missingJourneys.length === 0 ? "passed" : "incomplete",
  missing_journeys: missingJourneys.map((journey) => journey.id),
  journeys,
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
if ((missing.length > 0 || missingJourneys.length > 0) &&
  process.env["CODEHELPER_MATRIX_ALLOW_INCOMPLETE"] !== "1") {
  process.exitCode = 1;
}

function markdown(report) {
  const lines = [
    "# VS Code Local E2E Matrix",
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
  lines.push(
    "",
    "## Journey Evidence",
    "",
    "| Journey | Kind | Status | Description |",
    "| --- | --- | --- | --- |",
  );
  for (const journey of report.journeys) {
    lines.push(
      `| \`${journey.id}\` | ${journey.kind} | ${journey.status} | ` +
        `${journey.description} |`,
    );
  }
  lines.push("");
  return `${lines.join("\n")}\n`;
}
