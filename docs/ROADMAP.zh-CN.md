# CodeHelper 增强路线图

> 版本：2026-08 规划稿  
> 能力目标：达到成熟 Coding Agent 的工程深度，同时保持独立的产品形态。
> 核心定位：**安全、可审计、可扩展的本地 Coding Agent Runtime，并同时提供 TUI、CLI、VS Code、HTTP/SSE、ACP 等一致体验。**

---

## 1. 结论先行

CodeHelper 的主要优势已经不是“有没有工具”，而是：

- 四层安全模型、OS 沙箱与统一 Guard；
- Runtime 与 Host 分层，多种客户端共享 Operation/Event；
- durable eventlog、workspace journal、compact、revert；
- Provider、MCP、Skill、Hook、Subagent、Workflow 等完整骨架。

当前真正限制体验的，是部分关键能力停留在“骨架可用”而不是“默认可靠”：

1. 仓库理解已有词法符号索引、仓库地图与自动演进的 WorkingSet，但仍是词法级：无类型解析、无调用图、无语义检索；
2. 编辑后验证是工具能力，还不是强制闭环；
3. 长会话 compact、revert、crash resume 的语义完整性不足；
4. Subagent、Task、Fleet、Workflow 的控制面强于真实执行面；
5. TUI 很完整，但缺少编辑器原生上下文与交互；
6. usage / 成本 / 延迟已经贯通到三个读面（M3 的 §8.4），按用途分路也已经能配（§8.2 T1–T5），provider/web 出网经 Gate 且 keyring 有运行面（§8.6 T1–T3），prompt cache 写面已接（§8.3：Chat/Responses sticky key + Anthropic `cache_control`）。还缺的是仓库理解深度、编辑闭环、编辑器原生体验，以及 §8.6 明确后置的 secret/shell Dial/SBOM。

因此后续不应继续横向堆工具，而应集中建设六条主线：

| 优先级 | 工程主线 | 目标 | 交付 |
| --- | --- | --- | --- |
| P0 | Coding Intelligence（§4） | 找对代码、理解影响范围 | M1 已完成 |
| P0 | Correctness Loop（§5） | 改完自动证明正确 | M1 已完成；§5.4 checkpoint 并入 M2 |
| P1 | Agent Execution（§7） | 真子 Agent、真后台任务、真工作流 | M2 |
| P1 | Runtime Platform（§8） | 长会话、模型、成本、安全、可观测 | M3 |
| P2 | Ecosystem & Governance（§9） | MCP/Skill/Plugin 生产治理（企业治理与 SDK 本轮不做） | M4 |
| P2 | VS Code Extension（§6） | 获得编辑器原生体验 | M5 |

主线编号（§4–§9）是结构编号，交付顺序见 §10；两者不一致是有意的——VS Code 是主线三但排在最后交付，理由与代价记在 §10「本次顺序调整与其代价」。

VS Code 插件不是重写一套 Agent，而是 **CodeHelper 的新 Host**：

```text
VS Code Extension (TypeScript)
  ├─ Chat / Inline Edit / Diff / Approval / Tasks / Context UI
  ├─ VS Code Context Bridge
  └─ ACP+ / local JSON-RPC
                │
                ▼
CodeHelper Runtime (Go)
  ├─ Thread / Engine / PromptContext / Provider
  ├─ Guard / Policy / Constitution / Sandbox
  ├─ Tool / MCP / Skill / Subagent / Workflow
  └─ Eventlog / CAS / WorkspaceJournal / Usage
```

原则是：**插件负责感知和呈现，Runtime 负责决策和执行。**

---

## 2. 产品目标与边界

### 2.1 北极星体验

用户在终端或 VS Code 中提出一个真实开发任务后，CodeHelper 应稳定完成：

```text
读取约定
  → 建立仓库地图与任务 WorkingSet
  → 形成可执行计划
  → 最小范围修改
  → 自动诊断、测试和复核 diff
  → 失败时受预算约束地修复或回滚
  → 输出可审计的证据与结果
```

### 2.2 不改变的架构原则

1. **一套 Agent 循环：** TUI、VS Code、HTTP、ACP、Subagent、Task 共享 Runtime。
2. **一套安全入口：** 所有工具与后台执行都经过 Guard，Host 不直接执行模型决策。
3. **一套事实流：** 状态变化先定义 Operation/Event，再由各 Host 呈现。
4. **一套执行证据：** 搜索、编辑、验证、审批、compact、记忆都产生 receipt。
5. **安全不为体验让路：** VS Code 插件也不能绕过 constitution、sandbox 和 workspace journal。

### 2.3 明确不做

- 不为模仿其他产品而重写 Runtime 或抛弃 TUI；
- 不在第一阶段建设全量向量数据库；
- 不在 Subagent 仍为 stub 时宣传“多 Agent 完成”；
- 不在 `web_run` 仍为 fake runtime 时宣传完整浏览器自动化；
- 不用 Skill 数量掩盖单 Agent 深度不足；
- 不让插件直接写文件、跑 shell，形成第二套不可审计执行路径。

**本轮划出范围（不是永久否决，只是不排进 §10 的 M2–M5）：**

- 企业治理（§9.2）：组织级 policy 分发与只读锁定、provider allowlist 与数据驻留、配额、审计导出、可复现执行包；
- SDK（§9.3）：Go / TypeScript / Python client 与 Tool/Hook/Skill 脚手架。只保留其中第 6 条 contract tests 与 provider fixture server，并上提到 M2 当常设门禁；VS Code 插件按 §6.1 的薄插件原则直连 ACP+，不先为它建一层 TS SDK；
- 真实 browser 与多模态（§8.7）：browser 继续是 fake，按上面第四条不对外宣传。

---

## 3. 已确认的现状差距

以下差距来自当前代码，不是外部对比表的推测。

| 能力域 | 已有基础 | 关键缺口 |
| --- | --- | --- |
| 仓库理解 | `search_*`、fuzzy path、LSP diagnostics、`project_map`、`AGENTS.md`、词法符号索引 + `search_symbol`/`search_definition`/`search_references`/`search_related_tests`、每采样重建的 Repo Map 与自动演进 WorkingSet（[RFC-001](./rfc/RFC-001-repo-context.zh-CN.md)） | 索引为词法级（无类型解析与调用图）；Repo Map 的 L1 只给声明轮廓、无依赖邻域；工作集账本不持久化（`--resume` 后从空重建）；search 命中未计入来源；submodule 不可见 |
| 编辑 | exact edit、unified patch、`file_apply` 事务（write/edit/move/delete + dry-run 预览）、观测式 TurnDiff、workspace journal（落盘 + 跨进程恢复） | 缺有限模糊 edit 与 hunk 级失败诊断；已 commit 的 turn 跨重启不可 revert |
| 验证 | post-edit diagnostics、`quality_*`、verifier role、revert、turn 级 Verify Gate（默认 `soft + diagnostics`，另有 `repository`/`affected` scope） | `affected` 只覆盖 Go 与 Python，其余语言诚实报不可用；修复轮无独立 token 预算 |
| 上下文 | token/byte budget、context receipts、durable window、结构化 compact（六段机械派生 + 上一次摘要整块结转，[RFC-009](./rfc/RFC-009-context-lifecycle.zh-CN.md)） | 账本不持久化（`--resume` 后结构化段从空重建，只剩叙事）；无多级摘要树；无模型叙事摘要与 compact 独立预算；revert 重放截断目前不完整 |
| Subagent | route、mailbox、graph、生命周期、`agent_*`、真实 child turn、`agent_merge` 合回主工作区（RFC-006 D1–D10） | — |
| 后台任务 | Task FSM + lease/attempts、常驻 scheduler（claim / reclaim / automation 三循环 + drain）、按规范化 workspace root 隔离的跨 session claim、`agent_turn` / `workflow_run` / `shell_command` 三类 executor、`codehelper worker`、Automation、Lane；隔离写 `agent_turn` 成功后经 `agent_merge` + parent journal 合回宿主工作区；Fleet 降级为只读审计 | 后台无交互审批；`workflow_run` / `shell_command` 只有显式声明幂等才允许 task-level retry |
| Workflow | IR、JS VM、runtime driver、DAG 依赖与波次调度、`parallel` 转 join、condition、节点级 retry/timeout（attempt context 下传到底层 turn，取消收敛后才 retry）、节点 checkpoint 与跨进程续跑；`response_schema` production 后置校验 | profile 尚无 registry，非空值 fail-closed；上游节点输出不注入下游 prompt；节点隔离粒度仍是整个 run 一个工作区，因此共享可写 workspace 的 production turn 为保证 journal 原子性按 whole-turn 串行 |
| Shell | PTY、persistent/background session、timeouts、env allowlist、foreground 实时 `tool.output`、后台输出落盘（`joblog`）并按 cursor 补齐 | Windows 无强 sandbox/PTY 时仍是 partial；terminal/background/task 三条 shell 尚未共享状态机 |
| Git 托管 | 本地只读 git、PR/issue context、comment、preflight | 无安全的 commit/branch/push/PR create/review submit |
| Web | search/fetch/scrape、browser interface | browser 实际为 fake；主 Provider 无原生 image block |
| Usage | token、reasoning、cached token、cost 与延迟按 sample 落库（[RFC-008](./rfc/RFC-008-execution-receipt.zh-CN.md) T1–T5a），本地 `spans` 表与 CLI/TUI/HTTP 三个同源读面；工具自己发起的采样（`image_analyze`、`sub_query`）也记在发起它的 turn 名下（§8.2 T2）；prompt cache 写面已接（Chat/Responses `prompt_cache_key`、Anthropic 稳定前缀 `cache_control`） | usage 行按 provider/model 归属而不带 purpose 列，所以 vision 槽位与 act 指向同一模型时两类调用在 rollup 里分不开；`spans` 无保留策略；不导出 OTLP（刻意，RFC-008 D5） |
| 安全 | policy、permissions、constitution、sandbox；provider/web 经 egress Gate + deny 审批重试；OS keyring | sandbox `AllowNetwork` 仍为 true；shell Dial / secret read / SBOM 后置（RFC-011 §5） |
| 客户端 | 成熟 TUI、CLI、HTTP/SSE、ACP+（Operation 信封、游标回放、重启重绑）、最小 WebUI | 无 VS Code 原生客户端（M5）；ACP 缺附件与 thread 列表 |

---

## 4. P0 主线一：Coding Intelligence

目标不是再增加一个搜索工具，而是建立可持续演进的仓库理解层。

### 4.1 Repo Index

**已有基础：** `search_text`、`search_files`、`search_project`、LSP、fuzzy path。

**增强项：**

1. **[已完成]** 建立轻量增量索引：
   - 文件、语言、符号、定义、引用、测试映射；
   - 基于文件 digest 增量刷新；
   - 支持 ignore、generated/vendor、大文件策略；
   - 首期采用语言启发式 + LSP，可选 tree-sitter，不强制向量服务。
2. **[已完成]** 增加一等工具：
   - `search_symbol`
   - `search_definition`
   - `search_references`
   - `search_related_tests`
3. **[已完成]** 索引可离线重建，缓存损坏不能阻塞基础 grep/search。
4. 多 workspace root 独立索引，支持 VS Code multi-root。（表结构已按 `root_path` 留位，实际仍只索引单根）

