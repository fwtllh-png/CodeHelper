# 后续规划

简体中文 | [English](../en/roadmap.md)

本文只描述目标，不作为能力已交付的证明。当前行为以其他指南和代码为准。

## 产品北极星

开发者把真实仓库任务交给 CodeHelper 后，应获得：

1. 正确的仓库定位；
2. 有界且可检查的计划；
3. 最小、受治理的变更；
4. 自动诊断和相关测试；
5. 验证失败后的修复或回滚；
6. 解释上下文、动作、成本和结果的 Durable Receipt；
7. 终端、编辑器和自动化 Host 中一致的语义。

## 近期：稳定初始发布

### 文档与上手

- 保持中英文文档一致；
- 提供不嵌入 Secret 的常见 Provider 可执行示例；
- 持续维护 CLI/Config/Link 漂移检查；
- 明确平台支持范围和失败模式。

### 正确性

- 提升 Affected Test 和依赖影响分析精度；
- 改进 Verify Command Discovery；
- 让 Repair Budget 与 Rollback 结果更易理解；
- 强化 Crash/Restart Recovery Test。

### 发布准备

- 建立可重复的 CLI 与 VS Code Release Pipeline；
- 发布 Checksum、SBOM、Provenance、Compatibility 与 Rollback 指南；
- 验证支持 Target 的 Clean Install；
- 从持续维护的产品测试与发布工作流建立真实模型、真实 VS Code、Chaos、耐久和
  Canary 发布门禁；
- 在第二个公开 Schema 出现前定义兼容策略。

## 中期：Coding Intelligence

- 超越词法提取的语言级 Symbol；
- 跨文件 Reference 与 Impact Graph；
- 更准确的仓库 Build/Test Topology；
- 只在显著改善证据时引入 Semantic Retrieval；
- 解释文件或测试为何进入 Working Set。

目标是减少无关读取和未验证编辑，而不是追求更大的 Context Window。

## 中期：执行可靠性

- 确定性的 Task Recovery 与 Lease 可观测性；
- Workflow Cancel、Retry 和 Checkpoint 保证；
- 更清晰的 Subagent Merge/Conflict 语义；
- 可评估质量的有界长会话 Compact；
- 并发 Provider、Tool 与 Worker 的资源隔离。

具体扫描工作流、优先级、状态和验收证据统一记录在
[运行时可靠性系统治理](./reliability-hardening.md)中。

## 中期：安全与治理

- 更强的跨平台 Sandbox 动态证据；
- 显式 Egress Policy 与 Endpoint Inventory；
- 托管环境的 Signed Policy/Permission Distribution；
- 具有脱敏保证的结构化 Audit Export；
- Plugin/MCP Provenance 与 Operator Risk Report；
- Credential Rotation 与 Revocation Workflow。

## 中期：Host 体验

### TUI

- 更清晰的 Approval、Verify、Cost 和 Background Work Panel；
- 更好的 Session Navigation 与 Recovery Feedback；
- Accessibility 与 Keyboard Discoverability。

### VS Code

- 稳定的 Editor Context Synchronization；
- 更完整的 Native Diff 与 Diagnostic Workflow；
- 可靠的本地 Single-root/Multi-root Lifecycle；
- 透明 Rollback 的 Managed Runtime Update；
- 大型 Workspace 下的有界性能。

### API 与 ACP

- 公开 Contract Example；
- Client Conformance Fixture；
- Backpressure 与 Replay 指南；
- 非 Loopback 部署的 Authentication 指南。

## 长期：生态

- 契约稳定后提供 Extension SDK；
- MCP、Skill、Hook 与 Workflow Spec 模板；
- Signed Registry Operation 与 Offline Mirror；
- 不分叉 Core Runtime 的可选企业治理能力。

## 明确不做

- 追求内置工具数量最大化；
- 替代仓库 CI；
- 隐藏不安全的平台限制；
- 强制上传源码的 Cloud Control Plane；
- 多套 Host 专用 Agent 实现；
- 为未发布开发数据过早承担兼容成本。

## Roadmap 验收规则

一项规划只有同时满足以下条件才算完成：

- 可从受支持 Host 到达；
- 已定义安全与失败行为；
- 测试覆盖契约；
- Observability 或 Receipt 可检查结果；
- 中英文文档已更新；
- Release/Support 声明与动态证据一致。

## 如何提出规划

设计变更应包含：

1. 用户问题与可衡量结果；
2. 所属 Package 与 Protocol 影响；
3. Security 与 Persistence 影响；
4. Cancel/Failure/Recovery 语义；
5. Test 与 Rollout Plan；
6. 文档变更。

避免使用 `phase1`、`p2` 等仅表示阶段的名称，应使用交付领域行为的语义名称。
