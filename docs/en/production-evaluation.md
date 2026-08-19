# Production Evaluation Technical Specification

[Simplified Chinese](../zh-CN/production-evaluation.md) | English

> Status: Foundation v2 and the H2-capable Harness are qualified under Q1
> Round 08. D1 passed 56/56 and H1 passed 21/21. H2 formal Rounds 01 and 02
> each settled 16 tasks at 14/16 because Multi-Agent live quality did not meet
> policy. H2, H3-H4, and full release admission remain prohibited.
>
> Execution order and estimates are maintained in the
> [Production Evaluation Implementation Plan](./production-evaluation-implementation-plan.md).
> Current trust and defects are maintained in the
> [Findings Register](./production-evaluation-findings.md).

## 1. Decision and Trust State

The target quality model remains valid, but the current Evaluation
implementation is not release-authoritative:

| Area | Current status | Permitted use |
| --- | --- | --- |
| 17.1 Contract and command Runner | qualified as Foundation v2 input | frozen Harness use |
| 17.2 Capture and structural Replay | qualified as Foundation v2 input | frozen Harness use |
| 17.3 Oracle and Core Pack | invalidated | no admission evidence |
| Foundation v2 F1-F3 and D1 Harness | qualified by Q1 Round 06 | frozen Harness authority |
| D1 Product Discovery | passed 56/56; no Product Candidate | no Product Remediation |
| H1-capable Harness | qualified by Q1 Round 07 | frozen Harness authority |
| 17.4 VS Code and Process Chaos | H1 passed 21/21 | H1 evidence only; no H2-H4 authority |
| H2-capable Harness | qualified by Q1 Round 08 | frozen Harness authority |
| Live Model and Drift | two formal Rounds failed 14/16 | blocks H2 and H3-H4 |
| Product hypotheses PEC-0001 to PEC-0004 | not rediscovered in D1 | remain historical only |

`make eval-contract-check`, `make eval-replay`, and `make eval-oracle` remain
diagnostic commands. A green result from them cannot approve a product change,
close a Harness finding, or satisfy a release gate.

The current re-entry decision is:

```text
Specification -> Foundation Work Units -> one Qualification Epoch
              -> Global Assessment -> Harness Freeze
              -> Collect-all Product Discovery -> Global Assessment
              -> approved Product Remediation -> Chaos/Live/RC admission
```

No phase may infer authority from the existence of code or from a successful
command exit.

## 2. Goals and Non-Goals

### 2.1 Goals

The system must:

1. evaluate the real production Runtime, Guard, Tool, Persistence, and Host
   paths without a second business loop;
2. derive every verdict from typed evidence rather than copying a process exit
   status into named Oracles;
3. bind Source, Harness, Runtime, VSIX, Fixture, Provider, Model, configuration,
   seed, and Attempt into one immutable Run identity;
4. fail closed when required evidence, Mutation execution, Impact coverage,
   cleanup proof, or identity binding is absent;
5. collect all independent results before returning an aggregate failure;
6. preserve the first Attempt and distinguish recovery from first-attempt
   success;
7. protect credentials and private content before Evaluation persistence;
8. freeze one qualified Harness digest before any product conclusion;
9. turn confirmed incidents into permanent, redacted, deterministic regression
   assets;
10. produce release decisions that reject failed, unavailable, invalid, stale,
    mismatched, and incomplete evidence.

### 2.2 Non-Goals

The system does not:

- treat metadata Replay as Runtime or Host Replay;
- treat repeated execution of a pure in-memory function as a flake test;
- allow an LLM judge to decide Runtime, Security, Persistence, or Effect
  invariants;
- make one shared success Fixture represent unrelated Scenario families;
- bypass Host, Guard, Approval, Constitution, Journal, or Sandbox;
- write a private Capture directly into the tracked Corpus;
- hide first-attempt failure behind retry;
- repair a product while the same Discovery or Qualification Round is open;
- make intermediate Work Unit tests acceptance evidence for the full
  Foundation.

## 3. Normative Principles

The words **MUST**, **MUST NOT**, **REQUIRED**, and **INVALID** are normative.

1. **Evidence before verdict.** Every Oracle result MUST reference admitted
   Evidence IDs. A command result may be Evidence for a declared Verification
   command, but it cannot be copied into unrelated Oracle results.
2. **Independent facts.** Each Scenario MUST identify its Fixture, expected
   facts, Evidence requirements, Oracles, and Mutations. Shared primitives are
   allowed; shared Scenario truth is not.
3. **Production authority.** Evaluation orchestrates supported production
   entry points. Production packages MUST NOT import `evaluation`.
