# 固定阈值审计与动态容量治理

## 结论

QCode 中的数值常量不能统一删除。协议解码、防资源耗尽、文件格式和安全边界需要
明确的绝对上限；模型上下文、工具投影、并发、重试和轮询则必须由权威能力、显式配置
或运行时状态决定。

本次扫描覆盖 `internal`、`cmd`、`web/src` 和相关中文文档。词法扫描得到 632 条生产
代码数值候选，进一步按所有权和行为归并为下列阈值族。测试夹具中的样本值、协议版本、
数组索引、HTTP 状态码、单位换算和 UI 尺寸不计为运行策略阈值。

当前最高风险集中在 Context：

1. 非增量 Route 曾使用固定的 16K/24K/48K Compaction 档位，与模型实际 1M
   Context Window 无关；本次已移除。
2. 默认输出上限已改为模型能力、实际输入和显式预算共同决定。
3. Prompt、Tool Result、Search 和 Subagent Capsule 已接入同一动态容量链路。
4. `semantic_narrative=off` 时，压缩只能保留结构化事实和 Raw Tail；引用上一轮语义的
   问题容易在工具密集 Turn 后失去指代对象。
5. Provider 首个可见 Delta 已绕过后续批量窗口立即发送。

## 规范

所有容量决策必须声明来源，且只能属于以下类型：

| 来源 | 含义 | 要求 |
| --- | --- | --- |
| Capability | 模型、Provider、Transport 或平台声明的真实能力 | 运行时冻结到 TurnSpec，并记录 Provenance |
| Operator Config | 用户主动设置的限制或 SLA | 校验范围，不能扩大上游硬能力 |
| Protocol Contract | 防拒绝服务、编码或互操作所需的绝对边界 | 公开、版本化、文档化并有边界测试 |
| Runtime Observation | 当前请求大小、剩余预算、TTFT、缓存命中、队列和内存压力 | 使用稳定测量和滞回，记录决策原因 |

禁止：

- 根据模型名称维护容量档位；
- 根据 Transport 类型暗中设置固定 Token 上限；
- 在调用点散落未命名数字；
- 用性能软目标提前丢弃仍可被模型窗口容纳的用户语义；
- 仅修改测试期望来掩盖实现与文档不一致。

## 目标容量模型

### Context Admission

最终方案不使用 Prepare/Compact/Emergency 固定 Token 或固定百分比档位。每次采样前计算：

```text
hard_input_capacity =
  min(model_context_capacity, provider_transport_capacity)
  - actual_output_reservation
  - protocol_framing_reservation

required_capacity =
  stable_context
  + conversation_history
  + dynamic_context
  + tool_definitions
  + pending_tool_reservation
```

处理顺序：

1. `required_capacity <= hard_input_capacity` 时不做正确性 Compaction。
2. 超限时对可见 Tail 做一次因果组折叠，不改写已发送 Tool Result。
3. 重新测量后仍超限，才选择满足 Tool Pair 闭合的最小历史裁剪范围。
4. Compaction 后必须验证 Goal、Pending Input、未验证 Change 和当前用户请求仍可恢复。
5. 无候选能满足硬容量时返回结构化 `resource_exhausted`，不得继续猜测。

TTFT 和成本属于独立软目标。只有配置了 Operator SLA，或积累了足够的 Route
观测样本时，才允许通过 EWMA/P95 反馈调整模型可见 Tool Surface；软目标不能改变
上述正确性边界。

### Context Allocation

Prompt 各 Partition 不应各自持有模型无关的 Token 档位。应先预留 Mandatory 内容，
再按优先级从本次 `hard_input_capacity` 分配剩余容量：

1. 当前用户请求、Mode、Policy、Pending Interaction；
2. 最近对话和当前 Turn 因果链；
3. Tool Definitions；
4. Working Set、Repo Map、Evidence；
5. Memory、Skills 和可重新获取的辅助信息。

未使用的 Partition 配额应回流共享池，不能形成无法利用的静态空洞。

## 审计结果

### 模型与上下文

