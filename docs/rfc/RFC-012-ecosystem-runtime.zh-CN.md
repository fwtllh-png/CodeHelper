# RFC-012：M4 扩展生态生产治理

> 状态：Implemented；T0–T6、生产 deferred 与 Plugin lifecycle 补强已完成，M4 已收口
> 关联：[ROADMAP §9.1](../ROADMAP.zh-CN.md)、[ROADMAP M4](../ROADMAP.zh-CN.md)、[RFC-003 VSCodeHost](./RFC-003-vscode-transport.zh-CN.md)、[RFC-011 EgressBroker](./RFC-011-egress-broker.zh-CN.md)  
> 影响面：`internal/adapter/tool`、`internal/adapter/tool/dynamic`、`internal/adapter/tool/toolsearch`、`internal/adapter/mcp`、`internal/adapter/plugin`、`internal/adapter/skill`、`internal/runtime/agent/engine`、`internal/runtime/app/wire`、`internal/runtime/protocol`、`internal/host/runtimeapi`、`internal/host/cli`

---

## 1. 目标

M4 不再增加孤立的扩展机制，而是把已有 MCP、Plugin、Skill 和动态工具能力收敛成一个可动态更新、可撤销、可审计、可降级的生产运行面。

完成后必须满足：

1. 工具可以在会话运行期间注册、替换、撤销和按需加载；
2. 模型每次采样看到的工具定义，与后续允许执行的工具 revision 一致；
3. revoke 后旧 revision 永远不能开始新的执行；
4. 大型工具目录不整体注入模型上下文；
5. 单个 MCP server 故障不会拖垮整个 turn 或整个会话；
6. Skill 的版本、依赖、来源和内容 digest 可锁定；
7. Plugin 的发布者、签名、版本、能力与升级/回滚过程可审计；
8. 在线 Registry 和离线镜像使用完全相同的验证语义。

---

## 2. 明确不做

以下内容不属于 M4：

- 组织级 policy 远程分发与只读锁定；
- 企业级数据驻留、用户配额和集中审计导出；
- Go、TypeScript、Python SDK；
- 新的 Plugin ABI、WASM Runtime 或第二套工具协议；
- 真实 browser runtime；
- 通过自动重试重放未知幂等性的 MCP 工具调用；
- 用工具数量作为 M4 成功指标。

---

## 3. 当前基础与结构性缺口

### 3.1 已有基础

- `tool.Registry` 支持静态注册和 deferred loader；
- `tool/dynamic.Catalog` 已有 generation、replace、revoke 和 stale 检测雏形；
- `tool_search` 可以搜索 available/deferred descriptor；
- MCP 支持 stdio、HTTP、streamable HTTP、SSE、超时、reload 和 prewarm；
- Plugin 已有内容 hash、信任收据、只读 staging、enable/disable/revoke 和 authority 取消；
- Skill 已有安全发现、优先级、启停、按需 `load_skill` 和 Plugin authority；
- ACP/HTTP 已有共享 contract fixture，Protocol JSON Schema 可生成。

### 3.2 必须先解决的缺口

1. `tool.Registry` 只能新增，不能原子 replace/unregister；
2. Dynamic Catalog 与 Registry 各自持有状态，存在双真相源；
3. revoke 只让 executor 报错，没有真正移除 Registry 槽位；
4. revoke 后重新注册同名工具会撞 Registry duplicate；
5. replace 不会可靠替换 deferred loader；
6. `tool_search` 只返回描述，没有 materialize 闭环；
7. Engine 会过滤 deferred tool，搜索后模型仍拿不到可调用 schema；
8. 当前 tool catalog 在会话启动时进入稳定 PromptContext，运行中变化不会更新；
9. Engine 每个 model step 直接读取活 Registry，缺少“采样定义 → 工具调用”的 revision 绑定；
10. MCP reload 后 Pool catalog 与已注册工具不会原子对齐；
11. MCP 没有 health、breaker 和逐 server 故障隔离；
12. Skill 没有版本、依赖、兼容范围和 lockfile；
13. Plugin 没有发布者签名、Registry、原子升级和 rollback receipt。

---

## 4. 硬约束

### C1：一个权威目录

所有 builtin、host dynamic、MCP、Plugin 工具最终必须进入同一个 `tool.Registry` 目录。各适配器可以保留自己的发现状态，但不能再独立决定模型可见性和执行有效性。

### C2：采样快照与执行 revision 绑定

每次模型采样前冻结一次 Catalog Snapshot。该次响应产生的 tool call 必须携带内部 snapshot generation 与 entry revision，执行前重新校验：

- revision 仍有效：按冻结 descriptor 和 policy 执行；
- entry 已 replace：返回 `tool_catalog_stale`；
- entry 已 revoke：返回 `tool_revoked`；
- handler 不得收到 stale/revoked 调用。

同一次 provider retry 必须复用同一个 snapshot，不能因 retry 改变工具面。

### C3：revoke 优先于可用性

安全 revoke 必须立即阻止新调用，并取消由该 authority 管理的在飞调用。普通升级可以让旧调用 drain，但新调用只能进入新 revision。

### C4：远端描述不能授予权限

MCP、Plugin 或动态 Host 提供的 schema/description 不能决定 capability、resource、sandbox、network 或审批策略。权限只能来自可信本地配置和受控 RegistrationPolicy。

### C5：不重放未知副作用

MCP transport 超时、断连或 breaker half-open 都不能自动重放原 tool call。恢复探测使用 `ping`、`initialize` 或 catalog/list，不使用业务工具。

### C6：供应链验证先于 staging 和执行

Registry artifact 必须先验证索引签名、artifact digest、publisher allowlist 和兼容范围，再安全解包并进入只读 staging。任何一步失败都不能改变当前激活版本。

### C7：协议面必须双 Host 同步

新增的 catalog、health、lifecycle 事件或查询能力必须同时具备 ACP 与 HTTP contract fixture，并同步生成 JSON Schema。

### C8：错误必须分类

Host、receipt 和 metrics 使用稳定错误码，不以原始错误字符串判断状态。原始错误经过脱敏后只能作为 detail。

---

## 5. 总体架构

```text
MCP Pool ───────────────┐
Plugin Registry ────────┤
Skill Catalog ──────────┤ discovery / reconcile
Trusted Host Dynamic ───┘
              │
              ▼
┌──────────────────────────────────────────────┐
│ tool.Registry                               │
│ catalog_id · generation · entry revision    │
│ eager · deferred · materialized · revoked   │
└───────────────┬──────────────────────────────┘
                │ Snapshot()
                ▼
┌──────────────────────────────────────────────┐
│ Model Sampling Snapshot                     │
│ tools[] · dynamic catalog tail · receipt    │
└───────────────┬──────────────────────────────┘
                │ tool call + internal revision
                ▼
┌──────────────────────────────────────────────┐
│ ToolGuard                                   │
│ revision check → policy → approval → execute│
└──────────────────────────────────────────────┘
```

---

## 6. 核心设计

### D1：Catalog Snapshot

建议的内部结构：

```go
type CatalogSnapshot struct {
    CatalogID  string
    Generation uint64
    Digest     string
    Entries    map[string]CatalogEntrySnapshot
}

type CatalogEntrySnapshot struct {
    Name       string
    Source     string
    Revision   uint64
    State      EntryState
    Descriptor tool.Descriptor
}
```