**Repo Index 对本节的偏离（全部记入 [RFC-001](./rfc/RFC-001-repo-context.zh-CN.md)）：** 符号抽取只做语言启发式（Go / Python / JS+TS / Rust / Java），**没有**接 tree-sitter，也**没有**用 LSP 的 `workspace/symbol` 与 `textDocument/definition`——结果统一标注 `resolution: "lexical"`，注释与字符串里的假阳性是已知边界。`search_references` 是词法词边界扫描而非反向索引。文件枚举改为单次 `git ls-files`，代价是 submodule 内文件不可见，且 `vendor`/`node_modules` 这类需要作为显式跳过表保留在遍历层（git 只在它们真被 ignore 时才跳）。刷新为按需 digest 比对，**没有**文件系统 watch。`affected` scope 的测试映射只覆盖 Go（包）与 Python（pytest 文件），其余项目类型返回 `unavailable` 并说明原因而非静默判过。

### 4.2 分层 Repo Map

Repo Map 不应是一次性目录树，而应按 token 预算动态组织：

- **[已完成]** L0：模块、入口、构建系统、主要目录；
- **[部分完成]** L1：当前任务相关符号及其依赖邻域（只给工作集文件自身的声明轮廓，**没有**依赖邻域）；
- **[部分完成]** L2：按需文件内容、LSP 信息和测试（显式 `@file` / `--file` 走内容注入；自动工作集只给路径，内容由模型自取）；
- L3：大结果通过 handle 延迟读取。

**[已完成]** Repo Map 作为新的 PromptContext partition，必须具备 digest、预算、截断原因和 receipt。

### 4.3 自动 WorkingSet

WorkingSet 应从静态输入升级为 turn 内动态状态：

- **[已完成]** 用户显式 `@file`（`codehelper exec --file`）、`@symbol`（符号级 pin 未做）；
- VS Code 当前文件、选区、打开标签页（RFC-004 范围）；
- **[部分完成]** search/LSP 命中（search 命中经新的 `MetadataEvidence` 契约以 `SourceSearch` 计入，权重低于 `read`；LSP 仍只接诊断）；
- **[已完成]** TurnDiff 与 diagnostics；
- **[部分完成]** git diff、失败测试涉及路径（验证门禁跑过的路径已计入；git diff 未做）；
- Subagent evidence。

**[已完成]** 每个条目记录来源、相关分、最后访问 turn 和是否 critical。Compact 时优先保留 critical path，低相关项自动衰减。

**Repo Map / WorkingSet 对 §4.2–§4.3 的偏离（全部记入 [RFC-001 §10](./rfc/RFC-001-repo-context.zh-CN.md)）：** 两段不是"再加一个 bootstrap 分区"，而是把 PromptContext 拆成**稳定前缀 + 每次采样重建的易变尾块**，尾块以 `RoleSystem` 追加在 history 之后且不入 history（否则每次上下文变化都会作废其后整段 history 的 prefix cache）；`plan` 也一并移到尾块。仓库地图**每 turn 只取一次索引快照**，所以同 turn 内新读文件的符号轮廓要等下一 turn。L1 只给声明轮廓，**没有**依赖邻域（索引是词法的，跨文件邻域需要真解析）。账本**不持久化**，`--resume` 后从空重建。Anthropic 侧已把 system 发成 text block 数组：稳定前缀末块可带 `cache_control`，history 之后的 turn 尾块不加断点（§8.3）。

### 4.4 EvidenceSet 与 Agent 搜索策略

Engine 维护结构化 EvidenceSet：

- 当前假设；（**不做**：假设无法观测，要它就得让模型自己申报——一个会自述证据的模型同样会自述"我验证过了"）
- **[已完成]** 支撑它的文件、符号、搜索结果；（命令结果**不做**：shell stdout 无结构，解析出来的"事实"不比没有更可信）
- **[已完成]** 尚未验证的风险（改了没验证 / 改了没读过 / 诊断未清，从事实差集派生，证据一到自己消失）；
- **[已完成]** 修改所依赖的 read receipt（`MarkChanged` 带 `hadRead`，来源是工作集账本而不是模型自述）。

增加可测试的 coding policy：

1. **[已完成，由机制保证]** 先读取仓库约定和构建入口（`AGENTS.md` 进 `repository_instruction` 分区，构建文件与入口点进仓库地图；policy 正文只作解释）；
2. **[已完成]** 搜索结果按定义、引用、测试、配置分类（`tool.MetadataEvidence` 契约由工具产出；`test`/`config` 是**路径名启发式**，不是类型信息）；
3. **[已完成，硬约束]** 修改前必须有 read/evidence（guard 三处 `ValidateWrite` 硬拦，失败按可恢复失败回灌模型）；
4. **[已完成]** 修改后先做受影响范围验证，再扩大验证（`verify` 的 `scope = "affected"`，随 §4.1 的索引打开）；
5. **[已完成]** 重复搜索、重复读取、未消费结果的调用触发软提醒（**软的**：照常执行，只在下一次采样的尾块里说一句）。

**EvidenceSet / coding policy 对本节的偏离（全部记入 [RFC-001 §11](./rfc/RFC-001-repo-context.zh-CN.md)）：** 账本**只记观测到的事实与从事实差集派生的风险**，"模型自述的假设"与"命令输出作为证据"明确不做。提醒是软的，唯一的硬约束仍是 read-before-edit。分类由工具在结果元数据里给出，纯文本搜索的 `test`/`config` 靠路径名启发式，准确率没有量化。静态 coding policy 进**稳定前缀**（≤700 字节、仅在 `execution.Tools` 为真时注册），易变证据进 §4.3 的**尾块**且内部顺序为"提醒 → 风险 → 事实"（截断保前缀）。账本**不持久化**，`--resume` 后从空重建。compact 只追加一行 `UnverifiedChanges:`，完整的结构化 compact 属于下一条主线。

### 4.5 可选语义检索

只在符号索引和基准证明不足后引入：

- embedding 是可选后端，不是 Runtime 必选依赖；
- 仅索引代码摘要、文档和符号，不默认上传完整源码；
- 结果必须携带文件、行号、索引版本和置信度；
- 无语义服务时自动退回 symbol/text search。

### 4.6 验收指标

- Top-K 相关文件召回率；
- 找到定义/调用方的平均工具步数；
- 无效读取字节和重复搜索率；
- WorkingSet token 占用与任务成功率；
- 1 万+ 文件仓库增量更新耗时。

---

## 5. P0 主线二：Correctness Loop

目标是从“模型可以调用测试工具”升级为“有修改就必须形成验证结论”。

### 5.1 Verify Gate

新增配置：

```toml
[execution.verify]
mode = "soft"          # off | soft | hard
max_repair_steps = 3
on_failure = "ask"     # fail | ask | revert
scope = "affected"     # diagnostics | affected | repository
```

- `off`：保持现状；
- `soft`：自动运行诊断并强制把失败回灌模型；
- `hard`：turn 完成前必须通过配置的验证；
- 修复轮占用独立且有上限的 step/token/time 预算；
- 超限后按策略失败、询问或回滚，禁止无限循环。

### 5.2 分层验证

验证从便宜到昂贵逐层扩大：

1. 语法/格式；
2. 当前文件 LSP diagnostics；
3. 受影响 package/module 测试；
4. 静态检查；
5. repository verify；
6. 可选 verifier Subagent。

语言和仓库探测生成 verify profile，用户可在 `.codehelper/verify.toml` 覆盖。

### 5.3 Edit Transaction

多文件修改采用事务式流程：

```text
prepare edits
  → validate resources/read receipts
  → preview diff
  → atomic apply
  → diagnostics
  → verify
  → commit receipt or rollback
```

增强内容：

- 有限模糊 edit：仅在唯一匹配时成功；
- hunk 级 patch 失败诊断；
- rename/move/delete 一等工具；
- 可选 LSP rename、organize imports、format；
- 二进制、编码和换行策略显式化；
- 中途失败不能留下半应用状态。

**已实现（[RFC-005](./rfc/RFC-005-edit-transaction.zh-CN.md)）：** `file_apply` 以"一次工具调用 = 一次事务"落地上面的流程（校验 → `dry_run` 预览 → 原子写入 → 诊断 → 门禁 → 收据/回滚），`move`/`delete` 作为 op 而非独立工具，中途失败回退已写文件。**未实现：** 有限模糊 edit、hunk 级诊断、LSP rename/format、CRLF 与编码策略。**已补（M2，RFC-005 §11）：** journal 账本与 before-image 落盘到 `{workspace}/.codehelper/journal`，下一个进程启动时回滚被杀进程留下的未 commit turn，已 commit 的保留，owner 仍活着的跳过。**残余风险：** 已 commit 的 turn 跨重启不能 revert；恢复只管工作区文件，不动线程历史。

### 5.4 Checkpoint、Revert 与恢复正确性

> 归入 M2：后台任务与 workflow 节点要跨重启恢复，前提是 journal 与 checkpoint 先落盘，所以本节与 §7 同批交付而不是留在 §5 无主。

1. 建立 protocol TurnID 与 Engine numeric turn 的持久映射；
2. 修复 reconstruct 中 revert/backtrack 无法真正截断历史的问题；
3. **[已完成]** Verify Gate 前建立轻量 workspace checkpoint —— journal 在每次写入之前把 before 记录与 before-image 落盘（`{workspace}/.codehelper/journal`），门禁失败走进程内回滚，进程被杀走下一个进程的 `Recover`（RFC-005 §11）；
4. revert 同时恢复文件、模型可见历史和 WorkingSet；
5. 非文件副作用明确标记“未回滚”，不能假装原子。

第 1、2、4 条仍未做：跨重启恢复的是**工作区**，不是模型可见历史。

### 5.5 Execution Receipt

每个完成 turn 输出统一收据：

- 目标与计划；
- **[已完成]** evidence（观测到的事实 + 未验证的风险 + 软提醒，见 §4.4）；
- **[已完成]** 读取和修改路径；
- diff 摘要；
- diagnostics/tests/verify；
- approval 与 sandbox mode；
- token、cost、latency；
- 未解决问题和未回滚副作用。

### 5.6 验收指标

- 未验证修改交付率趋近 0；
- patch 首次成功率与歧义拒绝率；
- Verify Gate 失败自动修复率；
- 多文件事务回滚正确率；
- restart/revert 后历史与工作区一致率。

---

## 6. P2 主线三：VS Code Extension

> M5 范围（§10）：本主线整体后置为最后一个里程碑，只有 §6.2 的 V0 协议地基已在 M1 落地。后置的理由与代价见 §10「本次顺序调整与其代价」。

VS Code 能提供终端无法天然获得的编辑器上下文、原生 diff、诊断、代码导航和低摩擦审批。它是最重要的客户端增强，但不是最紧迫的——插件呈现的深度取决于 Runtime 的深度，所以顺序是先把执行面、经济性与工具治理做实，再一次性交付原生体验。

### 6.1 架构决策：薄插件 + 本地 Runtime

插件使用 TypeScript 实现，但不复制 Agent 逻辑：

```text
Extension UI
  → Context Bridge
  → Transport Client
  → codehelper host --adapter acp-v2
  → Runtime Operation/Event
```

职责边界：

