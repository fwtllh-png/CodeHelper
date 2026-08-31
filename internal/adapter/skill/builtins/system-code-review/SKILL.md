---
name: system-code-review
description: Reviews correctness, regressions, security, and tests. Invoke for code review or integration review. 代码审查 代码评审 回归审查
description_zh-CN: 审查正确性、回归、安全性与测试缺口；在代码审查或集成较大改动前使用。
---
# 系统代码审查

先读取变更、相邻测试和所属模块契约，再按严重度输出有证据的发现。结论必须引用文件与
行号，并区分已确认缺陷、风险和未验证假设。

满足以下任一条件时，应委派一个 `review` Subagent 独立审查：

- 改动跨越多个所有权模块；
- 修改协议、持久化、安全、并发或恢复语义；
- 实现者可能因上下文偏见遗漏回归。

审查任务应保持只读。Parent 负责核对发现、实施修复和最终结论，不把 Subagent 的
自述当作验证证据。
