# Production Evaluation Implementation Plan

[Simplified Chinese](../zh-CN/production-evaluation-implementation-plan.md) | English

> Status: the remediated H3 Harness is frozen by Q1 Round 13. Same-Lock H1
> Round 03 passed 21/21, H2 Round 05 passed 16/16, and formal H3 Round 02
> passed 14/14 with an `admit` RC decision. H4 remains separately gated and
> unauthorized.

| Stage | Status |
| --- | --- |
| S0 | completed |
| F1 Contract, identity, admission, Runner | qualified |
| F2 privacy, promotion, Replay | qualified |
| F3 Oracles, Core Pack, Impact | qualified |
| Q1 Qualification and Freeze | H3-capable successor completed by Round 13; `frozen_qualified` |
| D1 Collect-all Product Discovery | completed 56/56; no Product Candidate |
| H1 VS Code and Process Chaos | same-Lock Round 03 completed 21/21 |
| H2 Live Model and Drift | same-Lock Round 05 completed 16/16 |
| H3 Endurance and Release | Round 02 completed 14/14; RC candidate admitted |
| H4 Canary and Incident Closure | not authorized |

Development validation is recorded in
`evaluation/assessments/foundation-f1-f3-implementation-01.json`. It does not
substitute for the Q1 Epoch or create a Harness Lock.

Q1 Round 01 is recorded in
`evaluation/assessments/q1-qualification-global-assessment-01.json`. Its
Foundation Epoch passed 8/8, but Integration-01 failed ACP Interop and timed
out in VS Code Runtime Integration. Runs 02 and 03 were correctly suppressed,
so the Candidate Lock has zero clean Integration runs.

Q1 remediation attribution and implementation are recorded in
`evaluation/assessments/q1-remediation-attribution-01.json` and
`evaluation/assessments/q1-remediation-implementation-01.json`. Focused
post-fix validation cannot be appended to the failed Lock; all changed inputs
entered immutable successor Epochs. The D1-capable successor passed Q1 Round
06 with 8/8 Foundation tasks and three 7/7 Integration runs. D1 then passed
56/56. Closure decisions are recorded in
`evaluation/assessments/q1-qualification-global-assessment-06.json` and
`evaluation/assessments/d1-product-discovery-global-assessment-01.json`.
H1 preflight then closed at 18/18, and the successor Harness passed Q1 Round
07 with 8/8 Foundation tasks and three 7/7 Integration runs. Formal H1 passed
21/21. These decisions are recorded in
`evaluation/assessments/h1-preflight-global-assessment-26.json`,
`evaluation/assessments/q1-qualification-global-assessment-07.json`, and
`evaluation/assessments/h1-production-admission-global-assessment-01.json`.
H2 preflight closed after one separated remediation cycle, and Q1 Round 08
froze the first H2-capable successor. Formal Rounds 01 and 02 each passed
11/12 Live samples. Schema-v2 failure evidence then entered Q1 Round 09. A
fixed 12-sample diagnostic matrix and 36-sample evidence-driven investigation
authorized one unchanged-policy re-entry; Round 03 passed 16/16. The immutable
decisions are in
`evaluation/assessments/h2-preflight-global-assessment-01.json` through
`-03.json`, `evaluation/assessments/q1-qualification-global-assessment-08.json`
through `-09.json`, `evaluation/assessments/h2-reentry-decision-01.json`,
`evaluation/assessments/h2-reentry-global-assessment-01.json`,
and `evaluation/assessments/h2-production-admission-global-assessment-01.json`
through `-03.json`.

H3 preflight then delivered the four-hour Endurance driver, fixed resource
slopes, Release/VS Code RC/Package lanes, and the eight-lane RC aggregator.
Formal Round 01 completed 480/480 Turns but failed the unchanged persistence
slope limit at 498,233 bytes/Turn. Separate assessment and remediation traced
the growth to cumulative Session Delta payloads in Terminal Envelopes
(H3P-0003), cumulative automatic Checkpoint content in CAS (H3P-0004), and a
transient sampler `ENOENT` race (H3H-0009). Deterministic bounded gzip
encoding and the narrow sampler repair reduced the 480-Turn verification slope
to 133,391 bytes/Turn without changing the threshold or denominator.