| VS Code Extension | CodeHelper Runtime |
| --- | --- |
| 获取用户允许的编辑器上下文 | 选择和裁剪模型上下文 |
| 渲染 chat/diff/plan/task | Agent loop 与 provider |
| 展示审批并提交 decision | Guard、policy、constitution |
| 应用经过确认的 WorkspaceEdit | workspace journal、edit transaction |
| 显示 diagnostics/test 状态 | 调度诊断、测试、Verify Gate |
| 管理进程连接和版本协商 | 持久化 thread/event/usage |

插件不得直接依据模型文本执行 shell 或静默修改文件。

### 6.2 Transport：ACP+ / Local RPC

V0 ACP+ 已落地（[RFC-003](./rfc/RFC-003-vscode-transport.zh-CN.md) T0）：事件订阅改为会话级长订阅，`session/submit` 承载任意 Operation（approval/input/steer/compact/fork/revert），`session/replay` 提供有界游标回放，`session/load` 支持重启后按 `(sessionId, threadId)` 重绑，`initialize` 改为结构化协商（`protocolVersion`/`methods`/`operations`/`events`）。

仍待补齐：

- thread list/create/backtrack 等目录型方法（HTTP 已有）；
- attachment/selection/context receipt；
- 恢复后在原 thread 上继续开新 turn（V0 的 `session/load` 只恢复事件路由与回放）；
- 动态工具注册与 Workspace Trust。其中动态工具注册要等 M4 把 dynamic catalog 接进生产 wiring（§9.1 第 1 条），协议侧只需在那时补一个目录 generation 变更通知，不必自建一套注册表。

优先扩展 ACP 的通用能力；只在 ACP 无法表达的本地功能上增加版本化 JSON-RPC extension。不要同时维护两套完整协议。

### 6.3 Runtime 生命周期

插件负责：

1. 发现用户配置的 `codehelper` binary；
2. 可选下载官方签名 binary，并校验 checksum/signature；
3. 启动本地 stdio/socket Runtime；
4. 做版本和 capability 协商；
5. workspace 切换、窗口关闭时安全 detach；
6. Runtime 崩溃后恢复 thread/event cursor；
7. 不自动升级跨不兼容协议版本。

支持场景：

- 本地 workspace；
- multi-root workspace；
- Remote SSH；
- WSL；
- Dev Container；
- Codespaces 类远程 extension host。

路径、binary 和 sandbox 能力必须按 extension 实际运行端判断，不能假设 UI 所在机器就是 Runtime 所在机器。

### 6.4 Chat 与 Composer

第一版提供独立 Activity Bar Chat View：

- 流式正文、reasoning 状态、tool cards；
- plan、approval、input、verify、compact 卡片；
- stop、steer、retry、fork、backtrack；
- thread history 与搜索；
- `@file`、`@folder`、`@symbol`、`@selection`、`@diagnostics`、`@terminal`；
- 图片、日志、diff 等附件；
- mode、model、effort、posture、预算状态。

Composer 只发送引用和用户授权的上下文，不默认上传整个 workspace。

### 6.5 Context Bridge

插件可以提供以下候选上下文：

- active editor、selection、visible ranges；
- open tabs、recent files；
- workspace folders；
- symbols、definitions、references；
- diagnostics/problems；
- source control diff、branch；
- test failures；
- terminal selection或用户显式选择的输出；
- notebooks 与虚拟文档（后续）。

每项上下文都转为结构化引用：

```text
kind + uri + range + version/digest + source + user_explicit
```

Runtime 决定是否纳入 WorkingSet，并返回 receipt。插件展示“本轮发送了什么”，支持用户删除敏感引用。

### 6.6 原生编辑体验

分阶段支持：

1. **Diff Preview**
   - 使用 VS Code diff editor 展示 edit transaction；
   - 文件树和 hunk 导航；
   - diagnostics 与测试结果关联到具体文件。
2. **Apply**
   - Runtime 产生事务和 journal receipt；
   - 插件通过版本校验后的 `WorkspaceEdit` 应用；
   - 文件在生成后被用户修改时拒绝静默覆盖。
3. **Inline Edit**
   - 选区发起“解释、修复、重构、生成测试”；
   - 仍进入统一 thread/turn/verify，而不是独立模型调用。
4. **Code Actions**
   - 从 diagnostics 触发 CodeHelper；
   - 提供引用上下文，不绕过审批和验证。
5. **Review**
   - Problems 面板或独立 Tree View 展示结构化 finding；
   - 点击跳转 path/range；
   - 可创建修复 turn。

### 6.7 审批与安全体验

- 使用原生 modal/QuickPick/Webview 卡片显示工具、资源、命令、cwd、网络 host、sandbox mode；
- `once/session/always` scope 清晰区分；
- Workspace Trust 未开启时默认 read-only；
- 插件不能读取 VS Code SecretStorage 的明文并送入模型；
- Webview 启用严格 CSP，禁止任意远程脚本；
- 页面、MCP、terminal 输出标记为不可信内容，防 prompt injection；
- 所有审批 decision 进入 Runtime eventlog。

### 6.8 Tasks、Subagents 与后台作业

提供 Tree View：

- Threads；
- Plans；
- Agents；
- Tasks/Automations；
- Background Jobs；
- Approvals；
- Usage。

支持：

- agent 状态、消息、interrupt/follow-up/takeover；
- task pause/resume/cancel/log；
- terminal session attach；
- 后台完成通知；
- 点击结果打开 diff、日志或 evidence。

这些能力依赖真实 child runtime 和 scheduler，不应先用 UI 掩盖执行端 stub。

### 6.9 配置、分发与兼容

配置层次：

```text
CodeHelper defaults
  < user settings
  < workspace settings
  < workspace .codehelper config
  < per-session UI override
```

敏感配置进入 SecretStorage 或 Runtime keyring，普通配置进入 VS Code settings。分发支持：

- VS Code Marketplace；
- Open VSX；
- 企业私有扩展市场；
- 离线 VSIX；
- 内置/外置 binary 两种模式。

建立 extension ↔ protocol ↔ binary 兼容矩阵和最小支持版本。

### 6.10 测试与质量

- TypeScript unit tests；
- protocol contract fixtures；
- `@vscode/test-electron` extension integration；
- 本地/Remote SSH/WSL/Dev Container 矩阵；
- workspace trust、multi-root、断线恢复测试；
- diff 冲突、文件版本漂移、审批取消测试；
- accessibility、keyboard-only、theme/high-contrast 测试；
- extension activation time 与内存预算。

### 6.11 VS Code 分阶段交付

插件整体交付在 M5（§10），因此原先的四个阶段压缩为三个：V1 与原 V3 合并——插件动工时 shell 实时流、agents/tasks/jobs 事件与硬化过的工具目录都已在 M2–M4 落地，没必要先发一个看不见后台的 Companion 再回填。

| 阶段 | 交付内容 | 退出条件 |
| --- | --- | --- |
| V0 协议地基 | ACP+、结构化握手、event replay（已落地，见 §10 M1）。contract tests 已在 M2 落地为常设门禁（`make protocol-contract`），见 §11.3 | 双 Host contract fixture 覆盖每个协议面 |
| V1 Companion + 后台可见 | Chat、stream、stop、approval、thread、基础 `@file` / `@selection`、原生 diff preview 与受控 apply、diagnostics/verify 呈现、binary 生命周期与 Workspace Trust、Agents/Tasks/Jobs 视图、detach/reattach、通知 | 可完成受控单文件修改，并能观察跨窗口/重启的后台任务 |
| V2 Coding Native | selection/symbol/diagnostics context bridge、diff editor 深度集成、inline edit、code action | 真实修复任务无需切回终端 |
| V3 Production | Remote SSH/WSL/Dev Container、multi-root、签名更新、Marketplace/Open VSX/企业 VSIX | 安全/兼容/性能门禁通过 |

原 V4 的「企业策略」随 §9.2 一起本轮划出（见 §2.3），分发与签名更新保留在 V3。

### 6.12 VS Code 成功指标

- 插件激活和 Runtime ready 时间；
- 首次任务完成时间；
- `@` 上下文命中率；
- diff 接受率、冲突率、撤销率；
- 审批到操作完成时延；
- 用户在任务中切回终端的比例；
- extension 与 TUI 行为一致性；
- Remote/WSL/Container 任务成功率。

---

## 7. P1 主线四：Agent Execution

> M2 范围（§10）：§7.1–§7.4 加 §5.4，是 M1 之后的第一个里程碑。§7.5 Git 与代码托管不进 M2 退出条件——它按风险分层独立推进，只有第 1 层（本地 branch/add/commit/restore）会被 worktree 隔离顺带用到。

### 7.1 真正可执行的 Subagent

**已确认缺口：** 当前 child runtime 只生成占位结果，prompt 没有进入真实独立模型执行；worktree 主要是目录隔离；预算字段未形成强约束。

**增强项：**

1. Child agent 接入 `ThreadManager` 的独立 Engine；
2. 继承冻结后的 provider route 和 policy 上限，但拥有独立 history、WorkingSet 和预算；
3. 支持三种 workspace 策略：
   - read-only shared；
   - git worktree isolated；
   - same-workspace serialized；
4. 两个写 Agent 修改同一路径时，在执行或合并阶段显式冲突；
5. 强制 steps/tokens/USD/wall-time/concurrency budget；
6. 子结果使用结构化 schema：
   - summary
   - evidence
   - diff
   - verification
   - unresolved
7. `followup`、`interrupt`、`takeover` 语义分离；
8. 父 Agent 合并前必须通过 Verify Gate。

### 7.2 Scheduler 与 Worker

将 Task、Automation、Fleet、Lane 收敛成一套执行面：

- scheduler 周期 tick automation；
- worker claim lease；
- heartbeat 与超时回收；
- 幂等 retry/backoff；
- task payload version；
- agent/shell/workflow executor；
- per-project 并发与资源配额；
- shutdown drain；
- stale process reconciliation。

当前 session 启动时单次 tick 只保留为兼容路径，长期任务由持续服务负责。

### 7.3 Workflow Runtime

在现有 IR、goja VM、runtime driver 上增强：

- schema/version/migration；
- DAG dependency、parallel join、condition；
- retry、timeout、compensation；
- node checkpoint 与 crash resume；
- output handle 与数据流；
- capability/resource claim；
- 共享 session 与隔离 session 显式选择；
- JS VM CPU/内存/输出限制；
- deterministic time/random 用于测试。

### 7.4 Shell、PTY 与后台作业

1. **[已完成]** foreground shell 输出转为实时 event（`process.Options.OnOutput` → `tool.OutputObserver` → `engine.toolStream` → `tool.output`，有流式预算与截断标记，超预算只停解说不影响 `tool.result`）；
2. terminal/background/task shell 共享状态机；
3. **[已完成]** 后台输出通过 cursor + content handle 读取（`background_shell_wait` 在从归档取数时回报 `archived` 与 `pending_bytes`，超大结果溢出到 content handle）；
4. **[已完成]** 有限尾部跨重启恢复（`internal/persist/joblog` 每个 job 一个文件，环形缓冲被越过时按 offset 从文件补齐；进程走了文件还在）；
5. 受控 env schema 与 secret reference；
6. 命令 intent、cwd、网络目标进入审批摘要；
7. Windows 无强 sandbox/PTTY 时明确 unavailable/partial。

### 7.5 Git 与代码托管

按风险逐层建设：