4. **Fail closed.** Missing required evidence, an empty required selection, an
   untriggered fault, an unexecuted declared Mutation, or cleanup uncertainty
   is `invalid`, never `passed`.
5. **Collect all.** Independent Runs and fault cases MUST continue after a
   failure. Aggregate failure is returned only after all schedulable work has
   settled.
6. **Immutable identity.** Reports MUST NOT combine Runs with mismatched
   identity partitions. Artifact filenames MUST be collision-free.
7. **Privacy before persistence.** Raw command output, secrets, user paths, and
   conversation content MUST NOT enter reports, tracked Corpus, or Evaluation
   logs.
8. **No self-certification.** A producer cannot be the only authority that both
   creates and validates the same semantic claim.
9. **Qualification Epoch, not giant PR.** Foundation implementation may use
   independently reviewable Work Units. They become trusted only when the
   complete frozen set passes one Qualification Epoch.
10. **No same-round repair.** A failed Epoch or Discovery Round closes with a
    Global Assessment before any repair begins.

## 4. Corrected Architecture

```text
Versioned Foundation Specification
  |-- Scenario contracts
  |-- Evidence requirements and Oracle closure
  |-- Mutation and Impact policies
  |-- Privacy, cleanup, and isolation contracts
  `-- Admission policy
                         |
                  Evaluation Coordinator
             /             |               \
       Typed Drivers   Collect-all       Fault Controller
       CLI/ACP/VSIX      Scheduler       Proxy/Kill/Test-FS
             \             |               /
               Production Runtime and Guard
                         |
       Events / Facts / Receipts / Host / Workspace
                         |
          Evidence Admission and Provenance Binder
                         |
          Independent Oracles and Negative Controls
                         |
    Qualification Report -> Harness Lock -> Release Evidence
