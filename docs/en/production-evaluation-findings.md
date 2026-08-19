# Production Evaluation Findings Register

[Simplified Chinese](../zh-CN/production-evaluation-findings.md) | English

> Status: Foundation qualified and v3 Harness frozen. D1 Product Discovery is
> the next stage. No formal product finding, trusted 17.4 pass, or trusted
> 17.4 product repair is currently admitted.

This register follows the
[Technical Specification](./production-evaluation.md) and
[Implementation Plan](./production-evaluation-implementation-plan.md).
Discovery, Global Assessment, Remediation, and Verification are separate
Rounds.

## 1. Current Trust State

| Area | Status |
| --- | --- |
| Architecture Ratchet | independently trusted, 112/112 at audit time |
| Evaluation 17.1 | qualified as Foundation v2 input |
| Evaluation 17.2 | qualified as Foundation v2 input |
| Evaluation 17.3 | invalidated |
| Foundation v2 F1-F3 | qualified; v3 Harness frozen |
| Evaluation 17.4 | reset, not started |
| Formal product findings | 0 |
| Historical product hypotheses | 4 |
| Trusted 17.4 passes | 0 |
| Trusted 17.4 repairs | 0 |
| Open systemic Harness roots | 1, pending D1 collect-all evidence |
| Q1 Qualification | Round 03 passed; Harness `frozen_qualified` |
| Q1 Remediation | verified and closed by Round 03 |

Machine decisions:

```text
evaluation/assessments/17.4-convergence-review-reset-01.json
evaluation/assessments/production-evaluation-independent-audit-01.json
evaluation/assessments/foundation-f1-f3-implementation-01.json
evaluation/assessments/q1-qualification-global-assessment-01.json
evaluation/assessments/q1-remediation-attribution-01.json
evaluation/assessments/q1-remediation-implementation-01.json
evaluation/assessments/q1-qualification-global-assessment-03.json
```

Q1 Round 03 qualified F1 through F3 and froze one identity-bound v3 Harness
after 8/8 Foundation tasks and three 7/7 Integration runs. It closes all
Foundation roots except PEH-0025, whose Discovery-specific collect-all
behavior still requires D1 evidence.

## 2. Product Hypotheses

These IDs preserve history only. They must be rediscovered under one frozen
Collect-all Product Discovery Round before confirmation or repair.

| ID | Historical severity | Historical symptom | Current status |
| --- | --- | --- | --- |
| PEC-0001 | P1 | reconstructed Binding was not persisted after Extension Host reload | candidate |
| PEC-0002 | P0 | accepted Turn remained active after fast Runtime restart | candidate |
| PEC-0003 | P1 | first Tool after Fork entered recovery | candidate |
| PEC-0004 | P1 | explicitly named Tool definition was omitted | candidate |

No historical repair is retained as product evidence.

## 3. Systemic Harness Roots

| ID | Severity | Root | Representative evidence | Required closure |
| --- | --- | --- | --- | --- |
| PEH-0023 | P1 | Core Scenario and Oracle false assurance | shared Fixture; zero Provider Split; pure-function 500 Replay; empty Impact success | F2.3, F3.1 to F3.3 |
| PEH-0024 | P1 | incomplete Fixture request contracts | routed/default streams and fragments were not fully validated | F2.3, F3.2 |
| PEH-0025 | P1 | fail-fast Discovery and incomplete recovery proof | one Host or fault failure suppressed later evidence | Q1.1, D1 |
| PEH-0026 | P1 | missing machine Freeze and evidence consistency | stale links, ambiguous batch semantics, no frozen identity partition | S0, F1.1, Q1.3 |
| PEH-0027 | P1 | Evaluation controls cross production boundaries | test authority and Fixture Control were not isolated from production | F1.1, Q1.3 |
| PEH-0028 | P1 | execution and admission verdicts are not closed | command status copied to Oracles; Suite policy, requirement, and budget are inert | F1.2, F3.1 |
| PEH-0029 | P1 | Run containment and Evidence identity are incomplete | descendant leak; empty/stale Evidence; report identity and filenames collide | F1.1, F1.3 |
| PEH-0030 | P1 | privacy and Corpus promotion fail open | raw output persisted; short secrets survive; manifest unscanned; batch half-commit | F2.1, F2.2 |
| PEH-0031 | P1 | Integration cleanup evidence is incomplete | Round 01 left three directories; remediation now binds PID/path ownership and rejects outstanding cleanup | new Q1 Epoch |