1. 本地 branch、add 精确路径、commit、restore staged；
2. stash/merge/rebase 仅在冲突模型和恢复策略成熟后开放；
3. push 显示 remote/branch/commits/diff，禁止默认主分支直推；
4. PR create/update；
5. 结构化 review finding 与 review comment；
6. merge 必须展示 checks/reviews/branch protection；
7. 永久禁止默认 force push、skip hooks、无确认 destructive reset。

### 7.6 验收指标

- Child agent 实际任务成功率；
- 多 Agent 路径冲突和合并失败率；
- worker 崩溃后的接管成功率；
- automation due slot 去重率；
- workflow 节点恢复正确率；
- 后台任务无审计执行次数必须为 0。

---

## 8. P1 主线五：Runtime Platform

> M3 范围（§10）：§8.2 模型路由、§8.3 prompt cache、§8.4 usage/cost/tracing、§8.6 安全纵深。§8.1 第 4 条的 in-flight checkpoint 随 §5.4 提前到 M2；§8.5 Persistence 与 §8.8 Host 一致性跟随各自里程碑的需要推进；§8.7 本轮不做（见 §2.3）。

### 8.1 长会话、Compact 与 Crash Resume

1. **[已完成]** 结构化摘要：目标、事实、改动、失败、待办、关键路径（[RFC-009](./rfc/RFC-009-context-lifecycle.zh-CN.md)）；
2. **[部分完成]** 递归 compact，按信息类型分配保留优先级（渲染顺序即截断顺序，上一次摘要整块结转；**没有**做多级摘要树）；
3. **[已完成]** `/context` 展示各 partition token/byte、截断原因和 compact 阈值；
4. 持久化最小 in-flight checkpoint：
   - tool call 边界；
   - approval/input waiting；
   - provider request 可重试边界；
5. compact → restart → resume 与不中断执行语义等价；
6. event retention 前生成可恢复 checkpoint。

### 8.2 模型路由与协议

> 设计见 [RFC-010](./rfc/RFC-010-model-routing.zh-CN.md)（T1–T5 均已落地；T4 见 §15，依赖 [RFC-011](./rfc/RFC-011-egress-broker.zh-CN.md)）。

1. **[部分完成]** plan、act、verify、compact、vision 可选择不同 route：`[route.*]` 具名槽位 + 一级回落到 act 已落地，`plan` / `subquery` / `vision` 三个槽位真的接线，收据的 `routes` 说得出每个用途用了谁。缺口是 `summary`（compact 的模型叙事摘要）与 `judge`（模型 verify）**今天不调模型，因此配了会报错而不是假装支持**（RFC-010 C2），maturity 里 `model_route` 报 `partial`；
2. **[部分完成]** 根据复杂度、上下文、工具、预算自动路由，用户可以锁定。**范围已收窄**为「按能力过滤 + 显式锁定」，复杂度自动路由不做（RFC-010 §8）。锁定已落地（`[route] lock` / `codehelper exec --lock-route`）；**按能力过滤已落地**（RFC-010 T3）；**probe 收紧已落地**（RFC-010 T4）。
3. **[已完成]** OpenAI Responses 加入内置 catalog（RFC-010 T5）：`openai-responses` provider，协议 `openai_responses`，与 `openai` 共用 endpoint / `OPENAI_API_KEY`；默认 `execution.protocol` 仍是 `openai_chat`。选 Responses 用 `--provider openai-responses --model gpt-4.1`（或等价配置），不必再自带 endpoint；
4. **[已完成]** Anthropic `cache_control`（稳定前缀末块；见 §8.3）；
5. structured output / JSON Schema。**本轮明确不做**（RFC-010 §8）：现有的 JSON Schema 是工具入参，不是模型输出约束，而工具调用已经覆盖了绝大多数结构化需求；
6. **[部分完成]** 原生 image/file content block（RFC-010 T2）。`provider.ContentImage` 是一等 content block，三协议各有自己的 wire 形状；`interact.RouteVision` 走 provider 抽象与 `vision` 槽位。T3 之后，带图片的请求与 vision 槽位都会在采样前按能力位校验。还差：**file 块没做**；**`--attach-image` 仍走 `image_analyze`**（§9.5 未决：主模型有 `image_input` 时是否原生多模态，T3 只提供了判断所需的能力位，没有打开开关）；
7. **[已完成]** provider capability probe（RFC-010 T4）：`codehelper model probe` 经 egress Gate 写入 `provider_capabilities`；默认只收紧，`--trust-probe` 才放宽；
8. **[已完成]** 非幂等 stream 只有在具备 idempotency 保障时自动 retry：`ModelRequest.Idempotent` 是 pre-stream 重试的门禁并附 `Idempotency-Key`，stream 开始后不重放。RLM subquery 原先漏设它（同样安全的调用只有一次尝试），已随 RFC-010 T1 显式设上。

### 8.3 Prompt Cache 与上下文经济性

- **[已完成]** 稳定前缀：policy → repository instruction → tools → volatile turn（易变分区 `repo_map`/`working_set_ledger`/`plan` 已移到 history 之后的请求局部尾块，见 [RFC-001 §10.1](./rfc/RFC-001-repo-context.zh-CN.md)）；
- **[已完成]** provider cache 写面：OpenAI Chat Completions 与 Responses 在 `prompt_cache` 能力下发送 sticky `prompt_cache_key`；Anthropic 把 system 发成 text block 数组，仅在**稳定前缀**最后一个 block 上设 `cache_control: ephemeral`（history 之后的 turn 尾块不加，避免每 turn 击穿前缀）；
- **[已完成]** cached token 贯通 provider、protocol、usage、TUI/VS Code（Anthropic 的 `cache_read_input_tokens` 与 OpenAI 的 `cached_tokens` 都读进 `CachedTokens` 并落库，`CachedTokens ⊆ InputTokens` 有断言守；TUI、HTTP 与 VS Code Usage 均已展示）；
- **[已完成]** 统计 cache hit、compact 节省、Repo Map 成本（尾块字节数与截断次数已进 telemetry 与 `turn.receipt.context_sections`；compact 次数与节省字节数已进 telemetry；`cached_share` 在 CLI / TUI / HTTP 三面同源；写面已接，命中率取决于 provider 是否真的缓存）；
- 大工具结果默认 handle 化，按需读取；
- **[后置]** 为 plan/verify/compact 设独立预算：`summary` / `judge` 槽位已登记但**今天不调模型**（RFC-010 C2），独立预算没有消费者——要先有一条真的会调模型的 compact / verify 路径。

### 8.4 Usage、成本、延迟与追踪

> **本节已收尾。** 实施见 [RFC-008](./rfc/RFC-008-execution-receipt.zh-CN.md) 的 T1–T5a；原定的 T5b（OTLP 导出）**取消**，理由见 RFC-008 D5。偏离记在 RFC-008 §10 与 §11，摘要在 §10 的 M3 段。

1. **[已完成]** input/output/reasoning/cached token 与 cost 完整落库（schema v7 重建 `usage`：一次 provider 流 = 一个 `sample`，重试算两个；`cost_known` 由价格表的 `known` 派生，provider/model 下移到 usage 事件）；
2. **[已完成]** TUI、VS Code、HTTP 展示 usage/cost（TUI 的 `/cost` 面板、HTTP 的 `/v1/usage` rollup 与 VS Code Usage View 均读取同一投影；预算剩余挂在 `turn.receipt` 上，不新开端点）；
3. **[已完成]** 采集首 token、总延迟、tool latency、approval wait、verify latency（五项各有 span；首 token 的判据是"模型产出了东西"，不是 `message_start`）；
4. **[已完成，范围收窄]** 本地 spans，默认不上传（schema v8 的 `spans` 表与五种 span 已落地，但**没有引入 otel SDK**，见 §10 的偏离第 1 条；**OTLP 导出取消**——本地可查已经够用，跨机上报服务的是集群级场景，真需要时从 `spans` 表另起一条出网集成，RFC-008 D5）；
5. **[已完成]** `metrics`/`scorecard` 查询真实 SQLite/trace，而非文件透传（`--data-dir` 查库，两命令分工是明细 vs 一屏汇总；`--file` 保留为显式降级并正名为 counters）；
6. **[已完成]** 价格未知时显示 unknown，不能计算成 0（`usage.FormatCost` 一处实现、三面共用，测试钉住「已知的零」`$0.00` 与「未知」`unknown` 必须是不同字符串）。

### 8.5 Persistence 与检索

- history search 升级 SQLite FTS，并保留 fork chain；
- eventlog/SQLite/CAS 一致性检查和修复；
- snapshot 版本迁移、导入导出、保留与配额；
- ephemeral contentstore 与 durable CAS 明确晋升策略（M2 已铺好通路：`contentstore.Durable` 把引用计数 CAS 适配成 `contentstore.Store`，journal before-image 与 workflow 节点输出已经走它；剩下的是工具 `result_*` handle 与 `var_handle` 转录仍在内存）；
- agent/task/workflow 收敛 durable graph/lease 原语；
- schema migration 支持向前兼容与安全回滚。

### 8.6 安全纵深

> 设计见 [RFC-011](./rfc/RFC-011-egress-broker.zh-CN.md)。**T1–T3 已落地**（M3 出网主线收尾）：会话级 egress Gate、OS keyring、egress deny 审批重试。下列后置项仍属 §8.6 纵深，不挡 M3 退出。

1. 默认网络隔离；**部分**：生产会话的 provider/web HTTP 经 Enforce Gate；sandbox `AllowNetwork` 仍为 true（全量隔离后置）
2. **[已完成]** provider/web 通过专用 broker 出网，shell 按 host 审批（shell Dial 拦截仍后置；web 仍走 Guard Immediate + Gate；执行中 `egress_denied` 会再问一次并重试）
3. **[部分完成]** 执行期网络 enforcement：provider/web RoundTrip 执法与 deny→审批→重试已落地；shell managed-egress（`NetworkDeferred`）未做
4. **[已完成]** macOS Keychain、Linux Secret Service、Windows Credential Manager（`internal/security/keyring`，`auth login --kind keyring --from-env`）
5. 文件读取前 secret detection/redaction；
6. approval 展示 capability/resource/sandbox/network/scope；
7. MCP/Plugin/Skill 签名、来源、版本锁、SBOM；
8. 网页、MCP、terminal 输出带不可信来源标签；
9. Windows restricted token/job object 未落地前继续 fail-closed。

### 8.7 Web 与多模态

> 本轮不做（见 §2.3）：browser 保持 fake，`diagnostics` 的 `maturity` 继续把它报为不成熟。

- 以 Playwright/Chromium sidecar 实现真实 browser runtime；
- domain allowlist、下载隔离、credential 保护；
- 页面 prompt injection 提示；
- screenshot/image/file 进入统一 provider content；
- 网页 evidence 保留 URL、时间和引用范围；
- fetch 增加内容类型、大小、压缩炸弹和缓存策略。

### 8.8 Host 与 API 一致性