```

### 4.1 Ownership

| Path | Responsibility |
| --- | --- |
| `evaluation/spec` | planned Foundation, Scenario, Oracle, Mutation, Impact, and admission contracts |
| `evaluation/internal/runner` | typed Driver dispatch, Attempt lifecycle, process isolation, and bounded capture |
| `evaluation/internal/evidence` | identity, provenance, admission, canonical encoding, and digests |
| `evaluation/internal/oracle` | independent semantic verdicts over admitted Evidence |
| `evaluation/internal/replay` | explicitly labeled Structural, Provider, Runtime, Host, and Crash Replay |
| `evaluation/internal/qualification` | Collect-all scheduling, negative controls, and Epoch reports |
| `evaluation/internal/freeze` | Harness Lock construction and drift rejection |
| `evaluation/internal/report` | collision-free private artifacts and aggregate decision |
| `evaluation/vscode` | isolated official VS Code Extension Host Driver |
| `.tmp/evaluation` | private raw Capture, staging, run output, and disposable evidence |

These paths are planned ownership boundaries. Their mention does not claim that
they currently exist.

### 4.2 Production Isolation

- Evaluation code may import supported Host and Runtime contracts.
- Production Go, TypeScript, scripts, generated assets, and packages MUST NOT
  import or bundle Evaluation control code.
- Crash Points and Fixture Control require test-only authority and MUST be
  absent from production binaries and VSIX packages.
- A production artifact scan is a mandatory Freeze input.

## 5. Machine Contract Set

Implementation MUST create strict, versioned schemas for the following
artifacts. Unknown fields are rejected.

| Artifact | Purpose |
| --- | --- |
| `foundation.json` | root specification and required contract inventory |
| `scenario.json` | one executable Scenario and its expected facts |
| `oracle-contract.json` | required Evidence, absence policy, cardinality, and cross-checks |
| `mutation-contract.json` | applicable input class, expected observation, and minimum executions |
| `impact-policy.json` | changed-path rules, critical fallbacks, and uncovered-path policy |
| `harness-lock.json` | canonical digest inputs for the frozen Harness |
| `qualification.json` | complete Epoch inventory, identities, results, and decision |
| `promotion-review.json` | privacy and correctness approval for Corpus promotion |
| `release-evidence.json` | Source-bound lane evidence consumed by release admission |

### 5.1 Scenario Contract

Every Scenario MUST declare:

```json
{
  "schema_version": 2,
  "id": "vscode-approval-runtime-restart",
  "family": "approval-recovery",
  "risk": "P0",
  "driver": "vscode-electron",
  "fixture_id": "approval-restart-v1",
  "run_plan": {"attempts": 3, "collect_all_group": "host-recovery"},
  "expected_facts": ["approval_parked", "runtime_killed", "terminal_once"],
  "required_evidence": ["runtime_events", "effect_ledger", "host_projection"],
  "required_oracles": ["runtime", "effect", "persistence", "host", "resource"],
  "required_mutations": ["kill_after_approval_park"],
  "cleanup_contract": "vscode-process-tree-v1",
  "impact_tags": ["runtime", "host", "interaction", "persistence"]
}
```

The contract compiler MUST reject:

- a Scenario without a distinct `fixture_id` and expected-fact set;
- a P0 invariant without at least one responsible Oracle;
- a required Oracle whose Evidence closure is incomplete;
- a declared Mutation with no compatible Driver or injection point;
- a Scenario with no cleanup owner;
- a Scenario whose selected Driver does not enter a supported production path.

### 5.2 Evidence Identity and Provenance

Every Evidence record MUST contain or inherit:

```text
RunID, AttemptID, ScenarioID, SourceIdentity, HarnessIdentity,
RuntimeArtifactIdentity, HostArtifactIdentity, FixtureIdentity,
ProviderIdentity, ModelIdentity, ConfigIdentity, Seed, Producer,
EvidenceKind, EvidenceDigest
```

The canonical Run partition is:

```text
SHA256(
  schema_version || source || dirty_digest || harness_digest ||
  runtime_digest || host_digest || scenario_digest || fixture_digest ||
  provider_digest || model_digest || config_digest || seed || attempt
)
```

Rules:

- Evidence without the exact Run partition is rejected.
- Evidence files live in a fresh per-Attempt directory created before process
  start and cannot be reused.
- Producers write a temporary file and atomically seal it with a final digest.
- Empty files do not satisfy `required_evidence`.
- Report aggregation rejects mixed partitions.
- Artifact names include Scenario, Variant, Attempt, and Run partition; an
  existing artifact is an error, not an overwrite target.

### 5.3 Oracle Closure

An Oracle contract defines:

- admitted Evidence kinds and producer identities;
- required and optional cardinality;
- whether a proved zero value is valid;
- the status for absent, malformed, stale, or mismatched Evidence;
- cross-Evidence comparisons;
- responsible failure domains;
- negative controls that prove the Oracle detects its target defect.

`evidence_available: true` without provenance is not Evidence.

| Oracle | Required proof |
| --- | --- |
| Runtime | accepted Operation, ordered Events/Facts, Terminal or durable Park owner/deadline, Lease and Resume binding |
| Effect | Effect claim, Guard/Approval binding, Journal or external counter, execution/result cardinality |
| Workspace | before/after tree digests, expected and forbidden paths, preservation of pre-existing dirty state |
| Verification | exact Scenario-declared mandatory commands, exit/signal/timeout, output digest, execution identity |
| Persistence | Event/Fact rebuild, Snapshot, Receipt, Terminal, Outbox, reopen and recovery identity |
| Host | ACP cursor, visible items, waits, Terminal, reload/reconnect projection and continued operation |
| Security | Guard, Policy, Approval, Constitution, Sandbox, egress, secret scan, and fail-closed outcome |
| Resource | process identities, process groups, FDs, subscribers, ports, temporary paths, queues, RSS and persistence slope |
| Task quality | deterministic task assertions first; calibrated LLM judge only for non-P0 explanation quality |

Applicability is resolved when the Scenario contract is compiled. A Run does
not emit a synthetic `passed` for an inapplicable Oracle.

### 5.4 Admission Status

The allowed result states remain:

| State | Meaning |
| --- | --- |
| `passed` | executed; all required evidence and assertions passed |
| `failed` | executed; product or environment behavior violated a contract |
| `unavailable` | a declared non-product capability was unavailable |
| `not_evaluated` | the approved Run plan excluded this item |
| `invalid` | Harness, identity, evidence, selection, injection, cleanup, or privacy contract failed |

For P0 required work, only `passed` is admissible. `unavailable`,
`not_evaluated`, and `invalid` never reduce the denominator and never become
success through an exception.

## 6. Runner and Collect-All Execution

### 6.1 Effective Configuration

The Runner MUST compute one effective configuration:

- requirements are the union of Suite, Scenario, Driver, and lane
  requirements;
- budgets are the strictest applicable limits;
- exceptions are applied only by the Admission Evaluator;
- `minimum_valid_runs`, repetitions, and Attempt limits are enforced after
  Collect-all settlement;
- a nonblocking policy affects release disposition, not the truth of Run
  status.

### 6.2 Process and Workspace Isolation

Each Attempt receives isolated workspace, HOME, state, extension, user-data,
port, socket, Evidence, and report directories.

On timeout, cancel, or shutdown, the Runner MUST:

1. stop new child creation;
2. terminate the complete process group or platform Job Object;
3. wait for settlement with a bounded escalation;
4. verify no owned PID, port, socket, subscription, lock, or temporary path
   remains;
5. emit cleanup Evidence;
6. mark the Attempt `invalid` if ownership or cleanup is uncertain.

Raw stdout and stderr are bounded private Capture inputs. Reports retain only a
sanitized summary, byte counts, truncation status, and content digest.

### 6.3 Collect-All Scheduler

- Independent Runs, Hosts, Attempts, and fault cases are scheduled before
  aggregate evaluation.
- One failure does not cancel unrelated work.
- Dependency-blocked work is reported explicitly.
- Infrastructure cancellation records every unscheduled or interrupted item.
- The scheduler returns one complete inventory and never reports success with
  missing required items.

## 7. Replay, Mutation, and Causality

Replay levels are separate capabilities:

| Level | Required execution |
| --- | --- |
| Structural Replay | validate canonical Evidence, hash chain, identity, and causal graph |
| Provider Replay | pass recorded frames through the production Provider adapter |
| Runtime Replay | submit Operations to the production Runtime with controlled Tools |
| Host Replay | drive ACP or VS Code projection, cursor, wait, and reconnect behavior |
| Crash Replay | kill a process at a named durable boundary and recover the same state |

A lower level MUST NOT satisfy a higher-level Scenario.

Every Mutation contract declares applicability and expected observation.
Qualification reports per-Mutation eligible count, executed count, detected
count, rejected-invalid count, and skipped reason. A required Mutation with
zero eligible or executed instances is `invalid`.

Provider split Mutation preserves reconstructable frame bytes and enters the
production parser. Delay uses a controlled clock or transport. Unknown and
malformed inputs enter the compatibility boundary they are intended to test.

Causal slicing computes the ancestor closure of the target failure across ACP,
Runtime, Provider, Effect, Journal, and Host identities. Identity filtering
alone is not a causal slice.

## 8. Impact Selection

Impact policy MUST include:

- critical mappings for `cmd`, `internal/config`, Runtime protocol, Providers,
  MCP, Tools, Security, Persistence, Orchestration, Hosts, VS Code, scripts,
  schemas, `go.mod`, `go.sum`, and release workflows;
- a full-P0 fallback for a valid but unmatched product path;
- full Foundation selection when Evaluation contracts, Oracles, Fixtures,
  Mutation logic, or the Impact policy itself changes;
- explicit exclusion rules for documentation-only changes;
- a report of matched rules and the reason for every selected Scenario.

An empty selection for a required lane is `invalid`.

## 9. Privacy and Corpus Promotion

### 9.1 Data Boundary

- Raw Capture remains in an authorized local private directory with mode
  `0600` and bounded retention.
- Capture producers redact credentials and restricted content before writing
  Evaluation-readable files whenever the source format permits it.
- Evaluation never copies raw input into logs, errors, reports, or Corpus.
- Allowed metadata values use enums or allowlists, not a generic
  protocol-looking-string bypass.
- Secret scanning covers every promoted file, including manifests, reports,
  indexes, and Evidence.
- Go validation and JSON Schema encode the same conditional privacy rules.

### 9.2 Promotion Transaction

Promotion always targets `.tmp/evaluation/promotion/<batch-id>` first.
Tracked Corpus has no direct-write default.

One batch is admitted only when:

1. all slices canonicalize and Replay at their declared level;
2. all files pass Schema, path, credential, entropy, and restricted-content
   scans;
3. Source digest is rechecked after reading to prevent source substitution;
4. source class is proven by a trusted producer or marked synthetic;
5. a human privacy/correctness review emits `promotion-review.json`;
6. the complete batch is atomically installed;
7. a failure leaves no promoted subset.

Hash chains prove internal integrity, not source authenticity. Provenance and
review receipts provide the trust root.

## 10. Harness Qualification and Freeze

### 10.1 Foundation Qualification Epoch

The Epoch input is one immutable set of Work Unit commits. It executes:

- contract and Schema positive/negative tests;
- all Scenario-specific Fixtures and expected facts;
- Oracle negative controls and cross-Evidence checks;
- all declared Mutations with nonzero execution;
- Impact known, unknown, critical, and self-change cases;
- process-tree timeout, cancellation, and cleanup;
- privacy bypass and Corpus batch rollback tests;
- report collision, stale Evidence, and mixed-identity tests;
- production artifact isolation scans;
- Race tests for concurrent components.

Any failure closes the Epoch as failed and triggers one Global Assessment.
Focused repairs do not occur inside the failed Epoch.

### 10.2 Harness Lock

`harness-lock.json` includes canonical digests for:

- all Schemas, contracts, Fixtures, prompts, routed and default Provider
  streams, request fragments, assertions, Drivers, adapters, fault controls,
  cleanup contracts, config, build tags, and test authority;
- Evaluation binary, Runtime binary, VSIX, and production-isolation scan;
- build-time Source provenance and toolchain identity.

Harness Lock v3 also records normalized `input_roots`. Live identity
verification re-enumerates those roots and requires the complete path/digest
set to match. This rejects changed, added, or removed executable inputs.
Post-qualification Assessments and mirrored governance documents are outside
the executable input roots, so publishing them does not mutate the qualified
candidate identity. They cannot change Harness code, Runtime source,
Fixtures, tests, schemas, or build inputs without invalidating the Lock.

The same lock MUST pass three complete Integration qualification runs. Any
input change creates a new lock and resets the count.

Only then may the Harness status become `frozen_qualified`.

## 11. Product Discovery and Remediation

After Freeze:

1. run every required Host, Scenario, Attempt, and fault case;
2. retain all results, including later failures after the first failure;
3. close the Round without product edits;
4. perform one Global Assessment and group symptoms by systemic root;
5. approve or reject Product Candidates;
6. repair approved Candidates in separate product Work Units;
7. add a minimal regression tied to the frozen Scenario contract;
8. run a new Verification Round with the same Harness Lock or an explicitly
   requalified successor.

Historical PEC IDs are hypotheses until rediscovered under this process.

## 12. Release Lanes and Admission

| Lane | Required evidence | Authority |
| --- | --- | --- |
| Foundation contract | Schemas, negative controls, privacy, identity | blocks Foundation qualification |
| P0 Replay | affected Provider/Runtime/Host/Crash Replay | blocks merge after Freeze |
| Integration | real binary, ACP, official VS Code, Fixture Provider | blocks main after Freeze |
| Chaos | process, network, SQLite, filesystem, concurrency matrix | blocks release |
| Live | repeated model matrix, drift, cost, task quality | blocks release |
| Endurance | four-hour workload and resource slopes | blocks RC |
| RC | source/runtime/VSIX/Harness-bound aggregate | blocks candidate |
| Canary | controlled rollout and rollback signals | blocks expansion |

Release admission requires:

- P0 invariants: 100%;
- duplicate consequential Effects: 0;
- missing or multiple Terminals: 0;
- ownerless Running or Parked state: 0;
- Guard, Sandbox, privacy, and production-isolation violations: 0;
- required `unavailable`, `not_evaluated`, `invalid`, unmatched Impact, and
  unexecuted Mutation: 0;
- unattributed P0/P1: 0;
- exact Source, Harness, Runtime, VSIX, Provider, Model, and configuration
  partition match;
- no stale or expired evidence.

Statistical Live thresholds, cost budgets, and Endurance slopes are versioned
policy. They cannot offset a hard invariant.

## 13. Disposition of Current Assets

The following implementation ideas may be retained only after negative
requalification:

- strict JSON decoding and unknown-field rejection;
- Source commit and dirty-content digest;
- bounded output collection;
- mode-`0600` atomic file writes;
- canonical Evidence encoding and SHA-256 chain validation.

The following behavior MUST be replaced:

- copying command status into all Oracle results;
- treating Suite policy and budgets as validation-only data;
- accepting empty, stale, or unbound Evidence;
- direct tracked-Corpus promotion;
- generic string allowlisting for privacy;
- Scenario families sharing one truth Fixture;
- optional-only Verification passing a mandatory contract;
- optional or empty fault coverage;
- empty Impact success;
- metadata-only 500-run Replay as a flake gate;
- report filenames keyed only by Attempt.

Current green counts remain historical diagnostics and are not migration
baselines.

## 14. Specification Acceptance

This specification is ready for implementation approval only when:

1. the English and Chinese documents are reviewed as one change;
2. the Implementation Plan maps every audit root to a Work Unit and Gate;
3. all planned machine artifacts have an owner, producer, consumer, and
   fail-closed rule;
4. the Qualification Epoch and PR Work Unit distinction is accepted;
5. the reset assessment and Findings Register agree with this trust state;
6. `make docs-check`, `make book-check`, and `git diff --check` pass.

D1 and H1 completion do not authorize full release admission. H2 failed its
versioned Live threshold, so H3-H4 remain blocked until explicit H2 re-entry.
