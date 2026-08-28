import assert from "node:assert/strict";
import test from "node:test";
import {classifyMetric, compareSnapshots} from "./agent-ratchet.mjs";

test("allows an unchanged pre-existing violation", () => {
  assert.equal(classifyMetric({
    baseline: 966,
    current: 966,
    maximum: 954
  }), "pre-existing");
});

test("rejects a worsening pre-existing violation", () => {
  assert.equal(classifyMetric({
    baseline: 966,
    current: 967,
    maximum: 954
  }), "regressed");
});

test("allows recovery and budget consumption within the strict limit", () => {
  assert.equal(classifyMetric({
    baseline: 966,
    current: 950,
    maximum: 954
  }), "recovered");
  assert.equal(classifyMetric({
    baseline: 900,
    current: 950,
    maximum: 954
  }), "ok");
});

test("compares architecture and Web measurements with the same semantics", () => {
  const findings = compareSnapshots(
    {
      architecture: {
        targets: [{id: "model", metrics: {production_lines: 966}}]
      },
      web: {bundle: {css_raw_bytes: 99000}}
    },
    {
      targets: [{id: "model", metrics: {production_lines: 967}}]
    },
    {bundle: {css_raw_bytes: 100001}},
    {
      architecture: {
        targets: [{
          id: "model",
          limits: {production_lines: 954}
        }]
      },
      web: {bundle_budgets: {css_raw_bytes: 100000}}
    }
  );

  assert.deepEqual(
    findings.map(({scope, status}) => [scope, status]),
    [["architecture", "regressed"], ["web", "exceeded"]]
  );
});

test("accepts an architecture target with an explicit retirement", () => {
  const findings = compareSnapshots(
    {
      architecture: {
        targets: [{id: "removed", metrics: {production_lines: 100}}]
      },
      web: {bundle: {}}
    },
    {targets: []},
    {bundle: {}},
    {
      architecture: {
        targets: [],
        retirements: {removed: "The subsystem was intentionally removed."}
      },
      web: {bundle_budgets: {}}
    }
  );

  assert.deepEqual(findings, []);
});