1. HTTP 补齐 agent/task/automation/fleet/lane/workflow；
2. ACP 已经过 `session/submit` 承载 approval/input/steer（[RFC-003](./rfc/RFC-003-vscode-transport.zh-CN.md) T0），仍缺 attachment 与 thread 列表；
3. WebUI 定位为运维控制面，不重复建设 IDE；
4. TUI、VS Code、HTTP、ACP 共用 protocol contract tests。**ACP/HTTP 双 Host 那一半已落地**（`internal/host/runtimeapi/contract` 一份场景表跑两遍，`make protocol-contract`；RFC-003 T1），VS Code 那一半随 M5；
5. 从 protocol 类型生成 OpenAPI/JSON Schema。**JSON Schema 已落地**（`make protocol-schema` 由 `operationPayloads` / `eventData` 工厂表生成，漂移由测试守住）；OpenAPI 仍缺；
6. 所有 Host 对同一 Problem code 的呈现和恢复建议一致。**已由双 Host 契约拉平一处**：点名不存在的 turn 两侧都同步拒绝并报 `invalid_argument`，ACP 原先回 `accepted: true` 再异步 `operation.rejected`（见 M2 实施偏离第 4 条）。

---

## 9. P2 主线六：Ecosystem & Governance

### 9.1 MCP / Plugin / Skill 生产化

> M4 范围（§10）：这是本主线本轮唯一排期的部分。地基已在（MCP 客户端四种 transport 与超时、plugin 的 hash 信任收据、skill 发现与 `load_skill`、hooks、`tool_search` 与 deferred availability），要做的是生产硬化。

1. 将 dynamic catalog 接入生产 wiring；
2. generation、revoke、stale catalog 检测；
3. 大型工具目录延迟加载，由 `tool_search` 按需 materialize；
4. MCP health、timeout、circuit breaker、permission profile；
5. Skill 版本锁、依赖、兼容范围、lint、fixture；
6. Plugin bundle 签名、组织 allowlist、升级回滚 receipt；
7. 内网 registry 支持离线镜像，不用“插件数量”作为质量指标。

### 9.2 企业治理

> 本轮不做（见 §2.3）。留在此处作为后续输入，不排进 M2–M5。

- 组织级 constitution/policy 分发与只读锁定；
- provider allowlist 和数据驻留；
- 仓库级禁止公共模型；
- 用户/项目/provider/任务类型配额；
- 审计导出：身份、模型、读写、审批、验证；
- 可复现执行包：config、route、context receipts、tools、diff、verify；
- 保留、删除和合规策略。

### 9.3 SDK 与扩展协议

> 本轮基本不做（见 §2.3），**唯一例外是 contract tests 与 provider fixture server，已上提到 M2 当常设门禁**——VS Code 客户端排到 M5，中间三个里程碑都会往协议里加面，没有双 Host contract fixture 就没有任何东西替真实客户端把关。

- 稳定 protocol/event schema 和兼容规则；
- Go SDK；
- TypeScript SDK，首先服务 VS Code Extension（本轮不做：插件按 §6.1 直连 ACP+）；
- 最小 Python client；
- Tool/Hook/Skill 脚手架；
- **[上提至 M2]** contract tests 与 provider fixture server；
- 未稳定 API 放入 experimental namespace。

---

## 10. 交付路线

### M0：正确性地基（1–2 周）

必须先完成：

- [x] 修复 revert/backtrack reconstruct 截断（重放历史带 turn 归属，镜像 `Engine.RevertWorkspace` 只删目标 turn 的语义）；
- [x] cached token 与 cost usage 贯通（`protocol.UsageData` 携带 `cached_tokens`/`cost_microunits`，落 `usage` 表并纳入 sequence 冲突检测）；
- [x] browser/subagent stub/fake 状态对外可见（`diagnostics` 新增 `maturity` 段；fleet 生产路径为真 worker，fake 仅存在于测试，故不上报）；
- [x] 建立首版 coding benchmark（`internal/host/bench` + `testdata/benchmarks`，`make bench`，hermetic 无网络/无模型）；
- [x] 定义 Execution Receipt（`turn.receipt` 事件，终态前发布，未采集分区显式列入 `not_collected`）；
- [x] 启动 VS Code transport RFC（[RFC-003](./rfc/RFC-003-vscode-transport.zh-CN.md)）。

**M0 期间发现的行为缺口（已在 M1 Verify Gate 中处理）：** 工具策略拒绝（如 read-before-edit）会直接失败整个 turn 且不产生 `tool.result`，模型没有自我纠正的机会。现已按可恢复/不可恢复分类：`ErrUnread`/`ErrStale`、参数 schema 错误、未知或不可用工具回灌模型；策略与沙箱拒绝仍终止 turn（理由见 [RFC-002 §2](./rfc/RFC-002-verify-gate.zh-CN.md)）。benchmark 任务 `unread-edit-blocked` 已同步翻成"锁定自我纠正成功"。

### M1：单 Agent 深度（4–6 周）

- [x] Repo Index（[RFC-001](./rfc/RFC-001-repo-context.zh-CN.md)：`internal/platform/repowalk` 统一遍历与 ignore、SQLite v5 词法符号索引、四个符号工具、Verify Gate 的 `affected` scope、四个 `symbol-*`/`related-tests-affected`/`index-degraded` benchmark 任务）；
- [x] Repo Map / WorkingSet（[RFC-001 §10](./rfc/RFC-001-repo-context.zh-CN.md)：PromptContext 拆成稳定前缀 + 每采样重建的易变尾块、`internal/runtime/agent/workingset` 账本（五类来源 + 衰减 + critical）、`internal/runtime/agent/repomap` 的 L0/L1、`promptcontext.AssembleTurn`、`turn.receipt.context_sections` 与 `read_paths`、`[context.repo_map]`/`[context.working_set]`、`codehelper exec --file`、三个 `repo-map-orientation`/`working-set-evolution`/`context-budget-truncation` benchmark 任务）；
- [x] EvidenceSet 与 coding policy（[RFC-001 §11](./rfc/RFC-001-repo-context.zh-CN.md)：`internal/runtime/agent/evidence` 账本（观测事实 + 派生风险 + 三类软提醒）、`tool.MetadataEvidence` 分类契约与 `workingset.SourceSearch`、稳定前缀 `coding_policy` 与尾块 `evidence` 两个分区、`turn.receipt.evidence`（`not_collected` 里的 `evidence` 随之划掉）、`[context.evidence]`/`[context.coding_policy]`、`EvidenceRisks`/`PolicyReminders` 计数器、compact 摘要追加 `UnverifiedChanges`、三个 `evidence-*`/`policy-*` benchmark 任务）；
- [x] Verify Gate（[RFC-002](./rfc/RFC-002-verify-gate.zh-CN.md)：`[execution.verify]`、`turn.verification` 事件、修复轮独立步数配额、收据填真实结论、四个 `verify-gate-*` benchmark 任务）；
- [x] Edit Transaction（[RFC-005](./rfc/RFC-005-edit-transaction.zh-CN.md)：改动改为写入观测、`file_apply` validate-then-apply 事务与 `dry_run` 预览、`internal/platform/textdiff`、收据行级统计、三个 `edit-transaction-*` benchmark 任务）；
- [x] 结构化 compact（[RFC-009](./rfc/RFC-009-context-lifecycle.zh-CN.md)：`internal/runtime/agent/compact`（六段 + digest 结转 + 跨 turn 失败账本）、`interact.PlanStep{Title,Status}` 与宽容反序列化、`evidence.Set.Changes()`、`codehelper_summary` marker 与整块结转、`[context.compact]` 与 `CODEHELPER_COMPACT_*`（此前 `MaxContextBytes` 根本没接配置）、`Compactions`/`CompactionSavedBytes` 计数器、`turn.compaction.sections` 与 `turn.receipt.context_budget`、bench 的 `followups` 多 turn 能力与三个 `compact-*` 任务）；
- [x] V0 ACP+ protocol（[RFC-003](./rfc/RFC-003-vscode-transport.zh-CN.md) T0：事件订阅从「每次 prompt 建一条」改为会话级长订阅 + `pump` 按 `threadID` 分发、`session/submit` 承载任意 Operation 并由服务端补全 thread/turn/item 引用、`session/replay` 有界游标回放、`session/load` 重启后按 `(sessionId, threadId)` 重绑、结构化 `initialize`（`protocolVersion: 2` + `methods`/`operations`/`events` 由 `protocol.OperationKinds()`/`EventKinds()` 生成）、`session/desync` 告知历史不完整、`Runtime.ReplayEvents` 只读分页、`--acp-replay-limit`、`make acp-interop` 的五个新子测试）。

**V0 ACP+ 对本文档 §6.2 的偏离（全部记入 [RFC-003](./rfc/RFC-003-vscode-transport.zh-CN.md)）：** 先改订阅模型再加方法——原先 `session/prompt` 临时建订阅且丢弃 `turnID` 不匹配的事件，`thread.compact`/`thread.fork`/`turn.revert` 这类在两个 turn 之间提交的 Operation 产生的事件没有出口，只加 `session/submit` 会得到一个黑洞。`session/load` 被**纳入 V0**：stdio 下断线等于子进程重启，内存绑定全丢，没有重绑则 `session/replay` 只在同进程内有意义。布尔 `capabilities` 被**删除**而非并存（无法表达版本，且还没有消费者）。回放**不复用** `EventsLimited`（它会顺带建订阅者与 pump 重复投递），改为新增只读的 `Runtime.ReplayEvents`。回放按会话 thread 过滤，故 `events` 可能短于扫描窗口，`nextSeq` 取窗口末条而不是返回列表末条，否则整页被过滤时客户端死循环。**已知缺口：** `session/load` 只恢复事件路由与回放，不重建线程引擎上下文，恢复后的会话还不能在原 thread 上开新 turn；回放不含 `output.delta`/`reasoning.delta`/`tool.state`/`turn.compaction`（`eventlog.ShouldPersist` 不落盘），UI 需按已落盘事件重建；两个 turn 之间提交的 Operation 必须显式给 `turn_id`（服务端只在有活跃 turn 时代填）；thread 列表方法与附件仍缺。

**结构化 compact 对本文档 §8.1 的偏离（全部记入 [RFC-009](./rfc/RFC-009-context-lifecycle.zh-CN.md)）：** 六段**全部从运行时账本机械派生，不调模型**——一个会写摘要的模型同样会写"我已经验证过了"，而收据与账本的纪律是只记观测；模型叙事摘要要等 §8.2 的独立 route 与 §8.3 的独立预算。"按信息类型分配保留优先级"实现为**渲染顺序即截断顺序**（目标 → 待办 → 失败 → 改动 → 关键路径 → 事实 → 流水摘要），段整段丢而不切一半，**没有**做多级摘要树。递归 compact 只做"认出上一次摘要并整块结转"，因为五段来自活账本、重新生成就已是全会话累积值，不需要合并算法。账本**不持久化**：`--resume` 后结构化五段从空重建，只有叙事随 `replacement_history` 的消息文本活下来。`PromptContextReceipts` 的字节明细从摘要正文移出（仍在宿主收据里），这是本次唯一的行为回退。另外发现一个结构性事实：**切割按 turn 组原子进行，所以一次 compact 至少需要两个 turn**，单 turn 内无论 history 多大都压不出来——benchmark harness 因此新增 `followups`。

