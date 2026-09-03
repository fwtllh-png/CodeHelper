---
name: system-code-review
description: Reviews correctness, regressions, security, and tests. Invoke for code review or integration review. 代码审查 代码评审 回归审查
description_zh-CN: 审查正确性、回归、安全性与测试缺口；在代码审查或集成较大改动前使用。
---
# 系统代码审查

先读取变更、相邻测试和所属模块契约，再按严重度输出有证据的发现。结论必须引用文件与
行号，并区分已确认缺陷、风险和未验证假设。默认先在 Parent 内完成调查，不要因为任务
跨模块就同时开多个 `review` Subagent。

仅在同时满足以下条件时才委派：

- 并行收益明确，各 Child 有独立作用域（owned paths 或互不重叠的文件集）；
- Supervisor Admit 能通过（首包投影不超过 Child 预算）；
- 改动触及协议、持久化、安全、并发、恢复语义，或实现者存在上下文偏见。

审查任务应保持只读。Parent 负责核对发现、实施修复和最终结论，不把 Subagent 的
自述当作验证证据。失败若标为 `retryable`（预算或 Provider 限流），应 `followup_task`
重配额，而不是再 spawn 一批新的 review。