Because the product repair changed frozen inputs, Q1 restarted. Round 11 is
immutable invalid identity history; Round 12 failed one `evaluation-race`
command that passed three exact confirmations and was not reused. Round 13
passed 8/8 Foundation tasks and three consecutive 7/7 Integration runs. On
that Lock, H1 Round 03 passed 21/21 and H2 Round 05 passed 16/16. Formal H3
Round 02 then passed all 14 coordinator tasks, completed 480/480 Endurance
Turns, and admitted all eight required RC lanes. Decisions are recorded in
`evaluation/assessments/h3-production-admission-global-assessment-01.json`
through `-06.json`,
`evaluation/assessments/q1-qualification-global-assessment-11.json` through
`-13.json`, and the same-Lock H1/H2/H3 machine reports.

## 1. Execution Model

Three boundaries are intentionally different:

| Boundary | Meaning | May close a finding? |
| --- | --- | --- |
| Work Unit | one focused, reviewable PR or commit series | no |
| Qualification Epoch | one immutable set of Foundation Work Units tested together | yes, for Harness findings |
| Discovery or Verification Round | one collect-all product evaluation over a frozen Harness | yes, after Global Assessment |

The phrase "one Foundation batch" means one Qualification Epoch. It does not
mean one giant PR.

Rules:

1. Work Units may use focused tests for development feedback.
2. No Work Unit pass is a Foundation acceptance decision.
3. All Foundation Work Units enter the same first Qualification Epoch.
4. A failed Epoch closes without repair and proceeds to Global Assessment.
5. Repairs form a new immutable Epoch.
6. Product Discovery starts only after one Harness Lock completes three clean
   Integration qualification runs.
7. Product edits never occur in a Discovery Round.

## 2. Audit-to-Work Mapping

| Audit root | Corrective Work Units | Acceptance Gate |
| --- | --- | --- |
| command status self-certifies Oracles | F1.2, F3.1 | Oracle closure negative controls |
| Suite policy, requirements, and budgets are inert | F1.2 | Admission policy matrix |
| raw output and short secrets can persist | F2.1 | privacy bypass corpus |
| Corpus verification and promotion are fail-open | F2.1, F2.2 | batch rollback and full-file scan |
| timeout leaks descendants; Evidence can be stale | F1.3 | process-tree and freshness tests |
| Replay does not enter production paths | F2.3 | Provider/Runtime/Host Replay contracts |
| shared Fixture and optional fault matrix | F3.1, F3.2 | per-Scenario identity and mutation coverage |
| Impact may select nothing | F3.3 | unknown-path and self-change tests |
| Report identity and filenames collide | F1.1, F1.3 | mixed-partition and overwrite rejection |
| plan and documentation drift | S0, Q1 | mirrored docs and documentation gates |

## 3. Stage S0: Specification Closure

**Scope:** documentation and contract design only.

Deliver:

- approved technical specification and implementation plan in both languages;
- planned schema inventory and ownership;
- reset assessment and updated Findings Register;
- explicit decision that current `eval-*` commands remain diagnostic;
- approval record for the first Foundation Qualification Epoch.

Acceptance:

```bash
make docs-check
make book-check
git diff --check
```

Exit condition: explicit approval to begin Foundation Work Units.

Stop condition: any unresolved ambiguity in Evidence identity, Oracle closure,
Qualification Epoch semantics, privacy boundary, or production isolation.

Estimated effort: 0.5 to 1 engineer-week.

## 4. Stage F1: Contract, Identity, Admission, and Runner

### F1.1 Versioned Contracts and Identity

Deliver:

- strict version-2 Foundation, Scenario, Evidence, Oracle, Qualification, and
  Release Evidence schemas, plus a version-3 Harness Lock with explicit input
  roots;
- canonical Run partition and collision-free artifact naming;
- schema/Go parity tests and unknown-field rejection;
- mixed Source, Harness, Runtime, VSIX, Fixture, Provider, Model, Config, Seed,
  and Attempt rejection.

Negative controls:

- duplicate Run/Attempt;
- mixed Artifact or Environment identities;
- stale and cross-Attempt Evidence;
- existing artifact destination;
- schema-valid but semantically incomplete Evidence.

### F1.2 Effective Configuration and Admission

Deliver:

- unioned Suite/Scenario/Driver/lane requirements;
- strictest effective budgets;
- executable release policy, minimum-valid-run, and exception semantics;
- typed Driver dispatch;
- removal of command-status-to-Oracle projection.

Negative controls:

- missing Suite-only prerequisite;
- Suite budget stricter than Scenario budget;
- allowed `unavailable` affecting disposition but not Run truth;
- expired exception;
- P0 exception attempt;
- command exits zero while a required Oracle has no Evidence.

### F1.3 Runner Containment and Reports

Deliver:

- isolated per-Attempt directories and nonce-bound Evidence;
- Unix process groups and Windows Job Object ownership;
- bounded cancellation escalation and cleanup Evidence;
- sanitized stdout/stderr summaries and content digests;
- atomic, non-overwriting reports.