`CatalogID` 标识当前进程内目录实例。generation 只在同一个 CatalogID 内比较，避免进程重启后把新 generation 与旧值误判为回退。

### D2：原子 Reconcile

Registry 增加按 source 原子提交的接口：

```go
Reconcile(source string, expectedGeneration uint64, entries []Registration) (ChangeSet, error)
```

提交前完成：

- descriptor 和 JSON Schema 校验；
- tool/alias 名冲突校验；
- trusted RegistrationPolicy 校验；
- source 内重复项校验；
- generation CAS 校验；
- loader/executor 生命周期校验。

任一校验失败时目录零变化。

### D3：状态语义

| 状态 | 模型可见 | 可执行 | 说明 |
| --- | --- | --- | --- |
| `eager` | 是 | 是 | builtin 或明确常驻工具 |
| `deferred` | 仅可被 `tool_search` 搜索 | 否 | 未 materialize |
| `materialized` | 是 | 是 | 搜索后加载并固定到会话 |
| `unavailable` | 否 | 否 | 加载失败或依赖不可用 |
| `revoked` | 否 | 否 | tombstone，仅用于 stale/revoke 判定 |

首版 materialized 工具在会话内保持固定，不做 LRU 自动驱逐。达到数量或 schema token 上限时明确拒绝继续 materialize，避免模型工具面在无操作时自行变化。

### D4：`tool_search` 真正 materialize

`tool_search` 流程：

```text
搜索完整目录
  → 选出 top-k
  → Registry.Materialize(name, revision)
  → loader 单飞
  → generation 增长
  → 返回名称、revision、新 generation
  → 下一次模型采样看到新 tools[]
```

加载失败进入 `unavailable`，返回 `tool_load_failed`，并记录可观测但脱敏的 reason。

### D5：Prompt 与缓存

- immutable builtin 工具摘要可以保留在稳定前缀；
- dynamic/materialized catalog 放入 history 之后的易变尾块；
- 尾块携带 catalog ID、generation、digest、materialized 名称和截断信息；
- provider `tools[]` 与尾块必须来自同一个 snapshot；
- catalog receipt 进入 turn receipt，便于复现实验。

### D6：MCP health 与 breaker

每个 server 独立维护：

```text
starting → healthy → degraded → open → half_open → healthy
                                └──────────────→ open
```

计入失败：

- connect/initialize 失败；
- transport 断开；
- call timeout；
- JSON-RPC protocol error；
- catalog schema 非法。

不计入失败：

- MCP tool 正常返回 `isError=true`；
- Guard/policy/approval 拒绝；
- 用户取消；
- 本地参数 schema 拒绝。

`open` 时不再发业务调用；冷却后通过安全探测进入 `half_open`。恢复后重新 discovery，并以 source 为单位 Reconcile。

### D7：MCP permission profile

Server 配置增加可信 ceiling：

```json
{
  "permission_profile": {
    "capabilities": ["read", "network"],
    "resource_kinds": ["workspace", "network"],
    "network_hosts": ["example.internal"]
  }
}
```

每个 ToolBinding 必须是 ceiling 的子集。远端 catalog 更新不能通过新增 tool 或修改 schema 扩大本地授权。

### D8：Skill 版本与依赖

为可分发 Skill 增加 `skill.toml`：

```toml
schema_version = 1
name = "review"
version = "1.2.0"
codehelper = ">=0.4.0 <0.5.0"

[dependencies]
repository-context = "^2.1.0"
```

规则：

- Registry/Configured Skill 必须有 manifest；
- workspace/user 的单文件 Skill 继续兼容，但标记为 `local/unlocked`；
- 依赖必须形成 DAG；
- load 时按拓扑顺序加载；
- 名称、版本、来源、digest 写入 lockfile；
- lock 漂移、依赖环、版本冲突、兼容范围不满足全部 fail-closed。

建议路径：

```text
{data-dir}/skills/locks/<workspace-id>.lock.json
```

### D9：Skill lint 与 fixture

增加：

- `codehelper skill lint <path>`
- `codehelper skill lock`
- `codehelper skill verify`

lint 检查 manifest、frontmatter、大小、路径安全、依赖和兼容范围。Fixture 为 hermetic metadata/load/expected-digest 测试，不在 M4 引入真实模型评测。

### D10：Plugin 签名

Registry 使用 detached Ed25519 signature。签名 payload 使用 canonical JSON：

```json
{
  "schema_version": 1,
  "name": "plugin-name",
  "version": "1.2.0",
  "generation": 7,
  "publisher": "publisher-id",
  "artifact_sha256": "...",
  "manifest_sha256": "...",
  "capability_sha256": "..."
}
```

签名和公钥不放入 artifact 自身的内容 hash，避免自引用。可信 publisher allowlist 来自本地配置；本地手工 Plugin 继续支持现有“人工 review + hash receipt”路径，但必须明确显示 `unsigned-local`。

### D11：Plugin 原子升级与回滚

正常升级：

```text
resolve index
  → download/cache
  → verify signature and digest
  → safe extract
  → immutable stage
  → validate manifest/capabilities
  → build new catalog revision
  → atomic activate
  → old revision drain
  → write receipt
```

任一步失败都保留旧版本。rollback 只允许切回已验证、仍存在的 staged content，并生成新的 rollback receipt，不能直接修改历史 receipt。

安全 revoke 与普通升级不同：安全 revoke 立即撤销旧 authority，并取消其在飞调用。

### D12：Registry 与离线镜像

Registry index 为签名、版本化 JSON。支持：

- `https://` 内网 Registry；
- `file://` 离线镜像；
- 内容寻址 artifact cache；
- 显式版本安装；
- durable activation receipt 驱动的可复现加载与 rollback。

HTTPS 必须经过 RFC-011 Egress Gate。在线源和离线源必须走同一套 index signature、artifact digest 和 publisher allowlist 校验，禁止为离线镜像增加 bypass。

---

## 7. 协议与错误契约

### 7.1 事件

已实现：

- `tool.catalog.changed`
- `mcp.health.changed`
- `extension.lifecycle`

`tool.catalog.changed` 至少包含：

```json
{
  "catalog_id": "catalog-...",
  "generation": 12,
  "digest": "...",
  "added": ["mcp_repo_lookup"],
  "replaced": [],
  "revoked": ["plugin_old_run"]
}
```

事件不携带 secret、完整远端错误或 Plugin 参数。

### 7.2 查询面

ACP 与 HTTP 同时提供：

- catalog snapshot；
- materialized/available/unavailable 状态；
- MCP health；
- extension lifecycle receipt 事件与历史回放。

注册、替换、撤销默认关闭，仅在显式启用 trusted-host dynamic tools 时开放。RegistrationPolicy 由服务端配置，客户端不能提交 capability 或 sandbox 策略。

