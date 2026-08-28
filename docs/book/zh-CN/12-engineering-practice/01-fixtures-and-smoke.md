---
id: practice-fixtures-smoke
title: Hermetic Fixture 与真实 Provider Smoke
audience:
  - contributor
  - operator
prerequisites:
  - extension-provider
code_paths:
  - testdata/providers
  - internal/adapter/provider/fixture
test_paths:
  - internal/host/web/launcher_test.go
  - internal/adapter/provider/fault_injection_test.go
source_of_truth:
  - internal/adapter/provider/httpclient/deepseek_live_test.go
  - testdata/README.md
status: draft
last_verified: null
---

# Hermetic Fixture 与真实 Provider Smoke

## 学习目标

使用 Deterministic Fixture 验证正确性，用最小 Live Smoke 验证 Provider/Environment，
并明确两者不能互相替代。

## Test Pyramid

Provider Fixture 定义 Expected Prompt/Model 与 Replayable SSE，覆盖 Text、Tool、
Malformed Call、Cancellation、Editor Context、Subagent 和 Multi-sample，
不依赖网络/凭证。

Live Smoke 只发一次有界真实请求，验证 Credential、Endpoint、TLS、Remote Protocol 与
Account Availability；它不持久化 Secret，也不属于 Default Verify。

## Fixture Fidelity Contract

有效 Fixture 固定 Input、Protocol Byte、Timing/Control Point 与 Expected Normalized
Event；保留能触发 Adapter 行为的 Remote Quirk，但不复制 Private Production Traffic。

| Property | Why |
| --- | --- |
| Explicit Model/Prompt | 捕获 Route/Encoding 错误 |
| Complete Raw Frame | 验证 Fragment/Terminal/Usage |
| Deterministic Fault Point | 重现 Disconnect/Malformed/Timeout |
| Normalized Assertion | 验证 Adapter Contract |
| Bounded Synthetic Content | 可审查且无 Secret |

Golden Data 只在解释 Semantic Change 后更新；反复 Regenerate 直到 Test Pass 会破坏其
独立证据价值。

## Smoke Decision

| Result | Interpretation |
| --- | --- |
| Fixture Fail/Live Pass | Local Regression 或 Stale Fixture |
| Fixture Pass/Live Fail | Credential/Network/Provider Environment |
| Both Fail Similarly | 先查 Shared Encoding/Contract |
| Both Pass | Local Contract + Current Reachability |

Both Pass 仍不证明所有 Model、Region、Rate Limit、Long Stream 或 Future Behavior。

## 失败边界

- Fixture Mismatch 是 Code/Contract Failure。
- Live Auth/Rate/Network Failure 是 Environment Evidence。
- Prompt 必须匹配 `expected_prompt`。
- Fixture 不含真实 Credential/Private Response。
- Live Smoke 使用 Timeout/Bounded Output。

## 验证

```bash
go test ./internal/adapter/provider/... ./internal/host/web
make provider-deepseek-live-control  # 可选，需要凭证
```

## 复习问题

1. Live Smoke 能证明 Fixture 不能证明的什么？
2. 为什么不进入默认 CI？
3. 什么使 Fixture 可重复？
4. 何时可合法更新 Golden Fixture？
5. Fixture/Live 都通过仍不能证明什么？

## 事实来源与验证

| 项目 | 值 |
| --- | --- |
| Catalog ID | `practice-fixtures-smoke` |
| 状态 | `draft` |
| 最后验证 | 尚未验证 |