Negative controls:

- descendant survives direct-parent timeout;
- descendant writes after timeout;
- empty or pre-existing Evidence file;
- PID, port, socket, subscription, lock, or temporary-path leak;
- secret in stdout/stderr;
- output truncation with valid digest.

F1 acceptance:

- every negative control fails with the expected `invalid` or `failed` state;
- direct process, process-tree, evidence, and report tests pass under Race where
  applicable;
- no production package imports Evaluation.

Estimated effort: 2.5 to 3 engineer-weeks.

## 5. Stage F2: Evidence, Privacy, Promotion, and Replay

### F2.1 Privacy Admission

Deliver:

- enum/allowlist-based metadata admission;
- one conditional privacy contract shared by Go and JSON Schema;
- redaction before Evaluation persistence;
- full-artifact scanning for Evidence, manifest, report, index, and review
  receipt;
- sanitized error reporting that cannot echo rejected content.

Negative controls include short secrets, low-entropy internal credentials,
unknown key names, nested credentials, private paths, endpoints, multiline
content, binary input, malformed JSON, and double redaction.

### F2.2 Transactional Corpus Promotion

Deliver:

- `.tmp/evaluation/promotion/<batch-id>` staging;
- source digest recheck after reading;
- trusted producer or explicit synthetic source class;
- `promotion-review.json`;
- complete-batch atomic install and rollback;
- no direct tracked-Corpus default.

Negative controls:

- second-slice conflict after first-slice success;
- crash before install;
- source content changes between digest and read;
- missing review receipt;
- secret in manifest rather than Event data;
- extra unscanned file.

### F2.3 Replay Levels and Causal Closure

Deliver:

- separately typed Structural, Provider, Runtime, Host, and Crash Replay;
- production Provider adapter entry for frame mutations;
- production Runtime Operation entry with controlled Tools;
- ACP and official VS Code Host Replay;
- ancestor-closure causal slicer;
- per-Mutation applicability and execution ledger.

Negative controls:

- Structural Replay attempting to satisfy Runtime Replay;
- Provider Split with zero eligible Events;
- Delay that does not enter a controlled clock or transport;
- unknown Event bypassing the production compatibility boundary;
- causal slice missing an Effect, Journal, or Host ancestor.

F2 acceptance:

- tracked Corpus contains only reviewed, metadata-minimal assets;
- every required Mutation executes at least once;
- replay level is visible in every Run and cannot be upgraded by aggregation;
- Capture failure cannot alter business outcome.

Estimated effort: 2.5 to 3 engineer-weeks.

## 6. Stage F3: Oracles, Scenario Pack, and Impact

### F3.1 Evidence Adapters and Oracle Closure

Deliver:

- Runtime, Effect, Workspace, Verification, Persistence, Host, Security,
  Resource, and deterministic Task-quality Oracles;
- typed adapters from production facts to admitted Evidence;
- explicit proved-zero semantics;
- negative controls for every Oracle;
- stable attribution based on the first proven contract violation.

### F3.2 Scenario-specific Core Pack

Deliver:

- at least 30 independent Scenario families;
- one Fixture identity and Expected-fact set per Scenario;
- complete P0 invariant-to-Scenario-to-Oracle traceability;
- required fault and Mutation matrix;
- no family count based only on renamed copies of one Fixture.

Initial P0 families cover Terminal cardinality, durable waits, Effect
at-most-once, Guard and Approval binding, crash recovery, Outbox publication,
Host projection, workspace preservation, sandbox fail-closed, secrets, and
resource cleanup.

### F3.3 Fail-closed Impact Policy

Deliver:

- critical product-path coverage;
- full-P0 fallback for unmatched product paths;
- full Foundation selection for Evaluation self-change;
- explicit documentation-only exclusions;
- explainable selection output.

F3 acceptance:

- missing Evidence, optional-only mandatory Verification, empty fault matrix,
  reused Scenario truth, zero Mutation execution, and empty required Impact all
  fail;
- every Scenario is independently addressable by identity and digest;
- every P0 invariant has complete traceability.

Estimated effort: 2.5 to 3 engineer-weeks.

## 7. Stage Q1: Collect-All Qualification and Harness Freeze

### Q1.1 Collect-All Scheduler

Deliver:

- complete scheduling inventory;
- no first-failure cancellation of independent work;
- explicit dependency-blocked and infrastructure-canceled states;
- complete aggregate report.

### Q1.2 Foundation Qualification Epoch

Run all F1 through F3 contracts and negative controls as one immutable Epoch.
Focused reruns may diagnose, but only a complete rerun can qualify a repaired
Epoch.