**Edit Transaction 对本文档 §5.3 的偏离（全部记入 RFC-005）：** 事务边界是**一次工具调用**而不是一个 turn——turn 级暂存会让 Verify Gate 与模型回读都看到旧工作区，架构上直接否掉；`rename/move/delete` 做成 `file_apply` 的 op 而不是"一等工具"（少两个 descriptor 的 prompt 成本、一条代码路径）；有限模糊 edit、hunk 级 patch 失败诊断、LSP rename/organize imports/format、CRLF 与编码策略显式化都未做。**原已知缺口已补（M2）：** journal 账本与 before-image 落盘，跨进程恢复按 owner 存活与 commit 状态分流，详见 RFC-005 §11。

**Verify Gate 对本文档 §5.1 的偏离（全部记入 RFC-002）：** 默认值已从历史的 `off` 修正为路线图要求的 `soft + diagnostics`；改动 turn 默认形成真实 verdict，失败可修复并记录但不阻断，CI 需显式选择 `hard`。`on_failure` 只实现 `fail` 与 `revert`，`ask` 需接 `interact.Host` 故在配置加载期报错。修复轮有独立步数配额但**没有**独立 token 预算，这是已知缺口。`scope = "affected"` 原先因缺测试映射而报错，已随 Repo Index 打开（映射范围见上方 §4.1 的偏离说明）。

**退出条件：**

- 单 Agent 基准达到约定成功率；
- 修改默认带验证证据；
- compact/restart/revert 语义正确；
- ~~Extension 和 TUI 可复用同一 contract fixture~~ **顺延至 M2**：仓库里还没有 Extension，这条在 M1 无法验证。M2 起改为「ACP 与 HTTP 双 Host 复用同一 contract fixture」（RFC-003 T1），Extension 侧的复用在 M5 补齐。

### M2：真实多 Agent 与后台执行（5–7 周）

范围是 §7.1–§7.4 加 §5.4——「后台任务跨重启恢复」这条退出条件靠 §7 自己撑不住，理由见下方代价第 1 条。

- **[已完成]** 真 child runtime：`childTurnRuntime` 换成 `ThreadManager` 支撑的独立 Engine，子结果走 summary/evidence/diff/verification/unresolved 结构化 schema；`agent_merge` 经父 turn 的 file_apply/journal/`turnDiff` 合回主工作区（RFC-006 D1–D10）；
- **[已完成]** 三种 workspace 策略：read-only shared、真实 git worktree isolated、session 级 whole-turn gate 的 same-workspace serialized；两个隔离写 Agent 触同一路径时显式冲突；
- **[已完成]** steps/tokens/USD/wall-time/concurrency 预算形成强约束，不再只是声明字段；
- **[已完成]** 持续 scheduler/worker：lease claim、heartbeat、超时回收、幂等 retry/backoff、shutdown drain；claim 可跨 session 接管但通过 `sessions → workspaces.root_path` 限定在当前规范化 workspace；`agent_turn` / `workflow_run` / `shell_command` 三类 executor 均接入生产 scheduler（RFC-007）；
- **[已完成]** workflow DAG dependency、parallel join、condition、node checkpoint 与 crash resume；同波节点并发提交，指向同一可写 workspace/journal 的 production turn 由 whole-turn gate 串行，隔离执行根不受此限制；
- **[已完成]** workflow task 参数不再静默失效：`response_schema` 贯通 DAG/JS 与 production driver 并校验完整 JSON 输出；尚无定义的 `profile` 在 turn 前 fail-closed；
- **[已完成]** foreground shell 输出转实时 event（`tool.output`，live-only 不落盘），后台输出按 cursor 读取且落盘到 job log，poller 落后于环形缓冲时从归档补齐；
- **[已完成]** 持久 checkpoint（§5.4）：workspace journal 落盘并跨进程恢复（RFC-005 §11），task lease 与 workflow 节点（含节点输出 handle）跨重启可恢复；
- **[已完成]** TUI 侧 agents/tasks/jobs 面板（hotkey `5`/`6`/`7`），并收敛 `/agent` 这条误导路径——它不再 fork thread，而是打开 agents 面板并说明子 Agent 由模型工具派生；
- **[已完成]** contract tests 与 provider fixture server（从 §9.3 上提，见下方代价第 3 条）：`internal/host/runtimeapi/contract` 一份场景表跑两遍，ACP 与 HTTP 各写一个 driver（`make protocol-contract`）；协议形状另有生成式 JSON Schema 与漂移测试（`make protocol-schema`）。

**退出条件：** 后台任务跨重启恢复，多 Agent 冲突可检测，所有执行经过 Guard；每个新增协议面同时具备 ACP/HTTP 双 Host 的 contract fixture。

**退出条件核对：** 跨重启恢复三条路径都有测试——task 只在 lease 过期后被另一个进程接管（新 Host 启动不会抢活 lease）、workflow 按节点 checkpoint 续跑、workspace journal 回滚被杀进程留下的未 commit turn；worker 不会跨 workspace root 错领；多 Agent 写同一路径显式冲突；子 Agent 的工具执行绑在隔离根上并照常过 Guard；`agent_merge` 合回主工作区；三类后台 executor 均由同一 scheduler claim，`workflow_run` 复用 durable checkpoint，`shell_command` 复用 ToolGuard + 强沙箱；双 Host contract fixture 已是常设门禁。**M2 结束时仍留着的：** 已 commit 的 turn 跨重启不可 revert；节点隔离粒度仍是整个 run 一个工作区，共享可写根的节点因此不会并行执行。

**M2 的实施偏离（按发现顺序）：**

1. **`background_executor` 已从 maturity 移除。** `workflow_run` 把 v1 IR 固定在 task payload，以 task-stable run ID 复用 `workflow_runs` / `workflow_nodes` checkpoint；`shell_command` 只接受 command、workspace-relative cwd、timeout 和 description，执行仍走 `shell_run` 的 ToolGuard、policy 与强沙箱。两者只有 `idempotent=true` 才允许 task-level retry；后台没有审批 Host，遇到 Ask 立即 fail-closed。
2. **fleet 调度代码删除，JSONL 账本降级为审计。** RFC-007 D10 原本写「保留 fleet 作为第二条调度路径」，实施时改为删除：两套 claim 语义共存意味着两套超时与两套 owner 围栏，而 JSONL 没有唯一约束可以做 slot 去重。`codehelper fleet` 只剩读的动词（`list` / `status` / `inspect` / `logs` / `profile`），TUI 面板转只读，`create` / `enqueue` / `interrupt` / `resume` 指向 `codehelper worker`。
3. **`workflow` 的权限拒绝从「宿主错误」改为「节点失败」。** `AssertAllowed` 失败原本直接让整个 run 返回 error，节点停在 `running` 且 checkpoint 记录不闭合，resume 后无法判断它到底跑没跑。改为在重试循环之前判权限，拒绝即把节点 settle 成 `failed` 且不重试。
4. **ACP 对「turn 不存在」的应答改了。** 双 Host contract 的第一个真实产出：`session/submit` 一个 cancel/approval/input 到不存在的 turn，ACP 原先回 `accepted: true`，拒绝以 `operation.rejected` 事件异步到达；HTTP 则同步 404。现在 ACP 在提交前查 turn（仅限这三种「作用于已存在 turn」的 kind，steer/retry 例外，因为它们可能创建自己命名的 turn），两侧都同步拒绝且都报 `invalid_argument`。协议词表没有 `not_found`，故不新增错误码，只把 JSON-RPC 侧的 `ErrNotFound` 从 `-32603`（内部错误，暗示可重试）纠正为 `-32602`。
5. **`same_workspace_serialized` 用 Engine whole-turn gate，而不是 worker task。** 父/子 Engine 复用一个可取消 gate、父 workspace 工具根与 journal，保证整个 turn 不重叠。父 turn 内 `agent_wait` 对排队 child 立即返回 `deferred`，避免父等子、子等父 workspace 的死锁；child 写入已直接落宿主工作区，不再 merge。
6. **Workflow 节点 timeout 是底层取消，不是只停止等待。** `Driver.SpawnTask` 接收 attempt context；production driver 在 deadline 时提交对应 `turn.cancel`，等该调用退出后 retry，因此旧 attempt 不会与新 attempt 重叠执行工具或副作用。
7. **后台写 `agent_turn` 在 completed 前合回宿主工作区。** worktree child 的 diff 经现有 `agent_merge` baseline 检查、ToolGuard、资源 claim、共享 whole-turn gate 与 parent journal 应用；冲突或 apply 失败会让 task failed，并保留 `merged=false` 的结构化结果。`same_workspace_serialized` 已直接写宿主根，结果标记 `merged=true`。
8. **工具模型采样的 usage 写失败会终止 turn。** ToolSampler 仍先把已消费 token 记入 turn 内存账本，但不再吞掉 usage event emit 错误；错误穿过工具流成为不可恢复的 turn failure，禁止生成一份与 durable usage projection 不一致的成功收据。
6. **Workflow `profile` / `response_schema` 不再由 Driver 静默忽略。** 系统没有 profile registry，因此非空 profile 在创建 turn 前明确拒绝；response schema 作为 Workflow 层后置条件严格校验唯一 JSON 输出，并禁止外部 `$ref`。这不是 provider constrained decoding，RFC-010 的通用 structured output 仍保持后置。
7. **Task lease 与 workspace authority 补上多进程围栏。** production scheduler 的跨 session claim 必须匹配规范化 `workspaces.root_path`，executor 的 Guard/tool root 与 claim authority 因而一致；`NewPersistentRuntime` 只恢复没有 lease 的旧式 running 行，带 lease 的任务必须等待 expiry 后走 `Reclaim`。共享 journal 的普通 Host/Workflow thread 统一经过 whole-turn gate，避免多个 Engine 争用单 active transaction。

### M3：Runtime 经济性与可观测（4–6 周）

- **[已完成]** cost/latency/tracing 完整落库与展示（§8.4）：[RFC-008](./rfc/RFC-008-execution-receipt.zh-CN.md) 的 T1–T5a 全部落地；原定的 T5b（OTLP 导出）**取消**（D5）；VS Code 侧展示随 M5；
- **[已完成]** 多模型路由（§8.2）：[RFC-010](./rfc/RFC-010-model-routing.zh-CN.md) T1–T5；structured output、file 块与复杂度自动路由明确不做或无消费者；
- **[已完成]** prompt cache 写面（§8.3）：Chat Completions / Responses sticky `prompt_cache_key`；Anthropic 稳定前缀 `cache_control`。**后置**：`summary` / `judge` 独立预算（槽位已登记但今天不调模型，没有消费者）；
- **[已完成]** network broker、keyring、egress deny 审批重试（§8.6 T1–T3）：[RFC-011](./rfc/RFC-011-egress-broker.zh-CN.md)。**后置**：secret read / shell Dial / SBOM / 默认 sandbox 断网（RFC-011 §5）。

**已交付（RFC-008 T1–T5a）：**

- 记账口径改对：schema v7 重建 `usage`，一次 provider 流 = 一个 `sample`（重试算两个，因为重试真的花了那些 token），provider/model 从 turn 下移到 usage 事件，`cost_known` 由价格表派生而不是由调用方声明；
- 收据补两个分区：延迟（总时长、首 token、provider、tool、approval 等待、verify）与预算剩余；schema v8 新增 `spans` 表，五种 span（`turn` / `model_call` / `tool` / `approval_wait` / `verify`）由一个 per-turn 收集器同时喂收据与表，两个读者一个来源；
- 三个读面共用一个折叠层与一个金额格式化函数：`metrics` / `scorecard` 转查 SQLite，TUI 新增 `/cost` 面板报 turn/thread/session 与预算剩余，HTTP 给 `/v1/usage` 加 `rollup` 并新增 `GET /v1/threads/{thread_id}/turns/{turn_id}/trace`。**"未知不能算成 0"只有一个实现点**，否则它要在 CLI、TUI 与每个 HTTP 客户端各守一遍。

