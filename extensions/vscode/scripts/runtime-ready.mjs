import { spawn } from "node:child_process";
import { mkdtemp, mkdir, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { dirname, join, resolve } from "node:path";
import { performance } from "node:perf_hooks";
import { createInterface } from "node:readline";
import { clearTimeout, setTimeout } from "node:timers";

const binary = process.env["CODEHELPER_VSCODE_BINARY"];
const fixture = process.env["CODEHELPER_VSCODE_FIXTURE"];
if (binary === undefined) {
  throw new Error("CODEHELPER_VSCODE_BINARY is required");
}
if (fixture === undefined) {
  throw new Error("CODEHELPER_VSCODE_FIXTURE is required");
}
const samples = [];
for (let index = 0; index < 7; index++) {
  samples.push(await measure(index));
}
samples.sort((left, right) => left - right);
const p50 = percentile(samples, 0.50);
const p95 = percentile(samples, 0.95);
if (p95 >= 5_000) {
  throw new Error(`Runtime ready p95 ${p95.toFixed(1)}ms exceeds 5000ms`);
}
const report = {
  schema_version: 1,
  samples_ms: samples.map((value) => Number(value.toFixed(1))),
  p50_ms: Number(p50.toFixed(1)),
  p95_ms: Number(p95.toFixed(1)),
};
const reportPath = process.env["CODEHELPER_RUNTIME_READY_REPORT"] ??
  join(process.cwd(), "dist", "performance", "runtime-ready.json");
await mkdir(dirname(reportPath), { recursive: true });
await writeFile(reportPath, `${JSON.stringify(report, null, 2)}\n`);
process.stdout.write(`${JSON.stringify(report)}\n`);

async function measure(index) {
  const root = await mkdtemp(join(tmpdir(), "codehelper-ready-"));
  const workspace = join(root, "workspace");
  const data = join(root, "state");
  await mkdir(workspace);
  await mkdir(data);
  const child = spawn(resolve(binary), [
    "host", "--adapter", "acp",
    "--data-dir", data,
    "--workspace", workspace,
    "--posture", "never",
    "--max-steps", "1",
    "--provider-fixture", resolve(fixture),
  ], {
    cwd: workspace,
    stdio: ["pipe", "pipe", "pipe"],
    windowsHide: true,
  });
  const lines = createInterface({ input: child.stdout });
  const pending = new Map();
  let nextID = 0;
  lines.on("line", (line) => {
    const frame = JSON.parse(line);
    const resolveResponse = pending.get(String(frame.id));
    if (resolveResponse !== undefined) {
      pending.delete(String(frame.id));
      resolveResponse(frame);
    }
  });
  const request = (method, params = {}) => {
    const id = String(++nextID);
    const response = new Promise((resolveResponse) => {
      pending.set(id, resolveResponse);
    });
    child.stdin.write(JSON.stringify({ jsonrpc: "2.0", id, method, params }) + "\n");
    return response;
  };
  const started = performance.now();
  try {
    await request("initialize", {
      protocolVersion: 2,
      clientInfo: { name: "codehelper-vscode-performance", version: String(index) },
    });
    await request("session/new", { cwd: workspace, title: "Performance" });
    return performance.now() - started;
  } finally {
    const exited = new Promise((resolveExit) => child.once("exit", resolveExit));
    child.stdin.write(JSON.stringify({
      jsonrpc: "2.0", id: String(++nextID), method: "shutdown", params: {},
    }) + "\n");
    child.stdin.end();
    let killTimer;
    await Promise.race([
      exited,
      new Promise((resolveTimeout) => {
        killTimer = setTimeout(() => {
          child.kill("SIGKILL");
          resolveTimeout(undefined);
        }, 5_000);
      }),
    ]);
    clearTimeout(killTimer);
    lines.close();
    await rm(root, { recursive: true, force: true });
  }
}

function percentile(values, quantile) {
  const index = Math.min(values.length - 1, Math.ceil(values.length * quantile) - 1);
  return values[index];
}
