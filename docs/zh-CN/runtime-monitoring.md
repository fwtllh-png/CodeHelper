# VS Code Runtime 标准监测 Runbook

简体中文 | [English](../en/runtime-monitoring.md)

本文定义通过 VS Code 真实 Multi-Turn Session 持续优化 CodeHelper 的标准证据流程。
该流程不使用录屏，也不根据模型措辞推断成功。

## 范围与证据

关联使用四类数据源：

1. Opt-in VS Code Runtime Capture JSONL：全部 Live Protocol Event、ACP Request
   生命周期、Runtime stderr、进程退出和 Supervisor 状态；
2. `events-v1.jsonl`：Durable Event 与 Recovery Cursor；
3. `state-v1.db`：Runtime 持有的关系投影；
4. CodeHelper Extension Log：启动、恢复与更新故障。

身份必须使用 `session_id`、`thread_id`、`turn_id`、`operation_id` 和适用时的
`call_id` 组成的结构化元组。自然语言输出只能作为辅助证据，不能作为终态信号。

Runtime Capture 需要显式开启，可能包含 Prompt、Model Output、Tool
Arguments/Results、Diagnostics、Path 和 Command Output。文件权限为 `0600`；分享前
必须脱敏，Raw Capture 不能提交到仓库。

## 前置检查

1. 构建并安装
   `codehelper-vscode-<version>-darwin-arm64.vsix` 之类的 Target VSIX。Universal
   VSIX 不含 Bundled Runtime，不能作为本地端到端实测产物。
2. Reload VS Code Window。
3. 执行 `CodeHelper: Show Status`，必须得到 `state=ready`。
4. 创建或选择一个专用 Chat Session。
5. 在第一个被测 Turn 前执行 `CodeHelper: Start Runtime Capture`。
6. 记录提示中的 Capture Path，并确认首批记录包含 `capture.started` 和
   `state=ready` 的 `runtime.state`。

Capture 文件存在且持续增长前，不开始被测操作。

## 标准测试矩阵

使用能覆盖本次改动的最小用例集：

| Case | 目的 | 必需证据 |
| --- | --- | --- |
| Baseline Prompt | 启动与首 Token 基线 | Completed Turn Receipt |
| Read-only Analysis | Search/Read/Command 行为 | 配对的 Tool Event |
| Multi-Turn Follow-up | Context 保留与目标连续性 | 有序 Turn ID |
| Write/Fix Request | 修改与验证路径 | Change Receipt 或显式失败 |
| Retry/Continue | 保持 Source Turn Intent | 一致的 `turn.started.intent` |
| Implement Plan | 启动受治理的 Workspace Change | `intent=workspace_change` |
| Approval | 高风险操作治理 | Required/Resolved 配对 |
| Recoverable Tool Failure | 错误反馈与续行 | Failed Result 与后续终态 |
| Cancellation | 有界中断 | Canceled Terminal Event |
| Context Pressure | Compaction 正确性 | 实际缩小的 Compaction |
| Runtime Restart | Replay 与 Session 恢复 | Replay Marker 与 Ready State |

不能在有价值的 Workspace 中人为制造破坏性场景。Denial、Cancellation、Crash 与
Rollback 测试应使用 Fixture 或 Disposable Worktree。

## 实时检查

每个 Turn 都检查以下结构化不变量：

- 每个 Accepted Operation 恰好到达一个 Terminal Event；
- 每个 `tool.start` 都有一个相同 `call_id` 的 `tool.result`；
- 每个 ACP Request 都有一个 Completed 或 Failed Record；
- Approval/Input Request 都有对应 Resolution；
- Runtime Exit、stderr、Restart 与 Session Synchronization Error 被保留；
- `turn.receipt` 与 Terminal Event、Workspace Outcome、Changed Files、
  Verification、Usage 和 Cost 一致；
- Retry/Continue 保持 Source `turn.started.intent`；缺少 Intent 的旧 Event 使用
  已定义的 `answer` 兼容缺省值；
- Retry/Continue 在保留 Source Intent 的同时，必须在新 Turn 启动前重新应用当前
  持久化 Session Profile；Mode、Approval Posture、Reasoning 和 Tool Selection
  不能从失败 Source Turn 漂移到恢复 Turn；
- 恢复流程必须分离模型文本与 UI 文本：`turn.started.prompt` 可以包含有界的
  Recovery Evidence Capsule，`turn.started.display_prompt` 只能包含简洁、用户
  可见的 Retry/Continue 请求。Host 不得将 Source Turn ID、Tool Call ID、Digest
  或 `<recovery_evidence>` 投影为用户消息；