Required validation:

```bash
go test -count=1 ./evaluation/...
go test -race -count=1 ./evaluation/...
make test-hermetic
make architecture-ratchet
make docs-check
make book-check
git diff --check
```

Add VS Code checks once the typed VS Code Driver enters the Epoch.

### Q1.3 Harness Freeze

Deliver `harness-lock.json`, production artifact scans, and three consecutive
complete Integration qualification passes with identical Source, Harness,
Runtime, and VSIX partitions.

Exit condition: `frozen_qualified`.

Stop condition: any drift, unavailable required capability, cleanup
uncertainty, or incomplete inventory.

Estimated effort: 1.5 to 2 engineer-weeks.

## 8. Stage D1: Collect-All Product Discovery

Precondition: frozen qualified Harness.

Status: completed by `product-discovery-d1-01`. The Round settled 36 Core
Scenarios, 13 Fault Cases, five Host Cases, and two identity checks. All 56
passed; no Product Candidate was admitted.

Run Stream, Approval, Input, Cancel, Resume, Multi-Agent, Session Lifecycle,
Reload, reconnect, and supported Host variants. Execute every required Attempt
even after failures.

Outputs:

- one complete Discovery report;
- product hypotheses, Harness incidents, and environment failures separated;
- one Global Assessment;
- approved Product Candidate list or a decision that no product repair is
  authorized.

No code repair occurs in D1.

Estimated effort: 1 engineer-week per complete Discovery and assessment cycle.

## 9. Stage R1: Product Remediation and Verification

Each approved Product Candidate receives:

- one focused product Work Unit in the owning package;
- one minimal regression tied to the frozen Scenario;
- no unrelated framework refactor;
- one complete Verification Round after all approved repairs settle.

A repair that changes Harness inputs invalidates the Harness Lock and returns
to Q1 before product conclusions resume.

Estimate depends on rediscovered Candidates and is not included in Foundation
effort.

## 10. Stages H1 to H4: Production Admission

| Stage | Deliverable | Estimate |
| --- | --- | ---: |
| H1 VS Code and Process Chaos | completed again on the H3 Lock: Round 03 passed 21/21 | completed |
| H2 Live Model and Drift | completed again on the H3 Lock: Round 05 passed 16/16 | completed |
| H3 Endurance and Release | completed: Round 02 passed 14/14 and admitted eight of eight RC lanes | completed |
| H4 Canary and Incident Closure | controlled inventory, rollout stop, rollback, incident-to-Corpus | 1 to 1.5 engineer-weeks |

H3 is complete. Its `validated-dry-run` candidate is not uploaded or
publishable and does not authorize H4 Canary or rollout expansion.

## 11. Revised Estimate and Critical Path

The previous "8 to 9 engineer-weeks remaining" estimate is withdrawn because
17.3 was invalidated and 17.1/17.2 require requalification.

| Scope | Engineer-weeks |
| --- | ---: |
| S0 specification | 0.5 to 1 |
| F1 contract and Runner | 2.5 to 3 |
| F2 privacy and Replay | 2.5 to 3 |
| F3 Oracles and Core Pack | 2.5 to 3 |
| Q1 qualification and Freeze | 1.5 to 2 |
| D1 first Product Discovery | 1 |
| H1 to H4 admission system | 8 to 8.5 |
| Total excluding unknown product repairs | 18.5 to 21.5 |

With two engineers, elapsed time is approximately 13 to 16 weeks because S0,
core identity contracts, the first Qualification Epoch, and Harness Freeze are
critical-path work. Parallelism begins only where contracts are stable.

## 12. Global Stop Conditions

Stop implementation and perform a Global Assessment when:

- new symptoms indicate a shared root not represented in the specification;
- the same boundary fails after two corrected Epochs;
- fixes move repeatedly between Runner, Fixture, Oracle, and product code;
- a gate becomes green by reducing its denominator or changing expected
  status;
- required evidence is replaced by logs or prose;
- cleanup, privacy, or production isolation cannot be proved;
- Source, Harness, Runtime, or VSIX identity drifts during a Round;
- current estimates no longer match observed throughput.

Stopping is a control action, not a failure to make progress.

## 13. Approval Boundaries

Approvals under this plan progressed through H3 completion.

Completed approvals and remaining explicit boundaries are:

1. Foundation implementation F1 through F3;
2. Qualification and Harness Freeze Q1;
3. Product Discovery D1;
4. approved Product Remediation R1;
5. production admission H1, H2, and H3 are complete;
6. H4 Canary and Incident Closure is not authorized.

No later approval is implied by an earlier one.
