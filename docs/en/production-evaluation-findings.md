# Production Evaluation Findings Register

[Simplified Chinese](../zh-CN/production-evaluation-findings.md) | English

> Status: the H3-capable Harness is frozen under Q1 Round 13. Same-Lock H1
> Round 03 passed 21/21, H2 Round 05 passed 16/16, and formal H3 Round 02
> passed 14/14 with all eight RC lanes admitted. H3 admits a local
> `validated-dry-run` candidate only; H4 Canary and rollout expansion are not
> admitted. H4 implementation and its 120-Turn controlled preflight passed.
> Q1 Round 14, H1 Round 04, and H2 Round 06 passed, while same-Lock H3 Round
> 03 was operator-stopped at 96/480 Turns and is non-reusable. Formal H4 was
> not started.

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
| Foundation v2 F1-F3 and H3 Harness | qualified; v3 Harness frozen by Q1 Round 13 |
| Evaluation 17.4 | H1, H2, and H3 passed; H4 not started |
| Formal product findings | H2C-0001, H3P-0003, and H3P-0004 remediated and verified |
| Historical product hypotheses | 4 |
| Trusted 17.4 passes | same-Lock H1 Round 03; H2 Round 05; H3 Round 02 |
| Trusted 17.4 repairs | 2 |
| Open systemic Harness roots | 0 |
| Q1 Qualification | Round 13 passed; H3 Harness `frozen_qualified` |
| D1 Product Discovery | 56/56 passed; no Product Candidate |
| H1 Production Admission | same-Lock Round 03 passed 21/21 |
| H2 Production Admission | same-Lock Round 05 passed 16/16 |
| H3 Production Admission | Round 02 passed 14/14; eight of eight RC lanes admitted |
| H4 Canary Admission | not authorized |

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
evaluation/assessments/h2-preflight-global-assessment-01.json
evaluation/assessments/h2-preflight-global-assessment-02.json
evaluation/assessments/h2-preflight-global-assessment-03.json
evaluation/assessments/q1-qualification-global-assessment-08.json
evaluation/assessments/h2-production-admission-global-assessment-01.json
evaluation/assessments/h2-production-admission-global-assessment-02.json
evaluation/assessments/h2-reentry-decision-01.json
evaluation/assessments/q1-qualification-global-assessment-09.json
evaluation/assessments/h2-reentry-global-assessment-01.json
evaluation/assessments/h2-production-admission-global-assessment-03.json
evaluation/assessments/h3-preflight-global-assessment-06.json
evaluation/assessments/h3-production-admission-global-assessment-01.json
evaluation/assessments/h3-production-admission-global-assessment-02.json
evaluation/assessments/h3-production-admission-global-assessment-03.json
evaluation/assessments/h3-production-admission-global-assessment-04.json
evaluation/assessments/h3-production-admission-global-assessment-05.json
evaluation/assessments/q1-qualification-global-assessment-11.json
evaluation/assessments/q1-qualification-global-assessment-12.json
evaluation/assessments/q1-qualification-global-assessment-13.json
evaluation/assessments/h3-production-admission-global-assessment-06.json
evaluation/assessments/h4-preflight-global-assessment-01.json
evaluation/assessments/h4-closeout-global-assessment-01.json
```

Q1 Round 06 qualified the D1-capable Harness after 8/8 Foundation tasks and
three 7/7 Integration runs. D1 then settled all 56 tasks, closing PEH-0025 and
the D1H-0001 through D1H-0003 Harness findings.

Q1 Round 07 qualified the H1-capable successor with 8/8 Foundation tasks and
three 7/7 Integration runs. H1 then settled all 21 tasks across Extension
Host, process, provider, persistence, and filesystem lanes. Both identity
checks and the owned-resource cleanup check passed; no Product Candidate was
admitted.

H2 preflight found and closed four Harness findings plus H2C-0001, the stale
DeepSeek V4 Flash metadata contract. Q1 Round 08 qualified the successor.
Formal Rounds 01 and 02 each passed all eight exact-response samples but only
three of four Multi-Agent samples. Across formal Rounds, Multi-Agent quality
was 6/8 with a 40.92% Wilson 95% lower bound, below the 50% policy threshold.
Both Rounds retained identical Lock identity, private known-cost evidence, and
zero outstanding resources. H2 is therefore failed, not unavailable.

The failures remain immutable. Explicit re-entry added schema-v2 structured
failure evidence without changing the Prompt, denominator, or thresholds.
Q1 Round 09 qualified that Harness. A fixed 12-sample diagnostic matrix found
one Provider HTTP 400 after both child Agents completed; 36 instrumented
reproductions then passed and showed 130/130 reasoning messages retaining
replay state with zero orphan Tool Calls. No product-logic fix was authorized.
The one authorized formal re-entry, Round 03, passed 16/16 with 12/12 private,
known-cost Live samples and stable before/after identity. H2 is admitted.

### H3 Finding Closure

Formal H3 Round 01 completed 480/480 Turns with zero failed or canceled
Terminals, but failed the fixed persistence policy at 498,233 bytes/Turn.
Assessment separated the symptoms before remediation:

| ID | Domain | Root cause | Closure |
| --- | --- | --- | --- |
| H3P-0003 | Product persistence | every Terminal Envelope repeated the cumulative Session Delta history | deterministic bounded Session Delta encoding; verified |
| H3P-0004 | Product persistence | every automatic Checkpoint stored cumulative uncompressed history in CAS | deterministic bounded Checkpoint content encoding; verified |
| H3H-0009 | Harness sampling | dynamic CAS temporary refs could disappear during directory enumeration | ignore only transient `os.ErrNotExist`; verified |

The combined 480-Turn verification reduced persistence slope to 133,391
bytes/Turn without changing the 262,144 bytes/Turn threshold, duration,
denominator, Prompt, or retry policy. Because product inputs changed, Q1
restarted. Round 11 remains invalid runtime-identity history; Round 12 remains
a non-reusable single `evaluation-race` command failure that did not reproduce
in three exact confirmations. Q1 Round 13 then passed 8/8 Foundation tasks and
three consecutive 7/7 Integration runs.

On the Round 13 Lock, H1 Round 03 passed 21/21 and H2 Round 05 passed 16/16.
Formal H3 Round 02 passed all 14 coordinator tasks and the four-hour Endurance
workload completed 480/480 Turns. Persistence slope was 133,421 bytes/Turn,
P95 latency was 93 ms, RSS slope was 7,383 bytes/Turn, FD growth was zero, and
process restarts were zero. Foundation, Integration, Chaos, Live, Endurance,
Release, VS Code RC, and Package lanes all passed. Release Evidence v3 decided
`admit` for the local RC candidate; the VS Code candidate was not uploaded.

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

Q1, D1, H1, H2, and H3 are closed. H2 passed the one explicitly authorized
re-entry Round, and H3 passed after separately assessed product remediation
and requalification. H4 requires separate explicit authorization.

Current sequence:

1. Identity-bound attribution and Harness remediation are complete.
2. Q1 Round 06 froze the D1-capable v3 Candidate Lock.
3. D1 settled 36 Scenarios, 13 Fault Cases, five Host Cases, and two identity
   checks.
4. Q1 Round 07 froze the H1-capable successor Lock.
5. H1 settled all 21 tasks across five lanes.
6. Q1 Round 08 froze the H2-capable successor.
7. H2 Rounds 01 and 02 both failed 14/16 on Multi-Agent quality.
8. Q1 Round 09 froze schema-v2 failure evidence after explicit re-entry.
9. H2 Round 03 passed 16/16 without Prompt, threshold, or product changes.
10. H3 Round 01 failed the fixed persistence slope policy.
11. H3P-0003, H3P-0004, and H3H-0009 were remediated and verified separately.
12. Q1 Round 13 froze the H3-capable Lock after Rounds 11 and 12 remained
    immutable non-reusable history.
13. Same-Lock H1 Round 03 and H2 Round 05 passed.
14. H3 Round 02 passed 14/14 and admitted all eight RC lanes.
15. H4 admission was not granted.

The frozen Harness is authoritative only while its v3 input identity remains
unchanged.

## 7. Prohibited Actions

After H3 completion and before explicit H4 authorization:

- do not confirm or repair PEC-0001 through PEC-0004;
- do not restore removed 17.4 code;
- do not treat the non-uploaded `validated-dry-run` candidate as a public
  release, Canary, or rollout expansion;
- do not close one micro-incident while a systemic Epoch remains open;
- do not change product code during Discovery;
- do not weaken a denominator, status, or assertion to obtain green.
- do not start H4.
