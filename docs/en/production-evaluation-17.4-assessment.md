# Production Evaluation 17.4 Convergence Review Reset

[Simplified Chinese](../zh-CN/production-evaluation-17.4-assessment.md) | English

> Status: closed reset decision. This document records why the prior 17.4
> implementation and evidence are not trusted. It does not preserve any prior
> product pass or repair.

## Decision

Phase 17.4 is `reset_not_started`.

The machine-readable decision is:

```text
evaluation/assessments/17.4-convergence-review-reset-01.json
```

The later independent Foundation audit is:

```text
evaluation/assessments/production-evaluation-independent-audit-01.json
```

## Why the Reset Was Required

The previous implementation could not support a reliable product conclusion:

1. 36 Scenario families reused one success Fixture.
2. Runtime and Verification verdicts could pass without mandatory evidence.
3. Provider Split Mutation had zero effective executions while aggregate
   counts remained green.
4. Impact selection could return an empty Scenario set successfully.
5. Integration and Chaos discovery stopped at the first failure.
6. Fixture request routes and fragments were not fully contracted.
7. Harness inputs were not frozen into one machine digest before product
   repair.
8. Evaluation-only controls could cross production build boundaries.
9. Historical product repairs were made before Harness qualification.

These are systemic Harness roots, not a sequence of isolated incidents.

## Invalidated Evidence

The reset invalidates:

- prior 17.4 observed passes;
- prior 17.4 product attribution;
- prior 17.4 product repairs;
- prior VSIX Driver, Fixture Control, Crash Point, and Chaos evidence;
- any conclusion based on a non-frozen Harness;
- any result suppressed by first-failure termination.

PEC-0001 through PEC-0004 remain historical hypotheses. They must be
rediscovered before they can become formal product findings.

## Retained Facts

The following facts remain valid:

- the source baseline commit recorded by the reset assessment;
- the Architecture Ratchet result, because it was independently enforced;
- the existence of systemic Harness risks;
- the rule that Discovery, Global Assessment, Remediation, and Verification
  are separate Rounds;
- the one-way production dependency rule.

Candidate 17.1 and 17.2 code may be inspected and reused only after negative
requalification. It is not grandfathered into the new Foundation.

## Independent Audit Extension

The independent audit confirmed all prior false-assurance concerns and added
three Foundation roots:

| Root | Scope |
| --- | --- |
| PEH-0028 | command verdict and Suite admission do not form an executable Oracle closure |
| PEH-0029 | process ownership, Evidence freshness, Run identity, and report naming are incomplete |
| PEH-0030 | command output, privacy validation, Corpus provenance, and batch promotion fail open |

It also confirmed that the documentation gate failed because this assessment
document was missing.

## Re-entry Conditions

Phase 17.4 cannot restart until:

1. the corrected Technical Specification and Implementation Plan are approved;
2. Foundation Work Units F1 through F3 are complete;
3. one collect-all Foundation Qualification Epoch passes;
4. one Harness Lock completes three identical clean Integration qualification
   runs;
5. production artifact scanning proves Evaluation controls are absent;
6. Product Discovery is separately authorized.

Approval of the specification does not satisfy these conditions by itself.

## Current Authorization

Allowed:

- specification and documentation correction;
- review of candidate Foundation code;
- preparation of Foundation Work Units after explicit approval.

Not allowed:

- product Discovery;
- product Remediation;
- restoration of removed 17.4 code;
- use of current `eval-*` green output as release evidence;
- reclassification of a historical PEC as confirmed.
