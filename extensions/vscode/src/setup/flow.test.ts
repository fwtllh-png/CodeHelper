import assert from "node:assert/strict";
import test from "node:test";

import {
  actionableChecks,
  decodeReadiness,
  repairMessage,
  runtimeFailureCheck,
  setupArguments,
} from "./flow.js";

void test("decodeReadiness preserves structured repair details", () => {
  const report = decodeReadiness({
    status: "blocked",
    checks: [
      {
        id: "runtime.binary",
        status: "blocked",
        reason: "binary unavailable",
        impact: "runtime cannot start",
        action: "configure codehelper.binaryPath",
      },
      {
        id: "workspace.config",
        status: "ready",
        reason: "configuration loaded",
      },
    ],
  });

  assert.equal(report.status, "blocked");
  assert.deepEqual(actionableChecks(report).map((check) => check.id), [
    "runtime.binary",
  ]);
  const first = report.checks[0];
  assert.ok(first !== undefined);
  assert.match(repairMessage(first), /Impact: runtime cannot start/u);
  assert.match(repairMessage(first), /Action: configure/u);
});

void test("runtimeFailureCheck maps host failures to repair actions", () => {
  const binary = runtimeFailureCheck("CodeHelper binary was not found");
  assert.equal(binary.status, "blocked");
  assert.match(binary.action ?? "", /binaryPath/u);

  const config = runtimeFailureCheck("runtime config path is invalid");
  assert.match(config.action ?? "", /run CodeHelper Setup/u);
});

void test("decodeReadiness rejects malformed and unknown statuses", () => {
  assert.throws(
    () => decodeReadiness({ status: "unknown", checks: [] }),
    /readiness output is invalid/u,
  );
  assert.throws(
    () => decodeReadiness({
      status: "ready",
      checks: [{ id: "bad", status: "ready" }],
    }),
    /readiness check is invalid/u,
  );
});

void test("setupArguments writes only a credential reference", () => {
  const args = setupArguments({
    workspace: "/workspace",
    configPath: "/workspace/codehelper.toml",
    provider: "openai",
    model: "gpt-5",
    credentialKind: "env",
    credentialName: "OPENAI_API_KEY",
    force: true,
  });

  assert.deepEqual(args, [
    "setup",
    "--workspace", "/workspace",
    "--config", "/workspace/codehelper.toml",
    "--profile", "recommended",
    "--provider", "openai",
    "--model", "gpt-5",
    "--credential-kind", "env",
    "--credential-name", "OPENAI_API_KEY",
    "--json",
    "--force",
  ]);
  assert.equal(args.includes("secret-value"), false);
});
