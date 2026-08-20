# CodeHelper Production Evaluation

English | [Simplified Chinese](./README.zh-CN.md)

`evaluation/` owns all tracked production-evaluation code and assets.
Transient run evidence is written to `.tmp/evaluation/` and never mixed with
source assets.

## Layout

```text
evaluation/
  cmd/codehelper-eval/   Evaluation CLI
  d2/                    Independent D2 contracts, planner, Lock, and CLI
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

The H3-capable Harness is frozen under Q1 Round 13. On that exact Lock,
same-Lock H1 Round 03 passed 21/21, H2 Round 05 passed 16/16, and formal H3
Round 02 passed 14/14 with all eight RC lanes admitted. The implementation is
release-authoritative for that local `validated-dry-run` RC candidate
partition only. H2 Rounds 01/02 and H3 Round 01 remain immutable failures; H4
Canary and rollout expansion remain unauthorized. The H4 Harness and
controlled 120-Turn preflight are complete. Q1 Round 14, same-Lock H1 Round
04, and H2 Round 06 passed; required H3 Round 03 was operator-stopped at
96/480 Turns and is non-reusable, so formal H4 was not started.

D2.1 through D2.3 are implemented as an independent
non-release-authoritative control plane. Historical Round 05 settled 129/129
Cases: 35 passed, 93 were classified as Harness Incidents, and one Live
observation remained Unattributed. Driver execution remediation is now
qualified by `complex-discovery-d2-drivers-26`: its 105-Case successor closes
376/376 pairwise interactions and 18/18 checks, including ordered Journey
execution, Live CLI routing, and exact topology-to-Driver routing. No Product
Candidate was admitted by that shallow round. The Semantic/In-path loop then
closed Round 10 with 20/20 settled, 17 passed, three Exact-seed Product
Candidates, and zero Harness Incidents. `thread.compact`, `thread.fork`, and
`turn.revert` can each block later cancellation while a Turn is parked on
approval. Product Remediation and D2.4 re-entry remain unauthorized.

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
H3 failure, remediation, requalification, and final admission are recorded in
`assessments/h3-production-admission-global-assessment-01.json` through
`-06.json` and `assessments/q1-qualification-global-assessment-11.json`
through `-13.json`.
The D2.3 closure and Driver remediation decision are recorded in
`assessments/d2-campaign-global-assessment-01.json` and
`assessments/d2-driver-remediation-global-assessment-01.json`.
Semantic convergence and the three Product Candidates are recorded in
`assessments/d2-semantic-global-assessment-01.json` and
`assessments/d2-product-candidate-0001.json` through `-0003.json`.

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

Formal H3 Round 01 completed 480/480 Turns but failed the unchanged
persistence slope limit. Separate remediation bounded cumulative Session
Delta and Checkpoint content, then Q1 Round 13 passed 8/8 Foundation tasks and
three consecutive 7/7 Integration runs. Same-Lock H1 Round 03 and H2 Round 05
passed before formal H3 Round 02 completed 480/480 Endurance Turns and
admitted Foundation, Integration, Chaos, Live, Endurance, Release, VS Code RC,
and Package evidence. The RC candidate is not uploaded and does not authorize
H4.

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

Check the D2.1 Campaign and deterministic inventory:

```bash
go run ./evaluation/d2/cmd/codehelper-discovery check --root .
```

Create one immutable D2 Qualification Epoch against an existing frozen base
Lock:

```bash
go run ./evaluation/d2/cmd/codehelper-discovery qualify \
  --root . \
  --id complex-discovery-d2-foundation-01 \
  --base-lock .tmp/evaluation/q1/foundation-v2-q1-14/harness-lock.json \
  --output .tmp/evaluation/d2/complex-discovery-d2-foundation-01
```

Qualify the remediated D2 Drivers and Generators against the frozen production
artifacts:

```bash
go run ./evaluation/d2/cmd/codehelper-discovery qualify-drivers \
  --root . \
  --id complex-discovery-d2-drivers-36 \
  --base-lock .tmp/evaluation/q1/foundation-v2-q1-14/harness-lock.json \
  --runtime bin/codehelper \
  --vsix extensions/vscode/dist/codehelper-vscode-0.0.1.vsix \
  --output .tmp/evaluation/d2/complex-discovery-d2-drivers-36
```

The final closed Semantic Campaign is reproduced only against its exact Lock:

```bash
go run ./evaluation/d2/cmd/codehelper-discovery semantic-campaign \
  --root . \
  --id complex-discovery-d2-semantic-10 \
  --discovery-lock .tmp/evaluation/d2/complex-discovery-d2-drivers-36/discovery-lock.json \
  --runtime bin/codehelper \
  --output .tmp/evaluation/d2/complex-discovery-d2-semantic-10
```

The command below is historical Round 05 evidence. Do not substitute the
successor Lock or execute another Campaign without explicit approval:

```bash
go run ./evaluation/d2/cmd/codehelper-discovery campaign \
  --root . \
  --id complex-discovery-d2-campaign-05 \
  --discovery-lock .tmp/evaluation/d2/complex-discovery-d2-drivers-09/discovery-lock.json \
  --plan .tmp/evaluation/d2/complex-discovery-d2-drivers-09/campaign-plan.json \
  --inventory .tmp/evaluation/d2/complex-discovery-d2-drivers-09/driver-inventory.json \
  --runtime bin/codehelper \
  --vsix extensions/vscode/dist/codehelper-vscode-0.0.1.vsix \
  --live \
  --output .tmp/evaluation/d2/complex-discovery-d2-campaign-05
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
