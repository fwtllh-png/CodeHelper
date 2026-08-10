---
id: lab-provider-adapter
title: 实现 Provider Adapter
audience:
  - contributor
prerequisites:
  - extension-provider
  - practice-fixtures-smoke
code_paths:
  - internal/adapter/provider
  - internal/adapter/model
test_paths:
  - internal/adapter/provider/openai/stream_test.go
  - internal/adapter/provider/fault_injection_test.go
source_of_truth:
  - internal/adapter/provider/types.go
status: draft
last_verified: null
---

# 实现 Provider Adapter

简体中文 | [English](../../en/13-hands-on-labs/03-provider-adapter.md)

## 目标与前置条件

先实现 Fixture-only Stream Decoder，不接入网络客户端。

## 步骤

1. 在临时测试目录创建小型 Synthetic SSE。
2. 将 Text、Tool Fragment、Usage、Terminal、Malformed Frame 解码为 `provider.Event`。
3. 断言 Ordering、Bound、Unique Terminal、Stable Error。
4. 为 Truncated Line/Unknown Field 添加 Fuzz Seed。
5. 执行：

```bash
go test ./internal/adapter/provider/...
go test ./internal/adapter/provider/openai -fuzz FuzzStreamParserOpenAI -fuzztime=5s
```

## Test Matrix/Evidence

| Case | Required Observation |
| --- | --- |
| Fragmented Text | Ordered Delta |
| Fragmented Tool | Complete Valid JSON 后才 Executable |
| Cumulative Usage | Correct Call/Sample Accounting |
| Unknown Optional Field | 不改变 Known Semantic |
| Malformed Required Field | Stable Terminal Error |
| Disconnect Before Output | 保留 Retry Classification |
| Disconnect After Output | Partial-output；不 Transparent Replay |

用 Fake Clock/Body Closer 断言 Cancellation 关闭 Decoder/Response Resource。对照
`provider.Provider`/`Stream` Contract，而非复制另一个 Adapter。

## Completion Gate

Success、Malformed、Cancellation、Partial Stream 均有 Deterministic Test；Fuzz 无 Panic/
Unbounded Growth；且未增加 Production Catalog Entry。

## 预期结果

Decoder 确定性处理 Unknown Field，拒绝非法 Required Shape，不暴露 Credential。

## 失败诊断

Duplicate Terminal 是 Lifecycle Error；不完整 Tool Argument 是 Fragment Assembly Error；
无界 Scanner Growth 违反 Limit。

## 清理

删除临时测试/Fixture，检查 `git status --short`。

## 复习问题

1. Provider Stream Commit Point 是什么？
2. Tool Argument 何时可执行？
3. Partial Output 后为什么不能透明 Retry？

## 事实来源与验证

| 项目 | 值 |
| --- | --- |
| Catalog ID | `lab-provider-adapter` |
| 状态 | `verified` |