**M3 的实施偏离（按发现顺序）：**

1. **不引入 otel SDK，OTLP 导出直接取消。** §8.4 第 4 条写的是"本地 OpenTelemetry spans + OTLP opt-in"，落地形态是自有 span 类型加一张 SQLite 表，导出那一半不做（RFC-008 D5）。把 otel 的依赖树拖进核心只为写本地库不划算；而导出器要用户自架 collector、默认关闭、还得先过出网审批，服务的是集群级可观测，不是本地单用户 Agent 的读者。真需要跨机上报时，它是一条独立的出网集成，从 `spans` 表读就够。
2. **一次 trace = 一个 turn，没有独立 `trace_id`。** `turn_id` 就是关联键，`usage` 与收据都不加列。workflow run 跨多个 turn 的父 trace 留作未决（RFC-008 §9 第 3 条）：先加一个没有读者的列，结果只会是它一直为 NULL。
3. **v7 迁移清空旧 usage 行而不是转换。** 旧行的聚合口径本身是错的（同一次调用的两次 usage 事件被累加），转换等于把错的数字换个形状留下来。测试钉住"升级后 `usage` 为空且能继续正常投影新事件"，这是刻意行为不是 bug。同一原因，聚合语义改成按 `(turn_id, sample)` 最后一次覆盖，而不是累加。
4. **延迟分区不是总时长的划分。** 工具并行执行，所以 `ToolMS` 可以大于 `TotalMS`；`ApprovalWait ⊆ Tool` 成立，但把各分区加起来没有意义。读者必须知道这件事，否则会把一个健康的 turn 读成账目不平。RFC-010 T2 之后 `Provider ⊆ Total` 也不再成立：会采样模型的工具在自己的 tool span 里开 `model_call`，于是 provider 时间与 tool 时间重叠。
5. **金额格式化住在 `usage` 包，`--file` 正名为 counters。** 格式化紧贴 `PricedCalls` / `UnpricedCalls` 这对不变量，第二个消费者才不容易绕过它；`metrics` / `scorecard` 的 `--file` 从"看起来像账单"改为明说是进程计数器（events published、subscribers dropped 那一类），`scorecard` 的 `thin=true` 一并去掉。
6. **路由按 turn 取，不按采样取（RFC-010 T1）。** turn 开头把 `mode` 映射成 purpose 并把 route 冻结在 `TurnContext` 里，于是"turn 内先规划再执行"分不开（RFC-010 §9.1 未决）。同轮的其余偏离（`operate` 归到 `act`、槽位只能落在内置 catalog 或 fixture 上、vision 路由解析失败改为让会话起不来而不是被吞掉、子 Agent 继承父会话整张路由表）记在 RFC-010 §11。
7. **turn 的 token 是一个总量，钱是分模型换算后再相加（RFC-010 T2）。** 工具自己发起的采样与 turn 自身的采样分开计价——否则一张图会按 act 模型的价目结账——但 token 合成一个完整的 turn 总量，于是预算数得对。`InputTokenDelta` 仍只对自身采样算，因为它衡量的是估算准不准。其余偏离（只做 image 不做 file、`--attach-image` 仍走 `image_analyze`、usage 行不带 purpose 列）记在 RFC-010 §12。
8. **能力过滤只过滤、不挑选（RFC-010 T3）。** `Require` 拒绝缺能力的显式配置与 Auto 候选，但不会在多个能看图的模型里替用户挑一个。catalog 的 vision 标注是保守白名单；`file_input` 没做（无 ContentFile 消费者）；任意协议上显式 `PromptCacheKey` 在无 `prompt_cache` 时 fail-closed，会话 sticky key 经 `StickyPromptCacheKey` 在无能力时省略。详见 RFC-010 §13。
9. **Responses 是显式选路，不是默认切换（RFC-010 T5）。** 内置 `openai-responses` 与 `openai` 并存、共用密钥；`execution.protocol` 默认仍是 `openai_chat`。`gpt-4.1` 在 Auto 下因此歧义——必须显式给 provider。详见 RFC-010 §14。
10. **probe 只收紧，放宽要 `--trust-probe`（RFC-010 T4）。** 观测进 `provider_capabilities`（schema v9），不改 catalog；会话启动叠加观测。详见 RFC-010 §15。
11. **Anthropic system 按稳定/易变切块。** encode 把第一个非 system 角色之前的 system 当作稳定前缀（末块可带 `cache_control`），其后的 system（turn 尾块）不加断点——对应 `modelStep` 的 `promptMessages + history + turnContext` 布局。

**退出条件：** 每个 turn 的 token/cost/latency 可查，价格未知时显式 unknown 而不是算成 0；出网只经 broker，凭据只经系统 keyring；有 capability 的模型能发出缓存提示。

**退出条件核对（已完成）：** token / cost / latency 可按 turn / thread / session 查，CLI、TUI、HTTP 三面同源；provider/web 出网经 egress Gate（RFC-011 T1–T3），`kind = "keyring"` 有系统实现；Chat/Responses/Anthropic 写面已接。**明确后置（不挡 M3）：** secret read / shell Dial / 默认 sandbox 断网；`summary`/`judge` 独立预算；usage 行不带 purpose 列；`spans` 保留策略；子 Agent usage 归属；VS Code 侧展示（M5）。

### M4：MCP / Plugin / Skill 生产治理（3–5 周）

范围是 §9.1 七条，实施计划与进度台账见 [RFC-012 EcosystemRuntime](./rfc/RFC-012-ecosystem-runtime.zh-CN.md)。地基已经在：MCP 客户端四种 transport 与超时、plugin 的 content/capability hash 信任收据、skill 发现与 `load_skill` 按需加载、hooks、`tool_search` 与 deferred availability。这个里程碑做的是生产硬化，不是从零建。

**当前进度：** RFC-012 T0–T6、production deferred、Plugin lifecycle、Dynamic Host wiring 与 MCP Sync correctness 补强已完成，M4-G001–G017 全部关闭。Catalog snapshot/materialize、MCP production deferred producer、采样前权威 Sync/失败 quarantine、MCP health/breaker、Skill version/DAG/lock、Plugin signed Registry/activation/rollback 及运行中 Tool executor 原子切换、默认关闭的 trusted-host Dynamic Catalog，以及 `tool.catalog.changed` / `mcp.health.changed` / `extension.lifecycle` 双 Host 契约均已落地；协议、安全与 benchmark 门禁通过。`make verify` 的剩余红项是既有 MCP/lane 并行 flake 与本机外层 Seatbelt 导致的 Verify 18/23，已在 RFC-012 退出报告记录。下一里程碑为 M5。

- **[已完成]** dynamic catalog 接入 production wire：显式 `--trusted-dynamic-tools` 开关、服务端固定 RegistrationPolicy、ACP/HTTP register/replace/revoke、调用回程及 generation/revoke/stale fencing；双 Host contract 真实执行模型发起的动态工具调用；
- **[已完成]** MCP 异步 Sync 失败 fail-closed：失败时 quarantine MCP-owned sources，sampling 前执行 correctness Sync；connection identity 与私有 entry authority 防止旧 executor/同 revision 接管绕过 fencing；
- 大型工具目录延迟加载，`tool_search` 按需 materialize；
- MCP health、circuit breaker、per-server permission profile；
- Skill 版本锁、依赖、兼容范围、lint、fixture；
- Plugin bundle 签名、组织 allowlist、升级回滚 receipt；
- 内网 registry 与离线镜像。

**退出条件：** 工具目录可在运行中增删且模型侧不出现幽灵工具；不健康的 MCP server 被熔断而不是拖垮整个 turn；插件与 skill 的来源、版本、签名可审计。

### M5：VS Code 全量落地（6–8 周）

VS Code 后置到这里换来的是：插件第一版就能展示真多 Agent、后台任务、成本与追踪，不必先做一遍浅封装再回填。

**当前进度：** [RFC-013](./rfc/RFC-013-vscode-companion.zh-CN.md) T0–T10、[RFC-004](./rfc/RFC-004-context-bridge.zh-CN.md) T0–T5 与 [RFC-014](./rfc/RFC-014-vscode-production.zh-CN.md) T0–T6 已完成，M5 的 **V1 Companion**、**V2 Coding Native** 和 **V3 Production** 全部收口。插件现在每 root 可管理最多 32 个 Chat；显式新建 Chat 使用独立 Git worktree、Engine、sandbox、tool registry 和 journal，可真实并行，BindingStore v3 持久化选择与 cursor，重启恢复每个 Chat 最近 200 个 turn，改动通过 plan-bound merge 安全合回主 workspace。V3 最终证据包括 15/15 required E2E matrix、真实 Runtime restart/replay、Remote SSH durable reattach、Linux arm64/amd64 Dev Container、Rosetta darwin-x64、signed update/rollback/revocation、universal + 五 target VSIX、SBOM/provenance/signature/checksum、14 项安全门禁和资源预算。release provenance 绑定 source fingerprint，dirty tree 与临时 key 只能得到 `validated-dry-run, publishable=false, uploaded=false`。Windows x64/WSL2 无动态 runner，Dev Container disconnect re-attach 未宣称。M5-G021–M5-G027 全部关闭；`go test ./...` 的既有 macOS Seatbelt benchmark 限制不变。

阶段划分见 §6.11，原四阶段已压缩为三个：

- V1 Companion + 后台可见：Chat/stream/thread/approval、`@file` / `@selection`、原生 diff preview 与受控 apply、diagnostics/verify 呈现、binary 生命周期与版本握手、Workspace Trust，以及 Agents/Tasks/Jobs 视图与 detach/reattach（从原 M3 的 Tree View 移入）；
- V2 Coding Native：inline edit、code action、selection/symbol/diagnostics context bridge（RFC-004）；
- V3 Production：multi-root、Remote SSH/WSL/Dev Container、签名更新、Marketplace/Open VSX/企业 VSIX 分发（实施计划见 [RFC-014](./rfc/RFC-014-vscode-production.zh-CN.md)）。

**退出条件：** 用户能在 VS Code 内完成“理解 → 修改 → 验证 → 审批”并观察后台任务，无需切回终端；extension 与 TUI 在同一套 contract fixture 上行为一致。

### 本次顺序调整与其代价

原顺序是 M2 VS Code MVP → M3 多 Agent → M4 VS Code Native + 经济性 → M5 生态与企业化，现改为「多 Agent → 经济性 → 生态治理 → VS Code」。依赖方向核对过：原 M2 的六条（chat/stream/approval、`@file`/`@selection`、diff preview、diagnostics 呈现、binary 生命周期、Workspace Trust）全部只吃 M1 已落地的 ACP+ 与收据，不需要多 Agent 或后台执行；反过来原 M3 只有「VS Code Agents/Tasks/Jobs Tree View」一条依赖插件存在，已移入 M5 的 V1。三笔代价必须记住：