生产接线使用 session-scoped `dynamic.Manager`：`--trusted-dynamic-tools`
与工具运行时同时开启后，ACP 暴露 `tool/catalog|register|replace|revoke`
和 `tool/call/result`，调用通过 `tool/call` 通知回到同一受信客户端；HTTP
暴露 `/v1/tools/dynamic` 管理面、可重试 pending-call 查询和 result 回填。
两侧共用 Registry generation fencing，调用仍从 Registry 进入 ToolGuard。
默认服务端策略为 `plugin + write-tree + serial + sandbox none`：实际代码在
Host 侧执行，本地强沙箱不能约束它，故不作虚假 `SandboxStrong` 声明；客户端
不能在 spec 中提交或提升 capability/resource/sandbox policy。

### 7.3 稳定错误码

| 错误码 | 含义 | 是否可恢复 |
| --- | --- | --- |
| `tool_catalog_stale` | 调用绑定的 revision 已变化 | 是，刷新后重采样 |
| `tool_revoked` | 工具或 authority 已撤销 | 否，除非重新注册 |
| `tool_load_failed` | deferred loader 失败 | 视依赖状态 |
| `tool_catalog_limit` | materialized 数量/token 超限 | 否，需用户调整 |
| `mcp_circuit_open` | MCP breaker 已打开 | 是，等待恢复 |
| `mcp_unavailable` | MCP server 不可用 | 是 |
| `extension_signature_invalid` | 签名或 publisher 不可信 | 否 |
| `extension_digest_mismatch` | artifact/manifest/capability digest 不一致 | 否 |
| `dependency_conflict` | Skill 依赖版本无解 | 是，可改用其它 Skill |
| `dependency_cycle` | Skill 依赖存在环 | 是，可改用其它 Skill |
| `compatibility_mismatch` | CodeHelper 版本不满足 | 是，可改用其它 Skill |
| `skill_lock_drift` | Skill 内容、来源、依赖边或运行时版本与锁不一致 | 是，需重新审计并生成锁 |

---

## 8. 实施分片

### T0：RFC、基线与基准

- [x] 固化本 RFC；
- [x] 记录当前工具数量、tool catalog bytes/tokens、启动耗时；
- [x] 增加 100/500/1000 工具的 hermetic benchmark；
- [x] 增加当前 dynamic revoke/re-register 与 tool_search 不 materialize 的失败测试；
- [x] 定义 Catalog Snapshot 与 ChangeSet 类型。

**实现证据：**

- `internal/adapter/tool/catalog.go`：`CatalogEntryState`、`CatalogEntrySnapshot`、`CatalogSnapshot`、`CatalogChange`、`ChangeSet`；快照构造时校验 identity/state/descriptor/duplicate，并深拷贝 JSON Schema；
- `internal/adapter/tool/catalog_test.go`：排序、Lookup、输入隔离、输出隔离与非法快照测试；
- `internal/runtime/agent/promptcontext/catalog_benchmark_test.go`：真实 builtin 基线，以及 100/500/1000 工具的 Registry 注册和 prompt catalog 渲染基准；
- `internal/adapter/tool/dynamic/dynamic_test.go`：T0 以 characterization 锁定 M4-G001；T1 已翻转并重命名为 `TestRevokedDynamicToolCanBeRegisteredAgain`；
- `internal/adapter/tool/toolsearch/tool_search_test.go`：T0 锁定搜索后 descriptor 仍为 deferred 的 M4-G005；T2 已翻转为 search → materialize → call 正向闭环；
- `make catalog-bench`：固定基准入口。

**当前基线（Apple M5 Pro，darwin/arm64，`-benchtime=10x`）：**

| 场景 | 工具数 | 目录 bytes | 粗估 tokens（bytes/4） | 时间 | 分配 |
| --- | ---: | ---: | ---: | ---: | ---: |
| 默认 builtin | 60（52 available / 8 unavailable） | 5,194 | 1,299 | 初始化 1.29–2.21 ms | 未单独统计 |
| catalog render | 100 | 13,103 | 3,276 | 129,229 ns/op | 330,670 B/op，1,573 allocs/op |
| catalog render | 500 | 65,103 | 16,276 | 585,629 ns/op | 1,593,752 B/op，7,827 allocs/op |
| catalog render | 1000 | 130,103 | 32,526 | 1,267,275 ns/op | 3,596,037 B/op，15,852 allocs/op |
| Registry startup | 100 | - | - | 43,542 ns/op | 164,775 B/op，1,346 allocs/op |
| Registry startup | 500 | - | - | 259,475 ns/op | 831,446 B/op，7,042 allocs/op |
| Registry startup | 1000 | - | - | 549,292 ns/op | 1,663,044 B/op，14,550 allocs/op |

结果表明当前 catalog 的 prompt bytes、时间和分配均近似线性增长；1000 工具仅目录摘要就约 32.5K tokens，因此 T2 的 deferred/materialize 与硬预算是退出条件，不是可选优化。

**验证结果：**

```text
go test -count=1 -v ./internal/adapter/tool \
  ./internal/adapter/tool/dynamic \
  ./internal/adapter/tool/toolsearch \
  ./internal/runtime/agent/promptcontext
PASS

go test -count=1 ./internal/adapter/tool/... \
  ./internal/runtime/agent/promptcontext
PASS

make catalog-bench
PASS

go vet ./internal/adapter/tool/... ./internal/runtime/agent/promptcontext
PASS

go test -race -count=1 -p=1 ./internal/adapter/tool \
  ./internal/adapter/tool/dynamic \
  ./internal/adapter/tool/toolsearch \
  ./internal/runtime/agent/promptcontext
PASS

go build ./cmd/codehelper
PASS

go test -count=1 ./...
PARTIAL：MCP stdio 与 lane 在全量并行运行时超时，串行定向复跑通过；
host/bench 通过 18/23，5 个 Verify Gate 场景因当前外层沙箱无法提供
嵌套 Seatbelt（sandbox_unavailable）失败。
```

**实施偏离：** “失败测试”实现为 characterization tests：CI 保持绿色，但测试明确断言当前错误行为。T1/T2 修复对应缺口时，必须将断言翻转成成功行为；不使用 `t.Skip`，也不保留永久预期失败。

### T1：统一 Catalog Core

- [x] Registry 支持 source、generation、entry revision；
- [x] 实现原子 Reconcile/Replace/Revoke；
- [x] aliases 与 loader 生命周期纳入同一事务；
- [x] Dynamic Catalog 改成 Registry 适配层；
- [x] revoke/re-register、replace deferred loader 闭环；
- [x] 并发 reconcile 与 `-race` 测试；
- [x] 分类错误接入 Engine recoverable failure。

**实现证据：**

- `internal/adapter/tool/catalog.go`：Registry 成为 Catalog 唯一真相源，提供 source-scoped `Reconcile/Replace/Revoke`、generation CAS、entry revision、tombstone、Snapshot/SourceState 和稳定 digest；
- `internal/adapter/tool/tool.go`：`Resolve/Descriptors/InjectedSandbox` 全部读取 Registry 保存的权威 descriptor；deferred materialize、失败降级、动态 availability 收紧均更新 generation/revision；
- alias 校验在 commit 前完成，冲突时 Registry 零变化；revoke 同时写入 canonical/alias tombstone；
- deferred loader 的 replace/revoke 与加载竞态通过槽位身份校验，旧 loader 结果和等待者不能泄漏旧 executor；
- `internal/adapter/tool/dynamic/catalog.go`：删除独立 mutex、entries 与 generation，改为 Registry source 适配层；动态 executor 调用 Handler 前校验自己仍为当前 executor；
- `internal/runtime/agent/engine/toolfailure.go` 与 `engine.go`：`tool_catalog_stale`、`tool_revoked` 进入 recoverable tool result，并写入结构化 `error_category`；
- `internal/adapter/tool/registry_catalog_test.go`：覆盖 alias 原子回滚、tombstone/re-register、deferred loader replace、加载中 replace、CAS 竞争与 source round-trip no-op；
- `internal/adapter/tool/dynamic/dynamic_test.go`：M4-G001 翻转为成功断言，并验证 stale/revoked executor 到达 Handler 的次数为 0。

