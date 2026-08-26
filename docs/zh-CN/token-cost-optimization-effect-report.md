# 长会话 Token 与调用开销优化实测报告

> 日期：2026-08-27
> Workspace：CodeHelper
> Route：`deepseek-v4-flash`
> Session：`session_web_a1be7997-2391-405b-9c6c-8046627c9042`

## 结论

P0/P1 已实现。迁移前真实长会话与迁移后严格追加 E2E 共同证明：

- 同一 Turn 的追加 Sample 可达到 `99.9%` 左右 Provider Cache 命中；
- 旧 Responses 路径虽报告约 `229K` 逻辑公共前缀，跨 Turn 首次调用实际只命中 `11.53%`；
- 改为 OpenAI-compatible Chat 且锁定严格追加后，受控跨 Turn E2E 命中
  `71,936 / 72,039 = 99.86%`，第三 Turn 保持 `71,936 / 72,052 = 99.84%`；
- 正式 Engine 两 Turn Live E2E 命中
  `84,224 / 84,247 = 99.97%`，且第二 Turn 的 Prefix Manifest 为
  `PrefixCompared=true`、`PrefixMonotonic=true`；
- 冷启动与热化样本 TTFT 波动明显，不能把单次最低值当成稳定收益；
- 工具辅助 Turn 通常仍产生一次 Declaration Repair，Provider Calls/Turn 仍有优化空间。

因此当前状态应定义为：**跨 Turn 缓存前缀问题已在 Wire 和正式 Engine 真实 E2E 中通过验收；完整工作流的
Token 降幅和 TTFT 仍需继续按 Session 级样本统计，不能由缓存命中率直接替代**。

## 实测方法

在同一 Workspace、Session、Route、无附件条件下连续提交相同只读任务。任务要求读取三个固定文件并输出
固定格式结论。记录每个 `usage` 与 `turn.receipt` 的输入、缓存、公共前缀、TTFT、调用次数和压缩次数。

## 结果

### 迁移后严格追加验收

测试使用默认 `deepseek-v4-flash` Route、`/chat/completions` 和随机冷前缀。后续请求只在
前一请求末尾追加 assistant/user 消息；Engine 测试同时验证工具调用后的 Sample 与下一 Turn
均保持消息前缀逐字节不变。

| 层级 | Turn | Input | Cached | 命中率 |
| --- | --- | ---: | ---: | ---: |
| Wire | 冷启动 | 72,026 | 0 | 0% |
| Wire | 第二 Turn | 72,039 | 71,936 | 99.86% |
| Wire | 第三 Turn | 72,052 | 71,936 | 99.84% |
| Engine | 冷启动 | 84,226 | 0 | 0% |
| Engine | 第二 Turn | 84,247 | 84,224 | 99.97% |

真实测试：`TestDeepSeekChatAppendOnlyCache`、
`TestDeepSeekEngineCrossTurnAppendOnlyCache`。Wire 契约：
`TestCompatibleChatWireRequestIsStrictAppendOnly`。Engine 契约：
`TestPrefixManifestContinuesAcrossTurns`、
`TestToolResultRequestRemainsPrefixOfNextTurn`。

### 迁移前长会话基线

| 阶段 | Turn | Calls | Input | Cached | 命中率 | TTFT | Total |
| --- | --- | ---: | ---: | ---: | ---: | ---: | ---: |
| 变更后冷样本 | `turn_2dddef...` | 2 | 476,645 | 3,584 | 0.75% | 17.99s | 43.56s |
| 同任务热化 | `turn_ae0d7d...` | 3 | 726,324 | 485,760 | 66.88% | 10.17s | 25.88s |
| 单调用复查 | `turn_fdf3b4...` | 1 | 243,777 | 2,176 | 0.89% | 16.18s | 18.84s |
| 固定工具任务冷样本 | `turn_d00134...` | 3 | 813,507 | 298,496 | 36.69% | 18.16s | 41.24s |
| 固定工具任务热样本 | `turn_1f0e2a...` | 3 | 829,007 | 336,384 | 40.58% | 8.77s | 29.12s |
| append-only 重启样本 | `turn_9fd08c...` | 3 | 843,738 | 344,448 | 40.82% | 10.13s | 31.87s |
| append-only 跨 Turn 样本 | `turn_4fd189...` | 3 | 857,893 | 352,640 | 41.11% | 19.67s | 42.71s |

热化样本相对冷样本的 TTFT 最佳降幅为 `43.5%` 至 `51.7%`，但后续样本回升，不能视为稳定 SLA。
同 Turn 最后一次 Sample 的命中率为 `99.90%`、`99.93%`、`99.91%`、`99.93%`、`99.94%`。

append-only 跨 Turn 样本首调用：

- `PrefixMonotonic=true`
- `PrefixCommonTokens=229,234`
- `PrefixFirstDivergence=505`
- `PrefixDivergenceKind=history`
- Provider 实际缓存：`32,768 / 284,294 = 11.53%`

这证明旧 Responses 路径中，Runtime 的逻辑公共前缀没有转化为等长的 Provider 缓存命中。
该结论是迁移前基线，不再代表当前默认 Chat 路径。

## 正确性与开销

- 所有受控实测 Turn 均正常完成，无 Tool/Result 因果链断裂。
- 实测 Turn 的 `compactions=0`，没有因优化增加压缩。
- P0/P1 实施期间发现并修复了写预留固定放大、Plan 重试身份、rejected Turn 残留、
  Plan Recovery 授权继承和 Prefix 统计重复计权问题。
- 多数工具 Turn 为 `2 normal + 1 declaration_repair`，说明结构化终态约束正确生效，
  但额外 Sample 仍会增加 Token 与延迟。

## 后续验收

下一阶段应继续：

1. 在完整 CodeHelper 工具工作流中分离首调用、同 Turn 后续调用和跨 Turn 调用；
2. 在多个独立 Session 重复严格追加实验，报告中位数与分位数；
3. 统计 Declaration Repair 对 Calls/Turn、Token 与 TTFT 的贡献；
4. 在 Session 级整体 Token 降幅达到目标前，不宣称实现 `45%～65%` 的整体降幅。
