#!/usr/bin/env node

import {readFileSync, writeFileSync, mkdirSync} from "node:fs";
import {dirname, resolve} from "node:path";
import {pathToFileURL} from "node:url";

export function classifyMetric({baseline, current, maximum}) {
  if (baseline > maximum) {
    if (current > baseline) return "regressed";
    if (current > maximum) return "pre-existing";
    return "recovered";
  }
  return current > maximum ? "exceeded" : "ok";
}

export function compareSnapshots(snapshot, architecture, web, policies) {
  const findings = [];
  const baselineArchitecture = targetsByID(snapshot.architecture);
  const currentArchitecture = targetsByID(architecture);
  const architectureLimits = limitsByID(policies.architecture);

  for (const [id, baselineTarget] of baselineArchitecture) {
    const currentTarget = currentArchitecture.get(id);
    const limits = architectureLimits.get(id);
    if (!currentTarget || !limits) {
      findings.push({scope: "architecture", id, metric: "target", status: "missing"});
      continue;
    }
    for (const [metric, maximum] of Object.entries(limits)) {
      const baseline = baselineTarget.metrics[metric];
      const current = currentTarget.metrics[metric];
      if (!Number.isSafeInteger(baseline) || !Number.isSafeInteger(current)) {
        findings.push({scope: "architecture", id, metric, status: "missing"});
        continue;
      }
      findings.push({
        scope: "architecture",
        id,
        metric,
        baseline,
        current,
        maximum,
        status: classifyMetric({baseline, current, maximum})
      });
    }
  }

  for (const [metric, maximum] of Object.entries(
    policies.web.bundle_budgets ?? {}
  )) {
    const baseline = snapshot.web.bundle?.[metric];
    const current = web.bundle?.[metric];
    findings.push({
      scope: "web",
      id: "bundle",
      metric,
      baseline,
      current,
      maximum,
      status: Number.isSafeInteger(baseline) && Number.isSafeInteger(current)
        ? classifyMetric({baseline, current, maximum})
        : "missing"
    });
  }
  return findings;
}

function main(argv) {
  const [command, ...rest] = argv;
  const options = parseOptions(rest);
  const architecture = readJSON(required(options, "architecture-report"));
  const web = readJSON(required(options, "web-report"));

  if (command === "snapshot") {
    const output = required(options, "output");
    const snapshot = {
      version: 1,
      recorded_at: new Date().toISOString(),
      architecture,
      web
    };
    mkdirSync(dirname(resolve(output)), {recursive: true});
    writeFileSync(output, `${JSON.stringify(snapshot, null, 2)}\n`);
    printSnapshot(snapshot, options);
    return;
  }

  if (command === "check") {
    const snapshot = readJSON(required(options, "snapshot"));
    if (snapshot.version !== 1) fail("unsupported agent preflight snapshot");
    const policies = {
      architecture: readJSON(required(options, "architecture-policy")),
      web: readJSON(required(options, "web-policy"))
    };
    const findings = compareSnapshots(snapshot, architecture, web, policies);
    printFindings(findings);
    const failed = findings.filter(({status}) =>
      status === "regressed" || status === "exceeded" || status === "missing"
    );
    if (failed.length > 0) {
      fail(`fast ratchet failed with ${failed.length} regression(s)`);
    }
    process.stdout.write("Fast ratchet passed\n");
    return;
  }

  fail("usage: agent-ratchet.mjs snapshot|check [options]");
}

function printSnapshot(snapshot, options) {
  const architecturePolicy = options["architecture-policy"]
    ? readJSON(options["architecture-policy"])
    : {targets: []};
  const webPolicy = options["web-policy"]
    ? readJSON(options["web-policy"])
    : {bundle_budgets: {}};
  const self = {
    architecture: snapshot.architecture,
    web: snapshot.web
  };
  const findings = compareSnapshots(snapshot, self.architecture, self.web, {
    architecture: architecturePolicy,
    web: webPolicy
  });
  process.stdout.write(`Agent preflight recorded at ${options.output}\n`);
  printFindings(findings.filter(({status}) => status === "pre-existing"));
  printWebHeadroom(snapshot.web, webPolicy);
}

function printFindings(findings) {
  const visible = findings.filter(({status}) => status !== "ok");
  for (const finding of visible) {
    const values = Number.isSafeInteger(finding.current)
      ? ` ${finding.current}/${finding.maximum} (start ${finding.baseline})`
      : "";
    process.stdout.write(
      `[${finding.status}] ${finding.scope}:${finding.id}` +
      ` ${finding.metric}${values}\n`
    );
  }
}

function printWebHeadroom(web, policy) {
  const entries = Object.entries(policy.bundle_budgets ?? {})
    .map(([metric, maximum]) => ({
      metric,
      maximum,
      current: web.bundle?.[metric]
    }))
    .filter(({current}) => Number.isSafeInteger(current))
    .sort((left, right) =>
      (left.maximum - left.current) - (right.maximum - right.current)
    );
  for (const {metric, maximum, current} of entries) {
    const remaining = maximum - current;
    const ratio = remaining / maximum;
    const tone = remaining < 0 ? "EXCEEDED" : ratio <= 0.05 ? "CRITICAL" :
      ratio <= 0.15 ? "LOW" : "OK";
    process.stdout.write(
      `[budget:${tone}] web:${metric} ${current}/${maximum}` +
      ` (headroom ${remaining})\n`
    );
  }
}

function targetsByID(report) {
  return new Map((report.targets ?? []).map((target) => [target.id, target]));
}

function limitsByID(policy) {
  return new Map((policy.targets ?? []).map((target) => [target.id, target.limits]));
}

function parseOptions(args) {
  const options = {};
  for (let index = 0; index < args.length; index += 2) {
    const key = args[index];
    const value = args[index + 1];
    if (!key?.startsWith("--") || value === undefined) {
      fail(`invalid option ${key ?? ""}`);
    }
    options[key.slice(2)] = value;
  }
  return options;
}

function required(options, name) {
  const value = options[name];
  if (!value) fail(`--${name} is required`);
  return value;
}

function readJSON(path) {
  return JSON.parse(readFileSync(path, "utf8"));
}

function fail(message) {
  process.stderr.write(`${message}\n`);
  process.exit(1);
}

if (process.argv[1] &&
    import.meta.url === pathToFileURL(resolve(process.argv[1])).href) {
  main(process.argv.slice(2));
}