New symptoms are grouped under these roots unless a Global Assessment proves a
new independent root.

## 4. Independent Audit Result

The 2026-08-19 independent audit started from the current branch, plan,
implementation, and gates. Two independent validators confirmed all 12
candidates.

| Audit ID | Root | Result |
| --- | --- | --- |
| C1 | PEH-0028 | command exit status can produce evidence-free P0 Oracle passes |
| C2 | PEH-0028 | Suite admission contracts are validated but not executed |
| C3 | PEH-0030 | raw stdout/stderr enters private reports without redaction |
| C4 | PEH-0030 | privacy and Corpus manifest contracts fail open |
| C5 | PEH-0030 | promotion lacks trusted provenance and batch atomicity |
| C6 | PEH-0029 | Runner does not own the process tree or fresh Evidence |
| C7 | PEH-0023 | Structural Replay does not exercise production Runtime or Host |
| C8 | PEH-0023 | 36 Scenario names self-check one shared Fixture |
| C9 | PEH-0023 | unmatched critical paths return an empty successful selection |
| C10 | PEH-0029 | reports allow mixed identity and artifact overwrite |
| C11 | PEH-0026 | batch semantics and remaining-effort estimate were inconsistent |
| C12 | PEH-0026 | missing reset documents broke `make docs-check` |

Positive diagnostic commands did not invalidate the findings. Negative probes
proved that green aggregate counts could coexist with missing evidence,
unexecuted Mutations, empty Impact, privacy bypass, and inert Suite policy.

## 5. Disposition

Q1 Round 01 closed with one passed Foundation Epoch and one invalid
Integration Run. The approved remediation attribution separated its symptoms:

| ID | Domain | Attribution | Candidate remediation |
| --- | --- | --- | --- |
| Q1C-0001a | Product ACP contract | optional empty Session title reached a non-empty lifecycle invariant | default the ACP title before persistence |
| Q1C-0001b | Product Runtime adapter | Cancel arrived after Start admission but before Engine Scope activation and was rejected | map pre-Scope control to the existing coordinator-not-active sentinel |
| Q1C-0002a | Harness Fixture | Approval policy named a stale Tool or was not forwarded through typed Runtime arguments | bind explicit `file_apply`/`quality_verify` ask rules |
| Q1C-0002b | Harness Fixture | Editor Context had no structured completion stream | add the required `turn_complete` stream |
| PEH-0031 | Harness lifecycle | Node timeout did not execute async cleanup and Q1 had no owned-resource result | bound waits plus identity-bound cleanup evidence |

These are Q1 qualification findings, not formal 17.4 Product Discovery
findings. Q1 Round 03 verified them across three clean Integration runs. Each
run passed all seven tasks and cleaned five Runtime processes plus four
temporary directories with zero outstanding resources. Q1C-0001a/b,
Q1C-0002a/b, PEH-0031, Q1G-0001, and Q1G-0002 are closed.

May be retained after negative requalification:

- strict JSON decoding;
- Source and dirty-content digest;
- bounded output collection;
- private atomic file writes;
- canonical Evidence encoding and hash-chain integrity.

Must be replaced or redesigned:

- verdict projection from command status;
- validation-only admission policy;
- unbound Evidence files;
- direct tracked-Corpus promotion;
- generic metadata string allowlisting;
- shared Scenario truth;
- optional fault and Mutation coverage;
- empty Impact success;
- metadata-only flake claims;
- Attempt-only artifact names.

## 6. Re-entry Rule

Q1 is closed and the v3 Harness is frozen qualified. The next stage is D1
Collect-all Product Discovery. Product Remediation remains prohibited.

Current sequence:

1. Identity-bound attribution and remediation are complete.
2. Q1 Round 03 passed one immutable Foundation Qualification Epoch.
3. The v3 Candidate Lock completed three clean Integration runs with identical
   identity and became `frozen_qualified`.
4. D1 starts with the frozen Lock and collect-all semantics.

The frozen Harness is authoritative only while its v3 input identity remains
unchanged.

## 7. Prohibited Actions

Throughout Product Discovery:

- do not confirm or repair PEC-0001 through PEC-0004;
- do not restore removed 17.4 code;
- do not use current green counts as release evidence;
- do not close one micro-incident while a systemic Epoch remains open;
- do not change product code during Discovery;
- do not weaken a denominator, status, or assertion to obtain green.