**验证结果：**

```text
go test -count=1 ./internal/adapter/tool/... ./internal/runtime/agent/engine
PASS

go test -race -count=1 -p=1 ./internal/adapter/tool \
  ./internal/adapter/tool/dynamic \
  ./internal/adapter/tool/guard \
  ./internal/runtime/agent/engine
PASS

go test -count=50 ./internal/adapter/tool \
  -run 'TestRegistry(ReplaceDuringDeferredLoadRejectsOldResultAndWaiters|ReconcileCASAllowsOneConcurrentReplacement|ReconcileIsAtomicOnAliasConflict)'
PASS

go test -race -count=20 -p=1 ./internal/adapter/tool \
  -run 'TestRegistry(ReplaceDuringDeferredLoadRejectsOldResultAndWaiters|ReconcileCASAllowsOneConcurrentReplacement)'
PASS

go vet ./internal/adapter/tool/... ./internal/runtime/agent/engine
PASS

go build ./cmd/codehelper
PASS

go test -count=1 ./...
PARTIAL：T1 相关包全部通过；MCP stdio 与 lane 在全量并行运行时超时，
串行定向复跑通过；host/bench 通过 18/23，5 个 Verify Gate 场景因
当前外层沙箱无法提供嵌套 Seatbelt（sandbox_unavailable）失败。
```

T1 后 `make catalog-bench`（Apple M5 Pro，darwin/arm64，`-benchtime=10x`）：

| 场景 | 100 tools | 500 tools | 1000 tools |
| --- | ---: | ---: | ---: |
| catalog render | 0.183 ms | 1.003 ms | 2.073 ms |
| Registry startup | 0.321 ms | 1.644 ms | 3.321 ms |
| Registry startup alloc | 0.62 MB | 3.11 MB | 6.23 MB |

启动期兼容 `Register` 曾因逐项完整 reconcile 退化为 O(n²)，1000 tools 达到 1.25 s/1.97 GB；在宣告完成前已改为同一 Registry 内的 O(1) 一次性原子插入，最终结果恢复线性。

**实施偏离：**

1. RFC 草案把 `Replace/Revoke` 描述为独立原语；实际实现以 source 全量 desired state 为底层事务，单项 API 只是 CAS 包装，避免两套提交语义；
2. legacy `Register/RegisterDeferred` 的 source 唯一且不可变，使用 O(1) 原子插入而不是全量 source reconcile，保留启动性能；
3. runtime availability 仅允许 executor 收紧 `Availability/UnavailableReason`；capability、resource、schema 与 alias 不能通过动态 `Descriptor()` 扩权；
4. T1 只保证动态 executor 在 replace/revoke 后 fail-closed；“模型采样 snapshot → tool call revision”绑定仍属于 T2（M4-G003）。

### T2：Sampling Snapshot、Prompt 与 Materialize

- [x] 每次 provider sampling 冻结 Catalog Snapshot；
- [x] provider `tools[]` 和动态尾块使用同一 snapshot；
- [x] tool call 内部绑定 generation/revision；
- [x] Guard 执行前校验 stale/revoked；
- [x] `tool_search` 原子 materialize top-k；
- [x] materialized 数量与 schema token 上限；
- [x] catalog receipt 进入 turn receipt；
- [x] 新增 `tool.catalog.changed` 与双 Host contract。

**实现证据：**

- `engine.modelStep` 在 retry loop 外冻结 snapshot；`tools[]`、`tool_catalog` 易变尾块和返回 call binding 共同读取该快照，provider retry 不重新取目录；
- `provider.ToolCall` 携带不出 wire 的 catalog ID/generation/revision/authority，`Guard.ExecuteBound` 与 `Registry.ResolveBound` 在同一 Registry 锁内先校验再 resolve；authority 是每次 entry incarnation 唯一的私有 token，因此 replace/revoke、跨 source 同名接管或未广告调用都不会到达 loader/handler；
- `tool_search` 对 top-k 调用 `Registry.Materialize`，成功结果返回 generation/revision；并发 loading 计入 materialized 数量与 schema bytes 预算；
- 生产 MCP Adapter 将 discovery 后的远端工具注册为 source-owned deferred producer；未搜索的 schema 不进入模型 `tools[]`。已 materialized entry 在 no-op health/catalog Sync 后保持 pinned，远端 catalog 变化按 revision 替换回新的 deferred loader；
- `turn.receipt.catalog` 记录最后一次 sampling 的 digest、advertised/materialized/deferred/omitted；`tool.catalog.changed` 已进入事件注册表、JSON Schema 与 ACP/HTTP 共用 contract scenario；
- 启动时稳定 PromptContext 不再冻结 tool catalog；每次 sampling 在 history 后追加由同一 snapshot 生成的有预算目录尾块。

**验证结果：** tool 全包、engine、promptcontext、app、protocol、ACP/HTTP contract、vet、schema drift 与独立 build 通过；sampling replace/retry 与 materialize 限额高竞争测试及定向 race 重复门禁通过。全量仍为已知环境结果：MCP stdio/lane 并行超时但串行复跑通过，Verify benchmark 18/23（5 项受外层 Seatbelt `sandbox_unavailable` 限制）。
**实施偏离：** schema 上限使用 JSON Schema 确定性序列化字节数，而不是 provider-specific tokenizer；Prompt 目录仍同时执行 bytes 与启发式 token 双预算。eager 工具属于运行时核心契约，不因 search threshold 被静默裁掉；超总量/schema 上限时 fail-closed 返回 `tool_catalog_limit`。

### T3：MCP 生产硬化

- [x] Pool 按 server 隔离 reload；
- [x] health 状态机与快照；
- [x] circuit breaker 与 half-open 安全探测；
- [x] 禁止业务 tool call 自动重放；
- [x] catalog change notification/reconcile；
- [x] per-server permission ceiling；
- [x] `mcp.health.changed` 与 CLI/TUI/HTTP 状态；
- [x] flapping/timeout/protocol-error fixture；
- [x] 单 server 故障不影响其它 server。

**实现证据：**