- Plan Implement/Autopilot 必须以 `intent=workspace_change` 启动；
- Mode 与 Intent 是正交契约。普通 Chat 的 `mode=act` 仍以 `intent=answer`
  启动；只有 Diagnostics Fix、Implement Plan、Autopilot 等显式变更入口提交
  `workspace_change`。不得使用 Mode 或自然语言关键词猜测变更 Intent；
- Failed Turn Receipt 不携带成功 Outcome；Completed `workspace_change` Receipt
  必须同时具有 `outcome=changed`、Observed Changes 和 Changed Workspace Outcome；
- `shell_read` 不能修改 Workspace 文件；`exec_command` 默认仍为 Read-only，
  仅可通过 `write_paths` 声明最多 512 个已存在的精确文件，并且所有声明写入必须
  经过 Approval、Journal、TurnDiff、Sandbox Enforcement 与 Receipt；
  `write_stdin` 只能交互当前 Thread 所拥有的 Session；
- Execution Budget Admission 必须 FIFO 且有界；冲突 Claim 保持顺序，无关 Claim
  可以并发执行；
- 每个完成的 Tool Execution Receipt 只有一个 Terminal Status 与 Owner；取消的
  Process Call 必须记录 Teardown Latency，不保留不可达 Session 或存活 Process Group；
- File Mutation Tool 的 Preview、Approval、Revalidation 与 Commit 必须整体串行；
  `edit_plan_stale` 只能通过新的 Read、Plan 与 Approval 恢复；
- Fatal Tool Batch 必须在 Turn Failed 前为每个已 Start Call 发出且仅发出一个
  Structured Result；
- 未解决的结构化 Tool Failure 不能由承诺未来动作的文本清除；`workspace_change`
  与 `operation` Turn 必须有后续成功 Tool Batch 及恢复后的 Completion Check；
- Shell Tool 必须向模型声明实际执行方言为 POSIX `sh`；模型不得对
  `shell_read` 或 `exec_command` 使用 Bash-only Process
  Substitution 等不受支持的语法；
- Shell Tool 必须在启动进程前结构化拒绝已知 Bash-only Process Substitution；
  Descriptor 的自然语言提示不能作为唯一防线；
- `file_read` 或 `file_list` 路径不存在时，必须返回可恢复 Tool Failure，携带
  `error_category=file_not_found`、`required_action=file_list` 和
  `retry_original=false`。Runtime 将其回流给模型修正路径，不得升级为 `internal`
  Terminal；
- Strong Sandbox 校验若在 Shell Command 启动前观察到 Workspace Path 消失，必须
  继续阻止 Command 执行，并返回携带 `error_category=workspace_changed`、
  `required_action=<same shell tool>` 和 `retry_original=true` 的可恢复失败。并发
  Dependency Installation 或 Cleanup 不得将该执行前竞争升级为 `internal`
  Terminal；
- Tool Failure Completion Repair 只按连续无结构化进展的次数耗费预算。新的 Tool
  Batch 必须重置 No-progress Counter；累计 Repair Step 仍计入有界 Turn Step，
  连续文本承诺不能无限延长 Turn；
- Provider `end_turn` 只表示一次模型采样结束。任何执行过 Tool 的 Turn 只有在
  `turn_complete` Declaration 被接受且 Pending Actions 为空后才能完成。只读
  Declaration 绑定 Mutation Revision 0 且不含 Changed Paths；`workspace_change`
  还必须绑定当前非零 Mutation Revision 与精确 Changed Paths、Verification Passed
  且 Journal Commit；
- `turn_complete` 必须是所在 Tool Batch 的唯一 Call。Gate 必须在模型收到
  `required_action=final_answer` 前完成；User-facing Final Answer 之后不得再启动
  新的 Verification Pass；
- Completion Gate 在任意 Tool 执行后启用。纯文本 Answer 可直接完成，但执行过 Tool
  的 Answer 即使只读，也必须提交结构化 Completion Declaration。Verification Gate
  在显式 `workspace_change` 或任意实际 Workspace Mutation 后启用；Answer 一旦发生
  Mutation，不能绕过 Declaration、Verification、Journal 或 Receipt；
- Gate 通过前的模型 Text 是 Provisional Output，不能投影到稳定 Transcript。
  Accepted Declaration、Verification 和 Journal Commit 完成后才能发布 Final Answer；
- rejected `turn_complete` 必须产生配对的 `tool.result`，携带
  `accepted=false` 和结构化 `rejection`。只有 accepted Declaration 才要求 Runtime
  绑定 Completion Call ID。只读接受结果绑定 Revision 0 且不含 Changed Paths；修改型
  接受结果还绑定精确 Changed Paths 和非零 Mutation Revision。拒绝结果不能因缺少这些
  绑定字段转成 `internal` Failure；
