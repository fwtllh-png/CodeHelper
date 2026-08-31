---
name: system-debugging
description: Diagnoses complex failures with runtime evidence. Invoke when static inspection cannot isolate the cause. 复杂调试 故障排查 根因分析
description_zh-CN: 通过竞争假设和运行时证据诊断复杂故障；静态检查无法定位原因时使用。
---
# 系统化调试

先建立可复现基线，再列出可证伪的故障假设。每次只增加能够区分假设的最小观测，
根据新证据更新判断，修复后复现原路径并执行回归验证。

当存在两个以上彼此独立的故障假设或证据来源时，可并行委派 `explore` Subagent；
每个 Child 只负责一个假设并返回证据。涉及同一运行状态、同一日志流或顺序依赖的
排查留在 Parent，避免重复采样和相互干扰。

运行时观测无法支持结论时应明确保留未知项，不用猜测替代证据。