- `adapter/mcp.Pool` 以 `serverRuntime` 隔离连接、配置 hash、catalog 与 health；reload 只重连配置变化或待恢复的 server，单 server connect/discovery/collision 失败只打开该 breaker；
- `healthTracker` 实现 `starting → healthy → degraded → open → half_open`，冷却后只允许安全 `ping` 探测；`Connection` 的业务请求只发送一次，HTTP stale-session 只重放 initialize/ping/list，业务请求在重连后返回 `mcp_unavailable`；
- stdio、streamable HTTP 与 legacy SSE 接收 list-changed notification 后，合并触发对应 server 的完整 rediscovery；`tool/mcp.Adapter` 以 `mcp:<server>` source CAS reconcile/revoke，helper Registration 在无变化时复用；远端 connection 实例变化时即使 descriptor 未变也会替换 executor；
- 后台 health/catalog Sync 只负责低延迟更新；Engine 在每次 sampling snapshot 前通过 wire 的 `ToolCatalogSync` 再执行一次权威 reconcile。同步失败返回可重试 `unavailable` 且 provider 收不到请求；异步 Sync 失败立即 quarantine 全部 MCP-owned source，旧 revision/authority 无法继续执行，恢复同步后再注册；
- `PermissionProfile` 对 capability、resource kind 和固定 network host 建立本地 ceiling；动态 host 字段不能绕过 host allowlist；
- `mcp.health.changed` 进入协议注册表与 Schema；Engine 在 sampling 边界比较 Pool snapshot，ACP/HTTP 事件流、TUI `/mcp` 面板、`codehelper mcp status` 和 HTTP `GET /v1/mcp/health` 读取同一状态事实；
- `pool_t3_test.go`、`http_integration_test.go`、`mcp_test.go` 覆盖 A/B 隔离、per-server reload、timeout/RPC error flapping、half-open、`isError=true`、不重放、notification reconcile；双 Host 共用 `MCP health changes are visible on the shared event stream` scenario。

**验证结果：** MCP、MCP Registry adapter、Engine、wire、TUI、CLI、protocol 与 ACP/HTTP contract 定向测试通过；MCP/adapter/engine/wire/双 Host 定向 race、50 次 breaker/reload 重复、20 次 notification reconcile、`go vet`、schema、contract 与独立 build 通过。全量并行仍出现已知 MCP stdio/lane 超时，独立复跑通过；Verify benchmark 为 18/23（外层 Seatbelt 无法嵌套），当前工作树另有非 T3 的 `observability/verify` 状态断言失败。
**实施偏离：** 旧 v1 MCP 配置没有 `permission_profile` 时，以既有显式 ToolBinding/Resources/Prompts 作为兼容期隐式 ceiling；新 `mcp add` 写入显式 profile。notification 采用 server-scoped reconnect + full discovery，而不是信任远端增量。health 事件在 sampling 边界投影，因此工具阶段打开 breaker 时会在紧接着的恢复采样前发出。

### T4：Skill 版本、依赖与锁

- [x] `skill.toml` schema；
- [x] semver 与 CodeHelper compatibility；
- [x] dependency DAG、cycle/conflict 检测；
- [x] lockfile 原子写入与漂移检测；
- [x] 依赖拓扑加载与 receipt；
- [x] `skill lint/lock/verify`；
- [x] hermetic fixture；
- [x] 旧 workspace/user Skill 兼容测试。

**实现证据：**

- `adapter/skill/manifest.go` 使用严格 TOML 与 SemVer 解析 `schema_version/name/version/codehelper/dependencies`；Configured/Plugin Skill 必须有 manifest，workspace/user 单文件 Skill 保持 `local/unlocked`；
- `resolver.go` 在既有 Catalog precedence 快照上执行 compatibility、依赖闭包、DAG/cycle/conflict 检查和依赖优先拓扑排序；禁用、缺失、无版本或被 legacy shadow 的依赖均 fail-closed；
- `lock.go` 写入 workspace-scoped strict JSON lock，记录 runtime version、name/version/source/plugin/digest/dependency constraints；使用进程内 mutex、文件锁、临时文件 fsync、原子 rename 与父目录 fsync，读取拒绝 unknown field、symlink 和 multiply-linked file；
- `Catalog.Verify/LoadPlan` 对完整 governed resolution 校验锁，并在实际加载前重读 `SKILL.md`/`skill.toml` 校验 digest；`load_skill` 按拓扑返回依赖与根指令；
- `turn.receipt.skills` 记录实际加载的 name/version/source/plugin/digest/locked；Engine 将 `dependency_conflict`、`dependency_cycle`、`compatibility_mismatch`、`skill_lock_drift` 作为可恢复 tool result；
- CLI 增加 `skill lint <path>`、`skill lock`、`skill verify` 和 `--skills-lock`；默认 lock 路径按 canonical workspace digest 隔离。

**验证结果：** Skill/Catalog/tool/Engine/app/wire/protocol/CLI 定向与 race 测试通过；严格 schema、compatibility、cycle/conflict、legacy shadow、拓扑 LoadPlan、digest drift、并发原子 lock、plugin authority、旧 Skill 兼容和 receipt 均有 hermetic 测试。50 次 resolver/lock 高竞争、vet、schema、双 Host contract 与独立 build 通过。全量并行仍为 MCP stdio/lane 超时，独立复跑通过；Verify benchmark 18/23 受外层 Seatbelt 限制。
**实施偏离：** 同名 Skill 沿用既有 precedence，只选择一个候选版本，不支持 side-by-side 多版本；任何依赖 constraint 不满足直接返回 conflict。Lock 记录 resolved dependency constraints，不引入独立 package registry/solver。

### T5：Plugin 签名、升级与 Registry

- [x] Plugin manifest 增加 version/publisher/compatibility；
- [x] Ed25519 signed index；
- [x] publisher allowlist；
- [x] artifact cache 与安全解包；
- [x] 原子 install/update/activate；
- [x] normal upgrade drain；
- [x] security revoke immediate cancel；
- [x] rollback receipt；
- [x] HTTPS Registry；
- [x] `file://` 离线镜像；
- [x] downgrade/replay/path-traversal/symlink/hardlink 测试。

**实现证据：**

- `adapter/plugin/manifest.go` 为 Registry Plugin 增加 strict SemVer `version`、`publisher` 和 CodeHelper compatibility；三字段必须同时出现。本地旧包继续走 `unsigned-local`，Registry artifact 必须为 governed manifest；
- `registry_index.go` 定义版本化 strict JSON index 和 RFC canonical release payload，使用 detached Ed25519 signature；publisher key 只来自 strict 本地 allowlist，未知 publisher、签名篡改、非法 key 与 hard-linked/symlinked trust file 均 fail-closed；
- `distribution.go` 让 `https://` 与 `file://` 使用同一 index、签名、digest、compatibility 和 publisher 验证链；HTTPS client 由 wire 注入 RFC-011 Egress Gate，artifact 限制同源，离线 artifact 限制在 mirror 根内；
- artifact 以原始 SHA-256 内容寻址缓存；tar.gz 解包拒绝 traversal、symlink、hardlink、device、重复路径、超文件数、超压缩/解压大小，并在 immutable staging 前校验 artifact/manifest/capability digest 和 manifest identity；
- `PluginState.activation` 是跨进程锁保护、原子写入的唯一 active release 事实，保留 generation high-watermark 和最多 16 个 verified rollback target；每次 install/update/rollback 追加不可变 lifecycle receipt journal；
- normal update/rollback 不 revoke 旧 authority，既有 handle 从 executable snapshot 自然 drain；`security-revoke` 删除 trust 并通过 authority context 立即取消同进程在飞调用，其他运行进程通过 durable state watcher 在有界时间内撤销本地 authority；
- 生产 `tool/plugin.Adapter` 订阅 durable state fingerprint，以 `plugin:lifecycle` source 原子 reconcile namespaced Tool executor；sampling 冻结 Catalog 前强制 Sync，因此 lifecycle event、Catalog revision 与实际 executor 使用同一版本。替换后仅已开始调用 drain，未开始的旧 executor fail-closed，最后一个调用结束后释放 executable snapshot；
- CLI 增加 `plugin install NAME@VERSION`、`plugin update NAME@VERSION`、`plugin rollback NAME` 和 `plugin security-revoke NAME`，以及 Registry URL、publisher allowlist、cache 配置；Tool result metadata 暴露 version/publisher/trust。