- `turn_complete` 的模型可见 Content、Event Metadata 和 UI Projection 必须表达同一
  accepted/rejected 决策。Executor 不得在 Runtime Validation 前声称 Declaration
  已记录或要求输出 Final Answer；
- malformed `turn_complete` 必须保持拒绝，不能静默规范化。结构化 Result 使用
  `reason=invalid_declaration`，在 `error_detail` 和
  `completion_declaration_error` 中保留 JSON Schema 失败详情，并要求模型使用顶层
  `status`、`summary` 和 `pending_actions` 重试；
- 后续任何 Mutation 或 Verification Repair 都会使 Completion Declaration 失效。
  模型必须声明 `status`、`summary` 和 `pending_actions`。`status=complete` 要求显式
  空数组；`status=incomplete` 要求列出具体 Pending Actions，Runtime 以
  `required_action=continue_work` 拒绝该声明并继续同一个 Turn，且不得授权 Terminal
  Completion。Runtime 只为 accepted complete Declaration 绑定 Completion Call ID，
  以及只读 Revision 0，或精确 Changed Paths、当前 Revision 的 Accepted Quality Call ID
  和非零 Mutation Revision。模型不得复制或构造这些 Runtime Facts；
- Completion Repair Budget 只在 Mutation Revision 与 Accepted Quality Evidence
  均未变化时累计重复尝试；新的 Mutation Revision 或新 Accepted Quality Evidence
  必须重置 No-progress Counter；
- Completed `workspace_change` 必须具有 `status=passed` 的 Verification Receipt；
  `off`、`skipped`、`unavailable` 和 Soft-reported 结果都必须 Fail Closed；
- 成功的受保护 File Edit 必须使用 Journal After-image 刷新 ReadTracker，从而在
  保留 Stale-write 检查的同时支持连续编辑；
- Structured Recovery Evidence 必须遵守独立 Byte Budget，且只能作为审计上下文，
  不能授权 Replay 历史副作用；
- Compaction 必须减少 Retained Bytes，并把 History 降回配置预算；
- Completed、Failed、Canceled 必须经过同一个 Byte/Token Context Gate；Receipt
  使用 Terminal Event 冻结的 Budget Snapshot，不能读取可变的旧 History；
- Failed Terminal 可以在最后一个 Durable Completed Turn 内按闭合 Tool Pair
  Boundary 压缩，但不得持久化当前 Failed Transaction；
- 纯文本 `workspace_change` 在 No-change Contract Fail Closed 前必须获得一次结构化
  Completion Repair（`required_action=perform_workspace_mutation`）；
- `exec_command.write_globs` 必须在 Approval 前展开为最多 512 个已存在的精确文件；
  Guard、Journal、Sandbox、Reconciliation 和 Receipt 不得接收 Runtime Wildcard
  Grant；
- Edit Match Miss 不得降级为模糊替换。存在唯一邻近 Source Anchor 时，Tool Result
  必须提供 `edit_precondition_miss`、`replace_failed_change`、从 1 开始的失败
  Change Index、Match Count 和带行号的有界当前原文；无法安全定位 Anchor 时使用
  `reread_exact_range`。两条路径均设置 `retry_original=false`，并保证整个
  Transaction 零写入；
- Diagnostics Process Failure 在没有解析出 Diagnostic 时必须标记为
  `unavailable/error_category=runner_failure`，不能标记为源码 Finding；Receipt
  使用 `failed > unavailable > passed > not_evaluated` 聚合优先级；
- Strict `workspace_change` Verification 在 Repair Budget 用尽后不能降级为 Soft
  `reported`；
- Required Verification 耗尽后必须标记为 `blocked`，不能接受或立即丢弃改动。
  Workspace Journal 以持久化未验证 Draft 暂停；Continue 恢复该 Journal，Retry
  才显式撤销它；
- Post-edit Diagnostics 不可用时，`workspace_change` 只能使用最后一次 Mutation
  之后运行的 `quality_test` 或 `quality_verify` Passed Evidence 完成；Evidence
  必须声明精确 `covered_paths` 并覆盖全部 Changed Paths；Repair Feedback 必须
  携带机器可读的 `uncovered_paths`，不得从整个 Dirty Worktree 推导 Coverage；
  普通 Shell Success 永远不能作为 Verification Evidence；
- `turn.started.workspace_isolation=worktree` 与 Terminal Receipt 必须保留隔离
  Chat Worktree Path。成功的隔离变更在执行 `Merge Chat Changes` 和
  `Apply to Workspace` 前保持 Pending；主 Worktree 的 `git status` 不能证明隔离
  变更已丢失。VS Code 必须在 Final Answer 下方使用不可折叠的 Workspace Change
  Card 显示该 Pending 状态；