| 位置 | 当前行为 | 结论 | 建议 |
| --- | --- | --- | --- |
| `internal/runtime/agent/engine/context_policy.go` | Compaction 默认读取 Route Context Window | 已改善 | 继续迁移到基于实际请求 reservation 的单一 admission，最终移除三档比例 |
| `internal/runtime/agent/context/store_window.go` | 默认边界等于硬输入容量 | 已改善 | 显式非零值仅作为 Operator Ceiling |
| `internal/runtime/agent/engine/model_handler.go` | 输出 Ceiling 来自冻结的 Turn Capacity | 已改善 | 每次 Sample 再按实际输入、Token 与 USD Budget 收窄 |
| `internal/runtime/app/wire/modules_runtime.go` | `turn_budget_tokens=0` 不再从 Context Window 派生累计上限 | 已修复 | Context 仅约束单次请求；累计成本必须来自显式 Token/USD Budget |
| `internal/config/defaults.go:45` | Recent Tail 默认值已改为 0 | 已改善 | 0 由当前 Route 的动态容量解析，显式值只作为 Operator Ceiling |
| `internal/runtime/agent/engine/compaction.go` | Summary 默认使用 Turn 硬输入容量作为 Ceiling | 已改善 | 实际内容仍由 Truth Retention 和 Digest 数量约束 |
| `internal/config/defaults.go:37` | Truth、Digest、Fact、Handle 分别有固定配额 | 应动态化 | Mandatory 先分配，剩余容量按优先级共享，不为每类预切固定空间 |
| `internal/runtime/app/wire/route.go` | 默认 Partition Ceiling 从冻结的硬输入容量生成 | 已改善 | 静态 Prompt 使用共享总池，显式配置仍可收窄单个 Partition |
| `internal/runtime/agent/prompt/world_projection.go` | 不再补第二套固定默认 | 已改善 | 缺失预算由组合根统一负责 |
| `internal/runtime/agent/prompt/fragment.go` | 已删除单 Fragment 固定 10K Ceiling | 已改善 | Fragment 服从共享 Prompt Capacity |
| `internal/runtime/agent/context/budget.go:35` | 图片 Token 使用固定尺寸和 85/170 Token 公式 | 模型相关 | 移入 Model/Provider Capability，由对应视觉模型估算器实现 |
| `internal/runtime/agent/context/compaction_candidate.go` | 重复压缩曾丢失上一 Capsule 的 Goal | 已修复 | 保持 mandatory Goal 继承和回归测试 |

### Tool 与检索

| 位置 | 当前行为 | 结论 | 建议 |
| --- | --- | --- | --- |
| `internal/adapter/tool/tool.go` | 工具类别只保留标签，Token Ceiling 来自 Turn/Batch/ResultStore | 已改善 | 完整结果通过 CAS Handle 保留 |
| `internal/runtime/agent/engine/tool_result_pruning.go` | Surface 目标由当前超限量和结果占用动态计算 | 已改善 | 不再使用固定 4 KiB |
| `internal/runtime/agent/engine/history_recovery.go` | 已删除 16 KiB/384 B 的第二套旧结果旁路 | 已改善 | 统一走 Context Admission |
| `internal/adapter/tool/search/search.go` | 默认扫描与结果数服从 Runtime Result Budget | 已改善 | 显式参数只能进一步收窄 |
| `internal/adapter/tool/search/evidence.go` | 不再维护固定 32 项副本 | 已改善 | 由 Evidence Ledger 的显式配置统一治理 |
| `internal/adapter/tool/handle/handle.go` | Handle 默认 12 KiB、硬上限 50 KiB | 应拆分 | 硬上限进入 Tool Contract；默认读取量由剩余模型容量决定 |
| `internal/platform/process/output.go` | 进程保留 8 MiB，模型输出 1 MiB | 应配置化 | 原始归档上限按存储策略；模型投影按 Context Admission |
| `internal/platform/process/jobs.go:369` | Job Tail 固定 4 KiB | 应配置化 | 按请求显示预算读取，默认值进入公开配置 |
| `internal/adapter/tool/web/web.go` | Body 默认 32 MiB、硬上限 128 MiB | 保留硬边界 | 暴露 Host/Tool Contract；模型可见内容仍按动态预算裁剪 |
| `internal/adapter/tool/git/hosted.go` | 响应固定 2 MiB、默认页数 5 | 应配置化 | 网络下载硬上限保留，分页默认由请求预算和服务端游标决定 |

### Provider 与 TTFT

