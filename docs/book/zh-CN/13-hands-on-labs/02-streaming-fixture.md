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
  - internal/host/runtimeapi/web
  - web/src/runtime
test_paths:
  - internal/host/runtimeapi/web/contract_test.go
  - web/src/runtime/client.test.ts
source_of_truth:
  - testdata/providers/openai/fixture.json
status: draft
last_verified: null
---

# 使用 Fixture 观察 Streaming Event

## 目标与前置条件

将确定性 SSE Frame 追踪到 Runtime Event 和 Browser Projection；只需 Go、Node.js 与
本仓库，不需网络和凭证。

## 步骤

```bash
make web-build
make build
tmp="$(mktemp -d)"
./bin/qcode \
  --provider-fixture ./testdata/providers/openai \
  --provider openai \
  --model gpt-fixture \
  --workspace . \
  --data-dir "$tmp/state" \
  --no-open
```

打开 Runtime Ready URL，创建 Session，发送 `say hello`，然后打开 Trajectory。对照
`testdata/providers/openai/stream.sse` 检查 Delta、Usage、Receipt 与 Terminal Record。

## Evidence Worksheet

记录 Fixture Frame、Normalized Event、Cursor、Identity，以及 Durable/Live-only 属性。
验证 Delta 拼接为最终 Answer，Receipt 位于 Terminal 前。

作为 Negative Control，改变 Prompt 并确认 Fixture 明确失败，以证明 Request Validation
实际生效，而不是盲目 Replay Byte。

## 聚焦验证

```bash
go test ./internal/host/runtimeapi/web -run TestWebHostMeetsTheRuntimeContract
npm --prefix web test -- --testNamePattern "normalizes empty collections"
```

## 预期结果

Event 有序且共享 Operation/Thread/Turn Identity；Text 增量输出，Usage 一次，
Terminal 唯一。刷新浏览器后 Snapshot 与 Live Event 合并不产生重复。

## 清理

中断进程并删除 `$tmp`。

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