- Chat Merge 最多接受 512 个 Path。Runtime 必须用一个 Aggregate Plan ID 绑定完整
  Change Set，使用一个有界 Unified Diff 进行 Preview，并在同一个 Journal
  Transaction 中按每批最多 64 个文件 Apply；任一 Batch 失败必须回滚整个 Merge；
- Main Workspace 在 Chat Baseline 后变化时，Merge 必须在 Plan 阶段执行无副作用的
  Three-way Text Merge。非重叠修改必须保留双方结果；重叠修改、Delete/Modify
  冲突与 Binary Path 仍须在 Approval 前 Fail Closed；
- 可重试 Provider Transport Failure（包括结构化 Connection Reset）必须标记为
  `unavailable`，不能标记为 `internal`。在没有任何 Meaningful Output 时，即使
  Optional Retry 被禁用，Engine 也允许一次有界重试；已经输出有效内容或 Tool Call
  Fragment 后不得 Replay；
- Terminal Cleanup Failure 必须记录为结构化 `secondary_issues`，不能替换 Primary
  Turn Failure，也不能继续通过换行拼接到 Primary Message；
- Reasoning-only `max_tokens` 最多执行一次 Low-reasoning、No-tool Finish Sample；
  包含不完整 Tool Call Fragment 的输出继续使用普通有界 Continuation；
- 同一 Workspace Snapshot 上的相同只读 Tool Call 不应被重复执行；
- Fix Request 在没有 Change 和 Verification 时不能成功完成，除非结构化 Outcome
  明确解释原因。

缺少 Terminal Event、Tool 未配对、False Completion、进程退出丢失和 Compaction
放大都按 Release-blocking Finding 处理。

## 停止与冻结证据

1. 等待最后一个 Turn Terminal Event 与 Checkpoint。
2. 执行 `CodeHelper: Stop Runtime Capture`。
3. 确认存在 `capture.stopped`；如果只能重启 Extension Host，必须在报告中说明。
4. 记录文件 Size、Line Count、最终修改时间与 SHA-256。
5. 确认文件不再增长，Runtime 恢复 `state=ready`。

示例：

```bash
wc -l "$CAPTURE"
stat "$CAPTURE"
shasum -a 256 "$CAPTURE"
jq -r '.kind' "$CAPTURE" | sort | uniq -c | sort -nr
```

## 结构化分析

先检查 Count 与 Pairing，再读取大 Payload：

```bash
jq -r '
  select(.kind == "runtime.event") | .data.event.kind
' "$CAPTURE" | sort | uniq -c | sort -nr

jq -c '
  select(.kind == "runtime.event")
  | .data.event
  | select(
      .kind == "turn.failed"
      or .kind == "operation.rejected"
      or (.kind == "tool.result" and .data.is_error == true)
    )
' "$CAPTURE"

jq -c '
  select(.kind == "runtime.event")
  | .data.event
  | select(.kind == "turn.compaction")
  | {
      turn_id,
      original: .data.original_bytes,
      retained: .data.retained_bytes
    }
' "$CAPTURE"
```

Latency、Token、Cost、Verification、Changes 和 Context Budget 的聚合事实以
`turn.receipt` 为准。报告必须保留失败 Case 及恢复路径，不能只报告最终成功尝试。

## 报告契约

工作报告存放在 Tracked Product Documentation 之外：

```text
.tmp/runtime-monitor/<date>-<session-id>-report.md
```

每份报告必须包含：

1. Session、Thread、Runtime、Route、Posture、Sandbox 与时间范围；
2. Capture Path、权限、Record Count、Size 与 SHA-256；
3. 每个 Turn 的 Terminal State、Tool Count、Changes、Verification、Latency、
   Token、Cost 与 Checkpoint；
4. 按严重度排序的 Finding；
5. 每项 Finding 的结构化证据、复现、影响、归因、恢复与建议测试；
6. 健康不变量和剩余 Coverage Gap；
7. 最终优化优先级。

严重度使用 `Critical`、`High`、`Medium`、`Low`。没有 Protocol Event、Receipt、
Process State、Database Fact 或 Log Line 支持的内容只能标为 Observation，不能确认为
Defect。

## 优化闭环

对每个确认的 Defect：

1. 固定失败 ID 与最小复现；
2. 在 Ownership Boundary 增加聚焦回归测试；
3. 修复时不得削弱 Guard、Approval、Journal 或 Sandbox；
4. 按影响范围执行 Package Test 与更广检查；
5. 重新构建并安装 Target VSIX；
6. 用新的 Capture 重复同一 Session 测试矩阵；
7. 在报告中对比修复前后的结构化事实。

只有原始结构化失败不再出现，并且周边不变量仍通过，才能认为优化完成。
