# CodeHelper Production Evaluation

English | [Simplified Chinese](./README.zh-CN.md)

`evaluation/` owns all tracked production-evaluation code and assets.
Transient run evidence is written to `.tmp/evaluation/` and never mixed with
source assets.

## Layout

```text
evaluation/
  cmd/codehelper-eval/   Evaluation CLI
  internal/spec/         Manifest, Scenario, Run, and Policy contracts
  internal/source/       Source commit and dirty digest
  internal/runner/       Process-tree-owned execution, Attempts, and bound evidence
  internal/report/       Stable JSON and Markdown reports
  internal/foundation/   Foundation inventory, Oracle/Mutation catalogs, and digest
  internal/evidence/     Unified Evidence Envelope and hash chain
  internal/capture/      Capture adapters, redaction, and causal slicing
  internal/replay/       Deterministic Replay and Mutation
  internal/corpus/       Corpus promotion, validation, and Replay admission
  internal/oracle/       Nine provenance-bound semantic Oracles and fault injection
  internal/corepack/     Core Scenario Pack, impact mapping, and admission
  fixtures/oracle/       Oracle semantic fact fixtures
  schema/                JSON Schemas
  scenarios/             Versioned scenarios
  corpus/                Redacted tracked Replay Corpus
  assessments/           Machine-readable Round closure and decisions
  spec/                  Foundation, Oracle, and Mutation machine contracts
  manifest.json          Suite and release-policy entry point
```

Later Oracle, Fault, VS Code Driver, and Endurance work also belongs under this
top-level directory. Production Runtime, Provider, Tool, and Host packages
must not import `evaluation/`.

## Candidate Capability

The implementation below is not release-authoritative. Foundation v2 and the
failure-evidence-capable H2 Harness are qualified under Q1 Round 09. D1 passed
56/56, H1 passed 21/21, and H2 re-entry Round 03 passed 16/16. Rounds 01 and
02 remain immutable 14/16 failures. H3-H4 and full release admission remain
unauthorized.

The corrected target contracts and execution order are defined by:

- `docs/en/production-evaluation.md`;
- `docs/en/production-evaluation-implementation-plan.md`;
- `docs/en/production-evaluation-findings.md`.

The independent audit decision is
`assessments/production-evaluation-independent-audit-01.json`.
The F1-F3 implementation result is
`assessments/foundation-f1-f3-implementation-01.json`.
Q1 Round 01 and its failed Freeze decision are recorded in
`assessments/q1-qualification-global-assessment-01.json`.
Q1 remediation attribution and candidate implementation are recorded in
`assessments/q1-remediation-attribution-01.json` and
`assessments/q1-remediation-implementation-01.json`.
Q1 Round 03 and the successful Freeze decision are recorded in
`assessments/q1-qualification-global-assessment-03.json`.
The D1-capable successor and Discovery closure are recorded in
`assessments/q1-qualification-global-assessment-06.json` and
`assessments/d1-product-discovery-global-assessment-01.json`.
H1 preflight closure, the H1-capable successor Lock, and formal H1 closure are
recorded in `assessments/h1-preflight-global-assessment-26.json`,
`assessments/q1-qualification-global-assessment-07.json`, and
`assessments/h1-production-admission-global-assessment-01.json`.
H2 preflight, requalification, and the immutable formal decisions are recorded
in `assessments/h2-preflight-global-assessment-01.json` through `-03.json`,
`assessments/q1-qualification-global-assessment-08.json` through `-09.json`,
`assessments/h2-reentry-decision-01.json`,
`assessments/h2-reentry-global-assessment-01.json`, and
`assessments/h2-production-admission-global-assessment-01.json` through
`-03.json`.

The F1 implementation now provides:

- strict JSON Schema validation and unknown-field rejection;
- strongly typed Suite, Scenario, Run, Oracle, and Policy contracts;
- duplicate-ID, missing-Oracle, empty-denominator, and invalid-exception
  checks;