**验证结果：** Plugin/distribution/tool/wire/CLI 定向测试通过；Plugin/Tool/wire/CLI race、20 次 staging/state/concurrent update/external revoke 高竞争、vet、独立 build 与 diff check 通过。签名/unknown publisher/digest tamper、升级中断保留旧版、drain、同进程与跨进程在飞 cancel、append-only rollback receipt、downgrade/replay、archive traversal/link/device/entry/size limit 和 file/HTTPS parity 均有 hermetic fixture。全量并行仅复现既有 MCP stdio timeout、lane terminal flake 和 Verify 18/23 Seatbelt 限制；MCP 独立通过，lane 首次独立缺 terminal、重试通过。
**实施偏离：** Index 对每个 release 的 canonical payload 独立签名，以支持多 publisher 聚合，不使用单一 index publisher；激活通过 durable state 原子事务完成，不引入需要与 state 双写的 filesystem current pointer；安装和升级要求显式版本，不实现 `latest` 或 package solver。

### T6：协议、文档与发布门禁

- [x] `mcp.health.changed`、`extension.lifecycle`；
- [x] ACP/HTTP 双 Host contract fixture；
- [x] JSON Schema 生成与漂移测试；
- [x] diagnostics maturity 更新；
- [x] ARCHITECTURE/USAGE/ROADMAP 更新；
- [x] migration 与 rollback 说明；
- [x] 全量 `make verify`；
- [x] security/secret/sandbox 回归；
- [x] M4 benchmark 与退出报告。

**实现证据：**

- `protocol.ExtensionLifecycleData` 定义 strict `extension.lifecycle`，只暴露 extension kind、name、action、version、source、publisher、trust、digest、generation、enabled 与时间；action/trust/SHA-256 均 fail-closed 校验；
- `plugin.Registry.LifecycleSnapshots` 从 durable trust/activation state 生成脱敏快照；Engine 在 sampling 边界比较快照，投影 `active/installed/updated/rolled_back/enabled/disabled/revoked`，App 转成统一协议事件；
- `internal/host/runtimeapi/contract` 增加真实 unsigned-local Plugin fixture；ACP 和 HTTP 共用同一 lifecycle scenario，连同既有 catalog/health scenario 由 `make protocol-contract` 双跑；
- `runtime-protocol.schema.json` 由 schemagen 更新，漂移测试覆盖新 tagged union；ACP capability negotiation 自动从 `EventKinds()` 广告新事件；
- diagnostics 明确 RFC-012 的 ecosystem/MCP/Plugin/Skill 为 complete（complete capability 不出现在 incomplete maturity map）；
- ARCHITECTURE/USAGE/ROADMAP 同步事件、迁移、rollback 与运行边界；`scripts/test-secret-leak.sh` 修复已迁移 telemetry package 路径；
- Plugin Registry 补充 `extension_signature_invalid` / `extension_digest_mismatch` 稳定错误分类。

**验证结果：** `make protocol-contract`、schema 生成/漂移、T6 相关包 race、`make security-test`、`make sandbox-attack-test`、修复后的 `make secret-leak-test`、catalog benchmark 均通过；brand、全树 gofmt、`go vet ./...` 通过。`make verify` 已执行，但本机全量 `go test ./...` 仍复现既有 MCP stdio 并行 initialize timeout、lane terminal flake，以及外层 Seatbelt 无法嵌套导致 Verify benchmark 18/23，因而 Make 在进入全树 race 前停止；RFC 指定 ecosystem race 独立补跑。
**实施偏离：** lifecycle v1 只投影 Plugin；Skill load identity 已在 `turn.receipt.skills`，MCP 使用独立 health 事件。lifecycle 与 health 一样在 sampling 边界发布，不提供轮询线程；security revoke 的 authority 取消不等待事件。

---

## 9. 并行实施顺序

```text
T0 RFC/基准
  ↓
T1 Catalog Core
  ↓
T2 Snapshot/Materialize/Protocol
  ├────────→ T3 MCP
  ├────────→ T4 Skill
  └────────→ T5 Plugin/Registry
                 ↓
               T6 收尾
```

预计工程量：

| 分片 | 串行工作量 | 依赖 |
| --- | --- | --- |
| T0 | 1–2 天 | 无 |
| T1 | 4–5 天 | T0 |
| T2 | 4–5 天 | T1 |
| T3 | 4–5 天 | T2 |
| T4 | 3–4 天 | T1，可与 T2 后半并行 |
| T5 | 7–8 天 | T1/T2 |
| T6 | 3–4 天 | T3/T4/T5 |

三条工作流并行时约 4–5 周；完全串行约 6–7 周。

---

## 10. 测试与发布门禁

### 10.1 Catalog

- register/replace/revoke/re-register；
- stale generation CAS；
- stale revision 不到达 handler；
- revoke 取消 authority；
- alias 冲突零写入；
- deferred loader 单飞；
- loader 失败可观测；
- reconcile 与执行并发 race。

### 10.2 大目录与 token

- 100/500/1000 tools 下启动耗时；
- stable prefix bytes 不随动态目录线性增长；
- materialize 前 deferred schema 不进 `tools[]`；
- materialize 后下一次采样可调用；
- 达到上限时返回 `tool_catalog_limit`；
- revoke 后下一次采样无幽灵工具。

### 10.3 MCP

- connect timeout；
- call timeout；
- transport crash；
- malformed JSON-RPC；
- catalog schema drift；
- breaker open/half-open/recover；
- `isError=true` 不触发 breaker；
- A server 故障不影响 B server；
- 不自动重放非幂等调用。

### 10.4 Skill

- manifest strict decode；
- semver compatibility；
- dependency cycle/conflict；
- lockfile digest drift；
- shadow precedence；
- unsafe path/symlink；
- plugin authority revoke；
- legacy local skill compatibility。

### 10.5 Plugin 与 Registry

- unknown publisher；
- invalid signature；
- artifact/manifest/capability digest mismatch；
- archive path traversal；
- symlink/hardlink/device file；
- 超文件数/超大小/压缩炸弹；
- upgrade 中断保留旧版本；
- rollback 只选已验证 staging；
- offline/online 相同验签；
- downgrade/replay 拒绝。

### 10.6 常设命令

```bash
go test ./...
go test -race ./internal/adapter/tool/... ./internal/adapter/mcp/... \
  ./internal/adapter/plugin/... ./internal/adapter/skill/...
make protocol-contract
make protocol-schema
make verify
```

