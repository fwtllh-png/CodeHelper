---
id: lab-streaming-fixture
title: Observe Streaming Events with a Fixture
audience:
  - learner
  - contributor
prerequisites:
  - lab-first-turn
  - model-stream-reasoning-usage
code_paths:
  - testdata/providers/openai
  - internal/host/cli
test_paths:
  - internal/host/cli/run_test.go
source_of_truth:
  - testdata/providers/openai/fixture.json
status: draft
last_verified: null
---

# Observe Streaming Events with a Fixture

English | [简体中文](../../zh-CN/13-hands-on-labs/02-streaming-fixture.md)

## Goal and Prerequisites

Trace deterministic SSE frames into normalized Runtime Events. Requires Go and
the repository; no network or credential.

## Procedure

```bash
make build
tmp="$(mktemp -d)"
./bin/codehelper exec \
  --provider-fixture ./testdata/providers/openai \
  --provider openai \
  --model gpt-fixture \
  --workspace . \
  --data-dir "$tmp/state" \
  --output-format stream-json \
  "say hello" >"$tmp/events.ndjson"
```

Inspect `stream.sse`, then correlate deltas, usage, and terminal status with
NDJSON output:

```bash
jq -r '[.sequence, .kind, .turn_id, .item_id] | @tsv' "$tmp/events.ndjson"
test "$(jq -s '[.[] | select(.kind | test("turn.(completed|failed|canceled)"))] | length' "$tmp/events.ndjson")" -eq 1
go test ./internal/host/cli -run TestRunExecStreamsFixture
```

## Evidence Worksheet

Record the fixture frame, normalized Event kind, Cursor, identity, and whether
the Event is durable or live-only. Verify deltas concatenate to the final
answer and the Receipt precedes the terminal Event.

As a negative control, change the prompt and confirm the Fixture rejects it.
This proves request validation is active rather than replaying bytes blindly.

## Expected Result

Ordered Events share Operation/Thread/Turn identity, text is incremental, Usage
is emitted once, and one terminal Event closes the Turn.

## Failure Diagnosis

Prompt mismatch means `expected_prompt` drift. Missing terminal means parser or
projection failure. Malformed JSONL means Host framing failure.

## Cleanup

```bash
rm -rf "$tmp"
```

## Review Questions

1. Where are wire frames normalized?
2. Which identity fields remain stable?
3. Why is one terminal required?
4. What does the prompt-mismatch control prove?

## Sources and Verification

| Item | Value |
| --- | --- |
| Catalog ID | `lab-streaming-fixture` |
| Status | `draft` |
| Last verified | Not yet verified |
