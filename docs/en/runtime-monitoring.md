# VS Code Runtime Monitoring Runbook

[简体中文](../zh-CN/runtime-monitoring.md) | English

This runbook defines the standard evidence-driven workflow for optimizing
CodeHelper through real multi-Turn VS Code sessions. It does not use screen
recording and never infers success from model wording.

## Scope and Evidence

Use four correlated sources:

1. the opt-in VS Code Runtime Capture JSONL for every live protocol Event, ACP
   request lifecycle, Runtime stderr, process exit, and supervisor state;
2. `events-v1.jsonl` for durable Events and recovery cursors;
3. `state-v1.db` for Runtime-owned relational projections;
4. the CodeHelper Extension log for launch, recovery, and update failures.

Identity is always the tuple `session_id`, `thread_id`, `turn_id`,
`operation_id`, and `call_id` where applicable. Natural-language output is
supporting evidence, never a terminal-state signal.

Runtime Capture is explicit and may contain prompts, model output, Tool
arguments/results, diagnostics, paths, and command output. Files use mode
`0600`; redact them before sharing and never commit raw captures.

## Preflight

1. Build and install a target VSIX such as
   `codehelper-vscode-<version>-darwin-arm64.vsix`. The universal VSIX does not
   contain a bundled Runtime and is not a local end-to-end test artifact.
2. Reload the VS Code window.
3. Run `CodeHelper: Show Status` and require `state=ready`.
4. Create or select one dedicated Chat Session.
5. Run `CodeHelper: Start Runtime Capture` before the first measured Turn.
6. Record the reported Capture path and verify that its first records include
   `capture.started` and `runtime.state` with `state=ready`.

Do not start measured work until the Capture file exists and is growing.

## Standard Test Matrix

Use the smallest set that exercises the changed behavior:

| Case | Purpose | Required evidence |
| --- | --- | --- |
| Baseline prompt | startup and first-token baseline | completed Turn Receipt |
| Read-only analysis | search/read/command behavior | paired Tool Events |
| Multi-Turn follow-up | retained context and goal continuity | ordered Turn IDs |
| Write or fix request | mutation and verification path | change Receipt or explicit failure |
| Approval | guarded consequential action | required/resolved pair |
| Recoverable Tool failure | error feedback and continuation | failed result plus later terminal Event |
| Cancellation | bounded interruption | canceled terminal Event |
| Context pressure | compaction correctness | shrinking compaction records |
| Runtime restart | replay and Session recovery | replay markers and ready state |

Do not manufacture destructive cases in a valuable Workspace. Use a fixture or
disposable worktree when testing denial, cancellation, crash, or rollback.

## Live Checks

During every Turn, check structured invariants:

- each accepted operation reaches exactly one terminal Event;
- every `tool.start` has one `tool.result` with the same `call_id`;
- each ACP request has one completed or failed record;
- Approval and Input requests have matching resolutions;
- Runtime exit, stderr, restart, and Session synchronization errors are
  preserved;
- `turn.receipt` agrees with the terminal Event, Workspace outcome, changed
  files, verification, usage, and cost;
- a Compaction reduces retained bytes and returns history to its configured
  budget;
- identical read-only Tool calls are not repeatedly executed against the same
  Workspace snapshot;
- a fix request does not complete successfully with no change and no
  verification unless a structured outcome explicitly explains why.

Treat missing terminal Events, unpaired Tool calls, false completion, lost
process exits, and Compaction amplification as release-blocking findings.

## Stop and Freeze Evidence

1. Wait for the final Turn terminal Event and Checkpoint.
2. Run `CodeHelper: Stop Runtime Capture`.
3. Confirm `capture.stopped`, or document why the Extension Host had to be
   restarted.
4. Record file size, line count, final modification time, and SHA-256.
5. Confirm the file no longer grows and the Runtime returns to `state=ready`.

Example:

```bash
wc -l "$CAPTURE"
stat "$CAPTURE"
shasum -a 256 "$CAPTURE"
jq -r '.kind' "$CAPTURE" | sort | uniq -c | sort -nr
```

## Structured Analysis

Start with counts and pairing before reading large payloads:

```bash
jq -r '
  select(.kind == "runtime.event") | .data.event.kind
' "$CAPTURE" | sort | uniq -c | sort -nr

jq -c '
  select(.kind == "runtime.event")
  | .data.event
  | select(
      .kind == "turn.failed"
      or .kind == "operation.rejected"
      or (.kind == "tool.result" and .data.is_error == true)
    )
' "$CAPTURE"

jq -c '
  select(.kind == "runtime.event")
  | .data.event
  | select(.kind == "turn.compaction")
  | {
      turn_id,
      original: .data.original_bytes,
      retained: .data.retained_bytes
    }
' "$CAPTURE"
```

Use `turn.receipt` for aggregate latency, Token, cost, verification, changes,
and context-budget facts. Preserve failed cases and their recovery path in the
report rather than reporting only the final successful attempt.

## Report Contract

Store working reports outside tracked product documentation:

```text
.tmp/runtime-monitor/<date>-<session-id>-report.md
```

Every report contains:

1. Session, Thread, Runtime, route, posture, Sandbox, and time range;
2. Capture path, permissions, record count, size, and SHA-256;
3. one row or section per Turn with terminal state, Tool count, changes,
   verification, latency, Token, cost, and Checkpoint;
4. findings ordered by severity;
5. for each finding: structured evidence, reproduction, impact, attribution,
   recovery, and recommended test;
6. healthy invariants and remaining coverage gaps;
7. a final prioritized optimization list.

Use `Critical`, `High`, `Medium`, and `Low`. A claim without a protocol Event,
Receipt, process state, database fact, or log line remains an observation, not
a confirmed defect.

## Optimization Loop

For each confirmed defect:

1. capture the failing IDs and smallest reproducer;
2. add a focused regression test at the owning boundary;
3. implement the fix without weakening Guard, Approval, Journal, or Sandbox;
4. run package tests and broader checks based on blast radius;
5. rebuild and install the target VSIX;
6. repeat the same Session test matrix with a new Capture;
7. compare structured before/after facts in the report.

The optimization is complete only when the original structured failure no
longer occurs and the surrounding invariants still pass.
