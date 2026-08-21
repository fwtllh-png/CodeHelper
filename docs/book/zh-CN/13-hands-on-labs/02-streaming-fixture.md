---
id: lab-streaming-fixture
title: 使用 Fixture 观察 Streaming Event
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

# 使用 Fixture 观察 Streaming Event

## 目标与前置条件

将确定性 SSE Frame 追踪到 Normalized Runtime Event；只需 Go/仓库，不需网络和凭证。

## 步骤

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

检查 `stream.sse`，将 Delta、Usage、Terminal 与 NDJSON 对应：

```bash
jq -r '[.sequence, .kind, .turn_id, .item_id] | @tsv' "$tmp/events.ndjson"
test "$(jq -s '[.[] | select(.kind | test("turn.(completed|failed|canceled)"))] | length' "$tmp/events.ndjson")" -eq 1
go test ./internal/host/cli -run TestRunExecStreamsFixture
```

## Evidence Worksheet

记录 Fixture Frame、Normalized Event、Cursor、Identity，以及 Durable/Live-only 属性。
验证 Delta 拼接为最终 Answer，Receipt 位于 Terminal 前。

作为 Negative Control，改变 Prompt 并确认 Fixture 拒绝，以证明 Request Validation
实际生效，而不是盲目 Replay Byte。

## 预期结果

Event 有序且共享 Operation/Thread/Turn Identity；Text 增量输出，Usage 一次，Terminal 唯一。

## 失败诊断

Prompt Mismatch 表示 `expected_prompt` 漂移；无 Terminal 表示 Parser/Projection 问题；
非法 JSONL 表示 Host Framing 问题。

## 清理

```bash
rm -rf "$tmp"
```

## 复习问题

1. Wire Frame 在哪里 Normalize？
2. Prompt-mismatch Control 证明什么？
3. 为什么必须只有一个 Terminal？

## 事实来源与验证

| 项目 | 值 |
| --- | --- |
| Catalog ID | `lab-streaming-fixture` |
| 状态 | `draft` |
| 最后验证 | 尚未验证 |
