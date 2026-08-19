# Production Evaluation Findings Register

[Simplified Chinese](../zh-CN/production-evaluation-findings.md) | English

> Status: the H1-capable Harness is qualified; D1 Round 01 completed 56/56 and
> H1 Round 01 completed 21/21. No Product Candidate, full release pass, or
> Product Remediation is currently admitted.

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
| Foundation v2 F1-F3 and H1 Harness | qualified; v3 Harness frozen |
| Evaluation 17.4 | H1 passed; H2-H4 not started |
| Formal product findings | 0 |
| Historical product hypotheses | 4 |
| Trusted 17.4 passes | H1 Round 01 |
| Trusted 17.4 repairs | 0 |
| Open systemic Harness roots | 0 |
| Q1 Qualification | Round 07 passed; H1 Harness `frozen_qualified` |
| D1 Product Discovery | 56/56 passed; no Product Candidate |
| H1 Production Admission | 21/21 passed; no Product Candidate |

Machine decisions:

```text
evaluation/assessments/17.4-convergence-review-reset-01.json
evaluation/assessments/production-evaluation-independent-audit-01.json
evaluation/assessments/foundation-f1-f3-implementation-01.json
evaluation/assessments/q1-qualification-global-assessment-01.json
evaluation/assessments/q1-remediation-attribution-01.json
evaluation/assessments/q1-remediation-implementation-01.json
evaluation/assessments/q1-qualification-global-assessment-03.json
evaluation/assessments/d1-preflight-global-assessment-01.json
evaluation/assessments/d1-harness-remediation-01.json
evaluation/assessments/q1-qualification-global-assessment-06.json
evaluation/assessments/d1-product-discovery-global-assessment-01.json
evaluation/assessments/h1-preflight-global-assessment-26.json
evaluation/assessments/q1-qualification-global-assessment-07.json
evaluation/assessments/h1-production-admission-global-assessment-01.json
```

Q1 Round 06 qualified the D1-capable Harness after 8/8 Foundation tasks and
three 7/7 Integration runs. D1 then settled all 56 tasks, closing PEH-0025 and
the D1H-0001 through D1H-0003 Harness findings.

Q1 Round 07 qualified the H1-capable successor with 8/8 Foundation tasks and
three 7/7 Integration runs. H1 then settled all 21 tasks across Extension
Host, process, provider, persistence, and filesystem lanes. Both identity
checks and the owned-resource cleanup check passed; no Product Candidate was
admitted.

## 2. Product Hypotheses

These IDs preserve history only. D1 Round 01 did not rediscover them, so they
remain inadmissible for confirmation or repair.

| ID | Historical severity | Historical symptom | Current status |
| --- | --- | --- | --- |
| PEC-0001 | P1 | reconstructed Binding was not persisted after Extension Host reload | not rediscovered |
| PEC-0002 | P0 | accepted Turn remained active after fast Runtime restart | not rediscovered |
| PEC-0003 | P1 | first Tool after Fork entered recovery | not rediscovered |
| PEC-0004 | P1 | explicitly named Tool definition was omitted | not rediscovered |

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
findings. Q1 Round 06 verified the current Harness across three clean
Integration runs. Each
run passed all seven tasks and cleaned five Runtime processes plus four
temporary directories with zero outstanding resources. Q1C-0001a/b,
Q1C-0002a/b, PEH-0031, Q1G-0001/0002, and D1H-0001 through D1H-0003
are closed.

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

Q1, D1, and H1 are closed. No Product Candidate was admitted, so Product
Remediation remains prohibited. The next specification gate is explicit H2
admission authorization.

Current sequence:

1. Identity-bound attribution and Harness remediation are complete.
2. Q1 Round 06 froze the D1-capable v3 Candidate Lock.
3. D1 settled 36 Scenarios, 13 Fault Cases, five Host Cases, and two identity
   checks.
4. Q1 Round 07 froze the H1-capable successor Lock.
5. H1 settled all 21 tasks across five lanes.
6. No Product Candidate or Product Remediation was admitted.

The frozen Harness is authoritative only while its v3 input identity remains
unchanged.

## 7. Prohibited Actions

After H1 and before H2 admission:

- do not confirm or repair PEC-0001 through PEC-0004;
- do not restore removed 17.4 code;
- do not use current green counts as release evidence;
- do not close one micro-incident while a systemic Epoch remains open;
- do not change product code during Discovery;
- do not weaken a denominator, status, or assertion to obtain green.