| 位置 | 当前行为 | 结论 | 建议 |
| --- | --- | --- | --- |
| `internal/adapter/provider/assembly/delta_stream.go` | 首个可见 Delta 立即发送，后续仍聚合 | 已改善 | 后续窗口继续承担传输背压，不再影响 TTFT |
| `internal/adapter/provider/httpclient/client.go` | Idle/连接阶段使用固定默认时间 | 可配置化 | 继承调用 Context Deadline；仅在无 Deadline 时采用有 Provenance 的配置 |
| `internal/adapter/provider/wire/retry.go` | 退避从 10ms 增长并封顶 30s | 应配置化 | 优先使用 `Retry-After`；本地退避策略进入 Provider Contract |
| `internal/adapter/provider/sse.go` | SSE Event 固定 1 MiB | 保留硬边界 | 作为协议防护公开，并允许 Provider 声明更小上限 |
| `internal/adapter/provider/replay.go` | Replay 数据固定 1 MiB | 保留硬边界 | 版本化 Protocol Contract，不参与模型容量动态计算 |

### Runtime、并发与调度

| 位置 | 当前行为 | 结论 | 建议 |
| --- | --- | --- | --- |
| `internal/config/defaults.go` | Runtime Queue、并发、Worker Lease 等均有默认值 | 基本合理 | 已有 Config 与 Provenance；增加运行时饱和指标和拒绝原因 |
| `internal/runtime/agent/turnkernel/runtime_control.go:46` | Mailbox 默认 64 | 应统一 | 从 Runtime Subscriber/Operation Capacity 派生或显式配置 |
| `internal/runtime/agent/engine/cancel_handler.go:13` | Backlog 为 Mailbox 的 2 倍 | 可接受派生 | 保持相对关系，但将关系写入契约测试 |
| `internal/orchestration/subagent/context_fork.go` | Capsule 从父 Turn 剩余容量和 Child Budget 派生 | 已改善 | `task_capsule` 默认仍只携带任务相关事实 |
| 已移除的 Workflow JS VM | 生命周期与并行项曾固定为 1000/16 | 已消除 | 后台 Workflow 平面已删除 |
| 已移除的后台 Task Executor | 退避曾固定为 15s、封顶 10m | 已消除 | 后台 Worker 与 Task Queue 已删除 |
| `web/src/ui/App.tsx` | Workspace/Session 在页面重新可见时刷新 | 已改善 | 当前 Workspace 继续依赖 WebSocket |
| `web/src/ui/App.tsx` | Trajectory 由会改变 Trace 的 Runtime Event 触发查询 | 已改善 | 使用服务端已确认的 watermark，文本 Delta 不触发查询 |

### 持久化与可观测性

| 位置 | 当前行为 | 结论 | 建议 |
| --- | --- | --- | --- |
| `internal/persist/history/service.go:16` | Presentation Snapshot 固定 8 MiB | 应取消 | 图片等大对象迁移到 CAS，Snapshot 只保留引用 |
| `internal/persist/contentstore/store.go:50` | 内存 Store 默认 64 MiB/2048 项 | 应配置化 | 从 Runtime 内存预算分配并暴露使用率 |
| 已移除的 Observation/OTLP 平面 | Queue、Journal 与 Retention 曾使用固定容量 | 已消除 | 独立证据平面已删除 |
| `internal/persist/state/sqlite/store.go:131` | Busy Timeout 默认 5 秒 | 已配置化 | 保持；优先继承调用 Deadline，不允许覆盖更短 Deadline |

### 安全与协议边界

| 位置 | 当前行为 | 结论 | 建议 |
| --- | --- | --- | --- |
| `internal/runtime/protocol/editor_context.go:13` | 附件数量、文本和图片大小上限 | 应保留 | 属于输入验证与 DoS 边界；提升为公开 Protocol Constant 并生成 Web 限制 |
| `internal/runtime/durablecodec/codec.go:17` | 解码硬上限 64 MiB | 应保留 | 持久化格式安全边界，必须有拒绝测试 |
| `internal/security/sandbox/backend.go:28` | 精确写路径最多 512 | 应保留并公开 | Sandbox Profile 的结构上限，不得由模型扩大 |
| `internal/security/sandbox/landlock_protocol.go:17` | Helper 请求最大 1 MiB | 应保留 | IPC/内核边界，作为版本化协议约束 |
| `internal/security/credential/service.go:14` | Secret 最大 32 KiB | 应保留 | 防资源耗尽；错误中不得回显 Secret |
| `internal/platform/textdiff/textdiff.go:26` | Diff 最大矩阵单元数 | 应保留或算法切换 | 超限时改用线性内存算法，不直接扩大阈值 |
| `internal/host/runtimeapi/web/capacity.go` | JSON、WebSocket、连接和 Session 集中限额 | 基本合理 | 已集中为 Capacity；补齐 Config/Provenance 和资源使用指标 |
| `internal/security/policy/approval.go:53` | Approval Cache 默认 1024 | 应配置化 | 由并发 Session/TTL 推导，保留绝对防护上限 |