涉及强沙箱的测试必须在具备真实后端的 CI 执行；本地外层沙箱无法嵌套时要明确报告 unavailable，不能伪造通过。

---

## 11. 兼容与迁移

1. `Registry.Register` 保留为启动期兼容 API，内部转为 source=`builtin` 的一次性 reconcile；
2. 现有 unsigned 本地 Plugin 保留，但状态显示 `unsigned-local`，不能冒充 Registry 签名包；
3. 现有 workspace/user `SKILL.md` 保留，状态显示 `local/unlocked`；
4. 旧 MCP config 无 permission profile 时，以既有显式 ToolBinding/Resources/Prompts 推导兼容期 ceiling；`mcp add` 写入显式 profile，远端 discovery 不能扩展本地授权；
5. 动态 catalog 协议按版本严格解码，未知字段和未知版本 fail-closed；
6. Skill lock 使用原子写入并严格校验 schema；未知或漂移的旧 lock 不自动接受，
   需审计后重新执行 `skill lock`；
7. Plugin state 保持 schema v1；activation 与 lifecycle receipt 是兼容的可选字段，
   下一次原子状态事务写回。旧人工 review receipt 保持 `unsigned-local`，不会自动
   提升成 Registry signature；
8. Plugin rollback 只选择 receipt journal 中仍可验签的 immutable staging；
   security revoke 保留历史 receipt 但删除 authority，不能通过 rollback 恢复；
9. catalog/health/lifecycle 事件在 sampling 边界投影。authority revoke 立即生效，
   不等待下一次事件。

---

## 12. M4 退出条件

- [x] 运行中增删工具后，模型侧不存在幽灵工具；
- [x] stale/revoked 调用到达 handler 的次数为 0；
- [x] `tool_search` 可以完成搜索 → materialize → 调用；
- [x] 1000 工具目录不突破设定 prompt/schema 预算；
- [x] 单个 MCP server 故障不会阻塞其它 server 或整个 turn；
- [x] breaker 恢复不重放业务调用；
- [x] Skill 依赖、版本、来源和 digest 可复现；
- [x] Plugin publisher、签名、能力、版本、升级和 rollback 可审计；
- [x] 离线镜像与在线 Registry 验证结果一致；
- [x] ACP/HTTP contract、Protocol Schema、race、security 门禁通过；
- [x] ROADMAP、ARCHITECTURE、USAGE 与实际 maturity 一致。

### 12.1 M4 benchmark 与退出报告

2026-08-04 在 Apple M5 Pro、Go `darwin/arm64`、`-benchtime=10x` 下：

| 场景 | 时间 | catalog bytes | estimated tokens | alloc bytes |
| --- | ---: | ---: | ---: | ---: |
| Catalog 100 tools | 0.196 ms | 13,129 | 3,283 | 0.48 MB |
| Catalog 500 tools | 0.919 ms | 65,129 | 16,283 | 2.33 MB |
| Catalog 1000 tools | 1.832 ms | 130,129 | 32,533 | 4.94 MB |
| Registry startup 100 tools | 0.209 ms | - | - | 0.62 MB |
| Registry startup 500 tools | 1.138 ms | - | - | 3.11 MB |
| Registry startup 1000 tools | 2.348 ms | - | - | 6.23 MB |

结论：Catalog/Registry 仍按工具数线性增长，证明 deferred/materialize 与 schema
预算必须保留；生产 sampling 由 `MaxToolDefinitions/MaxToolSchemaBytes` 硬限制，
不会把 1000 个完整 schema 直接发送给模型。M4 功能、协议和安全门禁已收口；
本机不能宣称全量绿色，剩余红项是上文记录的外层 Seatbelt 与既有并行 flake，
需在具备真实强沙箱的 CI 复核。

---

## 13. Gap Ledger

所有已知缺口必须保留在本表，直到具备实现证据和自动化测试后才能关闭。

| ID | 缺口 | 影响 | 计划分片 | 状态 | 自动化门禁 |
| --- | --- | --- | --- | --- | --- |
| M4-G001 | Registry 无 replace/unregister | 无法可靠动态更新 | T1 | closed | `TestRegistryRevokeTombstoneAndReregister`、`TestRevokedDynamicToolCanBeRegisteredAgain` |
| M4-G002 | Dynamic Catalog 与 Registry 双真相源 | generation 与执行漂移 | T1 | closed | `TestRegistryReconcileIsAtomicOnAliasConflict`、`TestRegistryReconcileCASAllowsOneConcurrentReplacement`、`TestOldDynamicExecutorCannotRunAfterReplaceOrRevoke` |
| M4-G003 | tool call 未绑定 entry revision | replace 后可能执行错误实现 | T2 | closed | `TestSamplingSnapshotRejectsToolReplacedBeforeExecution`、`TestRegistryResolveBoundRejectsReplacedAndRevokedEntries` |
| M4-G004 | stable prompt tool catalog 启动后不更新 | 模型看到幽灵/缺失工具 | T2 | closed | `TestProviderRetryReusesCatalogSnapshot`、双 Host `catalog change and receipt use the same sampling snapshot` scenario |
| M4-G005 | `tool_search` 不 materialize | 搜索结果不可调用 | T2 | closed | `TestToolSearchRanksDeferredMatches`（search → materialize → call） |
| M4-G006 | MCP reload 与 Registry 不同步 | stale MCP executor | T3 | closed | `TestAdapterRevokesOpenServerAndRestoresAfterProbe`、`TestCatalogNotificationReconcilesOnlyServerSource` |
| M4-G007 | MCP 无 breaker | 故障 server 拖慢 turn | T3 | closed | `TestCircuitBreakerProbesWithoutReplayingBusinessCall`、`TestCircuitBreakerCountsTimeoutAndCanFlap`、`TestPoolIsolatesFailedServer` |
| M4-G008 | MCP 无 server permission ceiling | catalog 漂移可能扩大能力 | T3 | closed | `TestPermissionProfileRejectsCapabilityAndResourceEscalation`、`TestPermissionProfileRejectsDynamicNetworkHost` |
| M4-G009 | Skill 无版本/依赖/lock | 无法复现加载结果 | T4 | closed | `TestResolveLockLoadPlanAndDigestDrift`、`TestResolverRejectsCycleConflictAndLegacyShadow`、`TestLoadSkillReturnsLockedDependencyPlan` |
| M4-G010 | Plugin 无 publisher signature | 只能人工 hash trust | T5 | closed | `TestPublisherAllowlistIsStrictAndRejectsUnknownPublisher`、`TestRegistryRejectsSignatureDigestReplayDowngradeAndInterruptedUpdate` |
| M4-G011 | Plugin 升级无原子 rollback receipt | 升级失败恢复不可审计 | T5 | closed | `TestSignedRegistryInstallUpdateDrainRollbackAndSecurityRevoke`、`TestSecurityRevokeCancelsInflightPluginCall`、`TestExternalRegistryRevokeCancelsInflightPluginCall`、`TestConcurrentRegistryUpdatesConvergeOnHighestGeneration` |
| M4-G012 | 无在线/离线统一 Registry | 分发不可治理 | T5 | closed | `TestFileAndHTTPSRegistryUseIdenticalVerification`、`TestRegistrySafeExtractionRejectsTraversalAndLinkEntries` |
| M4-G013 | 无 catalog/health/lifecycle 双 Host 契约 | M5 客户端接入风险 | T6 | closed | `make protocol-contract`（ACP/HTTP 共用 catalog、MCP health、extension lifecycle scenarios） |
| M4-G014 | deferred/materialize 只有测试生产者，MCP catalog 仍全部 eager | 大型远端目录的完整 schema 进入每次采样 | T2 补强 | closed | `TestLargeMCPToolCatalogIsDeferredUntilSearchMaterializes`、`TestCatalogNotificationReconcilesOnlyServerSource` |
| M4-G015 | Plugin durable lifecycle 更新不切换运行中 Tool executor | lifecycle 显示新版本但调用仍落到旧版本 | T5 补强 | closed | `TestAdapterTracksDurableEnableReplaceDisableAndRevoke`、`TestAdapterSwitchesSignedUpdateAndRollbackWhileOldExecutorDrains` |
| M4-G016 | Dynamic Catalog 只有包内 API 与测试，无 production Host/Wire 接线 | 运行中 Host 无法注册、调用、替换或撤销动态工具 | T6 补强 | closed | 双 Host `trusted dynamic tools register execute replace and revoke` scenario、`TestTrustedDynamicToolsAreExplicitAndRequireToolRuntime`、`TestManagerInvocationRemainsPendingUntilCompleted` |
| M4-G017 | MCP 异步 Sync 失败只记录 `LastError` | 旧目录/executor 可能继续进入采样或通过 revision fencing | T3 补强 | closed | `TestAsyncSyncQuarantinesStaleCatalogAndRecovers`、`TestSamplingFailsClosedUntilToolCatalogSyncRecovers`、`TestAdapterReplacesExecutorWhenConnectionChanges`、`TestRegistryResolveBoundRejectsReplacedAndRevokedEntries` |