1. **§5.4 是 M2 的前置，不是可选项。** 「后台任务跨重启恢复」按当时的代码撑不住：编辑事务的 before-image 走 `contentstore.NewMemory`，`workspacejournal.Manager` 的 turn 账本全在进程内；`tasks` 表有 `lease_owner` / `lease_expires_at` 两列但没有任何 Go 代码写它们，重启时 `Tasks.RecoverInterrupted` 是把 running 一律标 failed 而不是重排队；schema v5 里没有 workflow 节点表。所以 §5.4 并入 M2，工期从原 M3 的 4–6 周上浮到 5–7 周。**（已交付：schema v6 + task lease/attempts、workflow 节点表与节点输出 handle、journal 落盘与跨进程恢复。）**
2. **TUI 成为唯一观察面。** M2/M3/M4 做出来的 agents/tasks/jobs 在 M5 之前只有 TUI 与 CLI 能看。而 TUI 的 `/agent` 是误导路径——`Model.startAgent` fork 一个 thread 跑父 runtime 的 turn，跟 `subagent.Manager` 的控制面不是一条线。TUI 面板与这条路径的收敛因此进 M2 范围，这是原 M3 没有预算的活。
3. **协议要连着三个里程碑没有真实客户端校准。** M2/M3/M4 都会往协议里加面（agent/task/job 状态、shell 增量、usage/trace 字段、工具目录 generation/revoke），而第一个真实外部消费者要到 M5 才出现。对策是把 §9.3 第 6 条「contract tests 与 provider fixture server」从生态里程碑上提到 M2，并在 §11.3 把「双 Host contract fixture」写成硬门禁——这是唯一便宜的保险，§9.3 其余内容本轮不做。

### 本轮划出范围

以下内容本轮不排期（同步记入 §2.3），理由是它们都不在「Runtime 深度 + 一个能用的编辑器客户端」这条主干上：

- §9.2 企业治理：组织级 policy 分发与只读锁定、provider allowlist 与数据驻留、配额、审计导出、可复现执行包；
- §9.3 的 SDK 部分：Go / TypeScript / Python client 与 Tool/Hook/Skill 脚手架。只保留第 6 条 contract tests（见上）。VS Code 插件按 §6.1 的薄插件原则直连 ACP+，不为它先建一层 TS SDK；
- §8.7 真实 browser 与多模态：browser 保持 fake，并继续按 §2.3 不对外宣传为完整浏览器自动化。

### 并行工作流

工作流编号沿用原有 W1–W6（跨文档引用不变），表格按调整后的交付顺序排列，所以 W3 落在最后一行。

| 工作流 | 主要范围 | 关键依赖 |
| --- | --- | --- |
| W1 Intelligence | Repo Index、WorkingSet、EvidenceSet、Compact | PromptContext receipt |
| W2 Correctness | Verify Gate、Edit Transaction、Revert | verify event/state machine |
| W4 Execution | Subagent、Scheduler、Workflow、Shell、持久 checkpoint | child runtime contract（M2 首发主线） |
| W5 Platform | Provider、Usage、Tracing、Security、Persistence | capability/usage schema |
| W6 Ecosystem | MCP、Skill、Plugin | M2 执行面稳定 |
| W3 VS Code | Extension、Context Bridge、Diff、分发 | M2–M4 协议面冻结 + contract tests |

---

## 11. 基准与成功指标

### 11.1 离线任务集

覆盖：

- 符号定位；
- 单文件修复；
- 跨文件重构；
- 生成和修复测试；
- diagnostics 驱动修复；
- patch 冲突；
- 长会话 compact/resume；
- revert/backtrack；
- Subagent 并行；
- permission/sandbox/network 阻塞；
- VS Code selection/diagnostics/diff 工作流。

### 11.2 核心指标

| 类别 | 指标 |
| --- | --- |
| 定位 | Top-K 召回、首次命中步数、无效读取字节 |
| 编辑 | 首次成功率、冲突率、事务回滚正确率 |
| 验证 | verify pass、自动修复率、未验证交付率 |
| 上下文 | compact 一致率、resume 成功率、token/成功任务 |
| 多 Agent | 子任务成功率、冲突率、预算遵守率 |
| 性能 | 首 token、总延迟、tool/verify latency |
| 成本 | cache hit、每成功任务 token/USD |
| 安全 | Guard 覆盖、未审批出网、secret 泄漏、静默 unsandbox |
| VS Code | activation、Runtime ready、diff 接受率、终端切换率 |
| 一致性 | TUI/VS Code/HTTP/ACP contract 通过率 |

### 11.3 发布门禁

每项增强必须同时具备：

1. capability/协议定义；
2. 单元或 contract tests。**VS Code 客户端排到 M5（§10），因此 M2–M4 期间新增的任何协议面必须同时落 ACP 与 HTTP 双 Host 的 contract fixture**，不接受只有单 Host 的 interop 测试——中间没有真实外部客户端替协议把关。落点是 `internal/host/runtimeapi/contract`：场景加在共享列表里，两个 driver 各自翻译；只能在一个信封里写出来的断言属于那个 Host 自己的 interop 套件，不属于契约；
3. hermetic 回归 fixture；
4. 安全影响说明；
5. 性能/token 预算；
6. 对应 benchmark；
7. 降级和错误呈现；
8. 文档与迁移说明。

---

## 12. 首批 RFC

| RFC | 内容 | 解决的问题 |
| --- | --- | --- |
| [RFC-001 RepoContext](./rfc/RFC-001-repo-context.zh-CN.md) | Index、Repo Map、WorkingSet、EvidenceSet + coding policy（四项均已落地） | 找对代码 |
| [RFC-002 VerifyGate](./rfc/RFC-002-verify-gate.zh-CN.md) | 状态机、命令探测、失败策略、事件 | 证对改动 |
| [RFC-003 VSCodeHost](./rfc/RFC-003-vscode-transport.zh-CN.md) | Extension 架构、ACP+、binary lifecycle | 编辑器体验 |
| [RFC-004 ContextBridge](./rfc/RFC-004-context-bridge.zh-CN.md) | M5 V2 selection/symbol/diagnostics context、逐项 receipt、inline edit、Code Action 与可导航 diff review；T0–T5 已完成 | 编辑器原生编码入口（M5） |
| [RFC-005 EditTransaction](./rfc/RFC-005-edit-transaction.zh-CN.md) | preview/apply/conflict/rollback（已落地；journal 落盘与跨进程恢复随 M2 追加，见 §11） | 原生安全编辑 |
| [RFC-006 ChildRuntime](./rfc/RFC-006-child-runtime.zh-CN.md) | 真子 Agent、隔离、预算、结果协议、`agent_merge`（D1–D10 已落地） | 多 Agent（M2） |
| [RFC-007 ExecutionService](./rfc/RFC-007-execution-service.zh-CN.md) | Task/Fleet/Automation/Worker 收敛 + 节点 checkpoint；`agent_turn` / `workflow_run` / `shell_command` executor 全部接入生产 scheduler，T1–T5 已完成 | 长任务（M2） |
| [RFC-008 ExecutionReceipt](./rfc/RFC-008-execution-receipt.zh-CN.md) | evidence/diff/verify 已随 RFC-002 / RFC-005 落在收据里；usage/cost/latency/trace 分区与 unknown 语义随 T1–T5a 落地（schema v7/v8、五种 span、CLI/TUI/HTTP 三个读面）；T5b（OTLP）取消，本 RFC 收尾 | 审计与度量（M3） |
| [RFC-009 ContextLifecycle](./rfc/RFC-009-context-lifecycle.zh-CN.md) | 结构化 compact：六段来源、优先级即截断顺序、marker 结转（已落地） | 长会话不失忆 |
| [RFC-010 ModelRouting](./rfc/RFC-010-model-routing.zh-CN.md) | per-purpose 具名槽位与回落链、能力过滤选路与 probe、image content block 与 vision 收敛、Responses 进 catalog（T1–T5）；§8.3 写面：Chat/Responses sticky key + Anthropic `cache_control`（§16） | 分路与协议（M3） |
| [RFC-011 EgressBroker](./rfc/RFC-011-egress-broker.zh-CN.md) | 会话级 egress Gate（provider/web Dial 执法）+ OS keyring + deny 审批重试（T1–T3 已落地；secret read / shell Dial / SBOM 等后置） | 出网与凭据（M3） |
| [RFC-012 EcosystemRuntime](./rfc/RFC-012-ecosystem-runtime.zh-CN.md) | 统一动态工具目录、MCP health/breaker、Skill 版本依赖锁、Plugin 签名升级回滚、在线 Registry 与离线镜像；T0–T6 与 Gap Ledger 持续更新 | 扩展生态生产治理（M4） |
| [RFC-013 VSCodeCompanion](./rfc/RFC-013-vscode-companion.zh-CN.md) | M5 V1 薄插件、ACP 查询与恢复、Chat/审批、受控编辑、后台观察面；T0 工程与协议生成门禁已完成 | 编辑器原生执行闭环（M5） |
| [RFC-014 VSCodeProduction](./rfc/RFC-014-vscode-production.zh-CN.md) | M5 V3 multi-root、remote Extension Host、签名更新回滚、target VSIX、15/15 E2E matrix 与 RC provenance；T0–T6 已完成 | 编辑器生产分发（M5） |

按 §10 调整后的顺序：**RFC-006 ChildRuntime 与 RFC-007 ExecutionService 立即并行启动**（单 Agent 主路径已随 M1 稳定，这是 M2 的两份设计前提）。持久 checkpoint 不新开 RFC——journal 落盘扩进 RFC-005（那正是它自己记的已知缺口），task lease 与 workflow 节点 checkpoint 扩进 RFC-007。RFC-008 的 usage/trace 分区随 M3 收尾。RFC-004 ContextBridge，以及 RFC-005 里"审批前先看 diff"所需的工具接口 plan/apply 拆分，都推迟到 M5 与插件一起推进——没有插件消费者时先拆接口只会得到一个无人调用的第二形态。

---

## 13. 最终判断

CodeHelper 接下来最重要的变化，不是“再拥有更多功能”，而是完成三次升级：

1. 从工具集合升级为有仓库认知和验证闭环的 Coding Agent；
2. 从 TUI 产品升级为 Runtime + TUI + VS Code 的多客户端产品；
3. 从编排骨架升级为可恢复、可度量、可治理的执行平台。

VS Code 插件会显著提升体验，但它不能替代 Runtime 深度。因此顺序是：**先稳定协议、上下文与编辑事务（M1，已完成）→ 把执行面做实（M2）→ 把成本、延迟与出网治住（M3）→ 把工具生态硬化（M4）→ 最后一次性交付编辑器原生体验（M5）**。插件后置换来的是它第一版就能展示真多 Agent、真后台任务与真成本，而不是把现有浅能力包装得更漂亮、再逐个回填。

代价也要认：M2–M4 期间 TUI 是唯一观察面，协议连着三个里程碑没有真实外部客户端校准。这两笔分别由 M2 的 TUI 面板与 §11.3 的双 Host contract fixture 门禁兜住，详见 §10。

---

## 参考

- [ARCHITECTURE.zh-CN.md](./ARCHITECTURE.zh-CN.md) — 分层、Runtime、安全与持久化
- [USAGE.zh-CN.md](./USAGE.zh-CN.md) — 当前配置与命令
- [PROJECT_ANALYSIS.zh-CN.md](./PROJECT_ANALYSIS.zh-CN.md) — 项目总览
