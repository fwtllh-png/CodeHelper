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
- Plan Implement/Autopilot 必须以 `intent=workspace_change` 启动；
- Failed Turn Receipt 不携带成功 Outcome；Completed `workspace_change` Receipt
  必须同时具有 `outcome=changed`、Observed Changes 和 Changed Workspace Outcome；
- `shell_read` 与 `terminal_run` 不能修改 Workspace 文件；`shell_run` 默认仍为
  Read-only，仅可通过 `write_paths` 声明最多 128 个已存在的精确文件，并且所有
  声明写入必须经过 Approval、Journal、TurnDiff、Sandbox Enforcement 与 Receipt；
- File Mutation Tool 的 Preview、Approval、Revalidation 与 Commit 必须整体串行；
  `edit_plan_stale` 只能通过新的 Read、Plan 与 Approval 恢复；
- Fatal Tool Batch 必须在 Turn Failed 前为每个已 Start Call 发出且仅发出一个
  Structured Result；
- 未解决的结构化 Tool Failure 不能由承诺未来动作的文本清除；`workspace_change`
  与 `operation` Turn 必须有后续成功 Tool Batch 及恢复后的 Completion Check；
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
- Post-edit Diagnostics 不可用时，`workspace_change` 只能使用最后一次 Mutation
  之后运行的 `quality_test` 或 `quality_verify` Passed Evidence 完成；Evidence
  必须声明精确 `covered_paths` 并覆盖全部 Changed Paths，普通 Shell Success
  永远不能作为 Verification Evidence；
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