## 不应动态化的数值

以下数值不是运行策略阈值，不应因本规范被删除：

- Schema、Protocol、Record 和 Snapshot 版本号；
- 加密摘要长度、文件描述符约定和系统调用常量；
- HTTP 状态码和协议规定字段；
- 单元测试中的边界样本、故障注入延迟和并发压力规模；
- UI 像素尺寸与动画值，但它们应集中在设计 Token 中；
- 明确公开且用于防 DoS、越界读取或 Sandbox 构造的硬上限。

## 实施路线

### P0：正确性与当前问题

1. 完成 Goal 跨重复 Compaction 保留。
2. 删除 Transport 专属固定 Compaction 档位。
3. 将 Recent Tail 默认容量改为 Route 动态容量。
4. Tool Result 首次准入定稿，采样路径不再事后改写。
5. 将默认输出容量改为模型、当前输入和剩余预算共同推导。

### P1：统一 Context Allocator

1. 引入单一 `ContextCapacity` 快照，冻结到 TurnSpec。
2. Prompt Partition、Tool Result、Summary、Narrative 和 Subagent Capsule 统一申请容量。
3. 删除各 Package 的第二套隐式默认值。
4. 每次决策记录 Capacity Source、Requested、Granted、Reason 和 Provenance。
5. 增加多种模型窗口与输出预留的参数化测试。

### P1：降低 TTFT

1. 首个 Provider Delta 立即透传，后续再做自适应合并。
2. 搜索结果在 Tool 边界按本次预算分页，避免先产生巨大结果再压缩。
3. 当前 Workspace 使用事件驱动刷新，移除 3 秒 Git 状态轮询。
4. Trace 使用 Cursor 增量更新，移除 1 秒全量查询。

### P2：配置与安全治理

1. 将网络、MCP、Worker、Retention 和存储容量纳入统一配置与 Provenance。
2. 将不可配置的绝对值登记为 Protocol/Security Contract。
3. 增加静态检查，禁止在容量决策路径新增未经登记的数值常量。
4. CI 输出新增、删除和来源变化的阈值清单。

## 验收标准

- 4K、64K、128K、1M Context 模型的 Compaction 决策均由同一算法得出。
- 改变模型或 Output Reserve 后，无需修改代码常量即可得到新容量。
- 正常容量内的 Tool 调用不产生 Surface Pruning 或 History Replacement。
- 单个 Turn 多次压缩后仍保留当前 Goal、最近对话和未闭合因果链。
- “上一轮解释 TTFT，下一轮询问这类体验”在压缩前后输出意图一致。
- 首个 Delta 不受批量刷新窗口延迟。
- Web 不把 Pruning、Replacement 和 Lifecycle 统计为同一种 Compaction。
- 所有绝对安全上限都能追溯到 Protocol/Config Contract 和边界测试。
- Runtime Test、Web Test、Docs Check 与 `git diff --check`
  全部通过。

## 真实会话验证

在 1M Context 模型上执行两轮真实会话：

1. 首轮解释 TTFT，次轮使用“这类体验”继续请求代码分析；
2. 次轮最终回答继续围绕 TTFT，没有漂移到历史图片任务；
3. 两轮均未产生 `PRUNE` 或 `COMPACT`；
4. Usage 记录的硬输入容量为 655360，输出容量来源为
   `model_capability`，后续输出保留会继续受 Turn/Session Budget 收缩；
5. 复杂代码分析的多次 Provider 调用仍会累计 Token。大窗口只解决单次请求
   可容纳性，不是成本预算；生产环境应通过显式 Turn Token Budget 或 USD
   Budget 约束总消耗，不能用提前 Compaction 代替成本治理。
