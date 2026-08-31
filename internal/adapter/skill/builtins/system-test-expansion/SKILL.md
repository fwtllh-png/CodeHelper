---
name: system-test-expansion
description: Expands tests from risk boundaries and observed behavior. Invoke for shared contracts or critical workflows. 补充测试 测试覆盖率 回归测试
description_zh-CN: 根据风险边界和实际行为扩展测试；改动影响共享契约或关键工作流时使用。
---
# 系统化测试扩展

从被修改的行为、不变量和历史失败模式推导测试矩阵。优先复用最近的测试框架，覆盖成功、
拒绝、边界、恢复和并发路径，并区分环境失败与产品失败。

当测试目标属于互不依赖的包或平台时，可委派多个 `verifier` Subagent 并行执行。
每个 Child 返回命令、结果和剩余风险，不修改产品实现。需要共享服务、端口、Fixture
或顺序状态的验证由 Parent 串行协调。

测试数量不是目标；每个用例都应保护一个明确契约或可复现故障。