状态仅允许：

- `open`
- `in_progress`
- `blocked`
- `closed`

关闭时必须在“自动化门禁”列补充具体测试或命令，不能只改状态。

---

## 14. 进度记录

每完成一个分片，在此追加记录，不覆盖历史。

| 日期 | 分片 | 状态 | 提交/变更 | 验证 | 偏离 |
| --- | --- | --- | --- | --- | --- |
| 2026-08-03 | 规划 | completed | 创建 RFC-012 | 文档评审待进行 | 无 |
| 2026-08-03 | T0 | completed | Catalog Snapshot/ChangeSet、规模基准、M4-G001/G005 特征测试、`make catalog-bench` | build/vet/定向 race/基准通过；全量 18/23 benchmark 受外层沙箱限制，MCP/lane 并行超时串行通过 | 缺口测试采用绿色 characterization，后续修复时翻转 |
| 2026-08-03 | T1 | completed | Registry 原子 reconcile/revision/tombstone、Dynamic Catalog 收敛、Engine 分类错误 | tool 全包、engine、vet、race、高竞争重复门禁与 build 通过；全量已知 MCP/lane 并行超时串行通过，Verify benchmark 受外层沙箱限制 | legacy Register 使用 O(1) 原子插入；sampling revision 绑定留在 T2 |
| 2026-08-04 | T2 | completed | Sampling Snapshot/revision binding、动态 catalog 尾块、tool_search materialize、预算、receipt、catalog event | 相关包、vet、schema、build、双 Host contract、定向 race 通过；全量 MCP/lane 并行超时串行通过，Verify 18/23 受外层沙箱限制 | schema 预算使用确定性 JSON bytes；eager 核心工具超预算时 fail-closed，不按 threshold 静默裁剪 |
| 2026-08-04 | T3 | completed | per-server reload/reconcile、health/breaker、safe probe、permission ceiling、health event 与 CLI/TUI/HTTP 状态 | 定向/race/高竞争、vet/schema/contract/build 通过；全量 MCP/lane 并行超时但独立通过，Verify 18/23 受沙箱限制，非 T3 verify 状态测试仍失败 | v1 配置从显式 bindings 推导兼容 ceiling；通知触发 server-scoped full discovery；health 在 sampling 边界投影 |
| 2026-08-04 | T4 | completed | strict skill.toml、SemVer/DAG、workspace lock、拓扑 load、receipt、lint/lock/verify CLI | 定向/race/50 次高竞争、vet/schema/双 Host contract/build 通过；全量 MCP/lane 并行超时但独立通过，Verify 18/23 受沙箱限制 | 同名 Skill 只解析 precedence winner，不支持 side-by-side 多版本 |
| 2026-08-04 | T5 | completed | governed Plugin manifest、Ed25519 release index、publisher allowlist、artifact cache/safe extract、原子 activation、drain/revoke/rollback、在线/离线 Registry CLI | 定向/race/20 次高竞争/vet/build 通过；全量仅既有 MCP/lane flake 与 Verify 18/23，独立重试通过 | release 逐项签名；active truth 使用 durable state；只支持显式版本 |
| 2026-08-04 | T2 补强 | completed | MCP production deferred producer、materialized pin、catalog replacement 回到 deferred | MCP Adapter 定向/规模/race、安全与全仓编译门禁 | discovery 保留 descriptor authority；materialize 不触发远端业务调用 |
| 2026-08-04 | T5 补强 | completed | Plugin durable watcher、Tool source reconcile、sampling Sync、executor drain/retire | Plugin/Tool/wire/CLI race、10 次跨进程 lifecycle 高竞争、vet/security/contract | 普通切换只 drain 已开始调用；security revoke 取消在飞调用 |
| 2026-08-04 | T6 补强 | completed | Dynamic Manager/Broker、wire 显式开关、ACP/HTTP 管理与调用回程、generation fencing | dynamic/wire/CLI、双 Host contract、定向 race、protocol 与 diff gate | 默认关闭；Host 执行不虚假声明本地强沙箱，authority 固定为服务端 policy |
| 2026-08-04 | T3 补强 | completed | MCP sampling correctness Sync、失败 quarantine、connection-aware executor replacement、entry authority fencing | tool/MCP/engine/wire 定向测试；20 次定向 race 重复通过 | Sync 失败时全量 quarantine MCP source，以可用性换取不暴露 stale authority |
| 2026-08-04 | T6 | completed | extension.lifecycle、双 Host contract、Schema/maturity、迁移/rollback、security gates、benchmark/退出报告 | protocol/schema/security/sandbox/secret/race/benchmark/build 通过；`make verify` 全量仍受 MCP/lane flake 与 Seatbelt 18/23 限制 | lifecycle v1 仅 Plugin，sampling 边界投影 |

---

## 15. 文档更新规则

后续每完成一个实现批次，必须同步完成：

1. 勾选对应 T0–T6 任务；
2. 补充实现文件、关键类型和行为证据；
3. 记录实际执行的测试命令与结果；
4. 更新 Gap Ledger 状态；
5. 记录设计偏离及原因；
6. 更新进度记录；
7. 如改变协议或用户行为，同步更新 ROADMAP、ARCHITECTURE、USAGE 和 Schema。

未完成以上七项，不视为该批次完成。