- build-time Source provenance plus version-3 Harness input-root identity;
- Suite/Scenario effective admission, immutable Run partition, process-tree
  timeout, output digests, fresh Evidence binding, and collision-free reports;
- separate first and recovery Attempts;
- byte-stable JSON and Markdown reports;
- atomic report writes with mode `0600`.

The F2 implementation now provides:

- VS Code Runtime Capture, Provider Event, and Observation Journal adapters;
- relative time, stable aliases, causal edges, and SHA-256 hash chains;
- metadata-only redaction plus credential, absolute-path, and high-entropy
  scanning;
- full-trace, Operation, and incomplete ACP Request causal slices;
- reviewed, whole-batch atomic promotion into private staging;
- delay, duplicate, truncate, interruption, unknown, malformed, and provider
  split mutations;
- 11 redacted traces derived from two real VS Code and DeepSeek runs plus one
  safe synthetic Provider trace that makes split coverage executable.

The F3 implementation now contains:

- deterministic Runtime, Effect, Workspace, Verification, Persistence, Host,
  Security, Resource, and Task-quality Oracles;
- stable Failure Signatures and seven attribution domains, with Harness failure
  kept distinct;
- 36 core Scenario Families, including 18 P0 scenarios with explicit
  scenario-specific Oracle closure;
- 13 fault-injection admissions covering all nine Oracles, including duplicate Effect, double Terminal,
  stuck Running, Receipt Drift, Replay Drift, Guard Bypass, and related
  boundaries;
- 22 changed-path Impact Rules with full-P0 fallback;
- a 500-run Replay command over 12 Corpus entries.

Q1 Rounds 01, 02, and 04 remain immutable failure and governance history. The
D1-capable successor passed Q1 Round 06, and D1 settled all 56 tasks. The
H1-capable successor passed Q1 Round 07 and H1 settled all 21 tasks. The
H2-capable successor passed Q1 Round 08. After Rounds 01 and 02 each settled
14/16, schema-v2 failure evidence entered Q1 Round 09 under Lock
`sha256:b2f944b8...`. The fixed diagnostic matrix and evidence-driven
investigation authorized one re-entry; formal H2 Round 03 passed 16/16.

The previous Phase 17.4 implementation was reset and removed. The current H1
implementation was rebuilt and requalified under the successor process. The
reset decision remains in `assessments/17.4-convergence-review-reset-01.json`.

Run candidate diagnostics:

```bash
make eval-contract-check
make eval-foundation-check
make eval-replay
make eval-oracle
```

Run the first-milestone self-check Scenario:

```bash
go run ./evaluation/cmd/codehelper-eval run \
  --root . \
  --suite evaluation-contract \
  --scenario contract-self-check \
  --run-id local-contract-check
```

Reports default to `.tmp/evaluation/<run-id>/`.

Select core scenarios for changed paths:

```bash
go run ./evaluation/cmd/codehelper-eval impact select \
  --path internal/security/policy/policy.go \
  --path internal/persist/state/store.go
```

Promote a local private Capture into a candidate Corpus:

```bash
go run ./evaluation/cmd/codehelper-eval capture promote \
  --input /private/path/runtime-capture.jsonl \
  --format vscode_runtime_capture_v1 \
  --prefix candidate-id \
  --batch candidate-batch \
  --review /private/path/promotion-review.json
```

Review and verify candidates under `.tmp/evaluation/` before moving them into
`evaluation/corpus/`. Raw Captures never enter Git.

## Boundaries

- The CLI drives versioned Scenarios and does not implement an Agent loop.
- Evaluation code cannot bypass a Host, Runtime, or Guard to execute business
  Tools.
- Capture, Evidence, and Report failure cannot change the evaluated business
  result.
- Tracked Scenarios contain no credentials, real user paths, or unauthorized
  workspace content.
- New states use only `passed`, `failed`, `unavailable`, `not_evaluated`, or
  `invalid`.
- Planned commands remain explicitly marked as unimplemented and never appear
  as shipped capability.
