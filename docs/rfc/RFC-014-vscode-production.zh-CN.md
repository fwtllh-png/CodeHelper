# RFC-014：M5 V3 VS Code Production

> 状态：Implementing
> 关联：[ROADMAP §6](../ROADMAP.zh-CN.md)、[RFC-003](./RFC-003-vscode-transport.zh-CN.md)、[RFC-004](./RFC-004-context-bridge.zh-CN.md)、[RFC-005](./RFC-005-edit-transaction.zh-CN.md)、[RFC-013](./RFC-013-vscode-companion.zh-CN.md)
> 影响面：`extensions/vscode`、`internal/host/runtimeapi/acp`、`internal/runtime/app`、`internal/runtime/protocol`、`internal/buildinfo`、`scripts/package-release.sh`、发布流水线

## 1. 目标

M5 V3 将已完成的 V1 Companion 和 V2 Coding Native 提升为可分发、可升级、可在
真实远程 Extension Host 与 multi-root workspace 中工作的生产扩展。

V3 必须完成以下闭环：

1. 一个 VS Code 窗口可管理多个 workspace root，每个 root 的 Runtime、session、
   cursor、approval、edit plan、后台任务和数据目录严格隔离；
2. Remote SSH、Dev Container 与 Codespaces 类 remote Extension Host 上，
   Runtime、binary、路径、存储和 sandbox 均以 Extension Host 所在端为准；
3. 官方 binary 可通过受签名清单安装、更新、健康检查和回滚，外置 binary 仍可显式使用；
4. extension、ACP protocol、binary 和 target platform 的兼容关系可机读、可测试，
   不自动跨越不兼容版本；
5. 产出 universal thin VSIX 与 target-specific bundled VSIX，具备 Marketplace、
   Open VSX、企业市场和离线分发所需的元数据、provenance 与发布门禁；
6. local/multi-root/remote/update/distribution 的安全、兼容、性能和恢复矩阵通过。

退出条件是：用户在支持的本地或远程窗口中，能对多个 root 分别完成 V2 的
“理解 -> 修改 -> 审批 -> 验证”闭环；官方 binary 的来源和版本可验证，失败更新可恢复；
同一构建产物可按渠道规则发布或离线安装。

## 2. 明确不做

- VS Code for Web / browser extension；V3 仍依赖 Node Extension Host、子进程和 stdio；
- 一个 turn、edit plan 或 atomic transaction 跨越多个 workspace root；
- 在一个窗口中混用多个 remote authority，或混合 local 与 remote root；
- 共享一个可写 Runtime 进程服务多个 root；
- 绕过 VS Code Remote Extension Host，直接从本地 UI 端 SSH/WSL exec；
- 自建通用软件包管理器、依赖求解器或任意第三方 update feed；
- 从 workspace setting、仓库文件或模型输出加载签名公钥、下载地址或发布凭据；
- 自动升级到 compatibility manifest 未明确允许的 binary/protocol 版本；
- 在仓库或 VSIX 中保存 Marketplace、Open VSX、企业市场或签名私钥；
- 当前分片不提供 WSL2 Windows runner；WSL2 后续作为独立平台矩阵补充；
- 承诺 macOS Seatbelt、Linux namespace/Landlock 和 Windows sandbox 能力相同；
  Runtime 必须按实际运行端报告能力并 fail-closed；
- 扩展 V2 的上下文种类、旁路模型调用或引入 proposed VS Code API。

## 3. 当前基础与缺口

### 3.1 已有基础

- extension 已通过 ACP v2 结构化握手、feature 协商和 durable cursor replay；
- `RuntimeController` 已封装单 root Runtime 生命周期，Runtime 持有最终 Guard、
  edit transaction、journal 和 Verify Gate 权威；
- V2 ContextBridge、Code Action、Changes 和后台 Tree 均有严格输入边界；
- `@vscode/test-electron`、真 binary/stdin、双 Host contract、安全、性能和 VSIX
  allowlist/install 已形成常设门禁；
- Runtime 持久层的 workspace、repo index、agent、task、usage 已按 root path 留有隔离面；
- `scripts/package-release.sh` 已构建 Linux/macOS/Windows 五个 target binary、checksum
  和 SBOM。

### 3.2 必须先解决的缺口

| 缺口 | 当前行为 | V3 风险 |
| --- | --- | --- |
| Controller 单例 | 多 root 直接显示 unavailable | 不能隔离或路由 root |
| Binding 单值 | `workspaceState` 只保存一个 v1 binding | root 间 session/cursor 覆盖 |
| 数据目录固定 | 所有 root 使用 `storageUri/runtime` | 多 Runtime 共享状态目录 |
| 本地 URI 假设 | controller/context/diagnostic 只接受 `file:` | remote editor URI 无法复验 |
| 本地路径等同 editor URI | Runtime 要求 `file:` URI 与磁盘路径相同 | 删除 scheme 检查会产生身份漏洞 |
| binary 只查 path | 任意兼容范围的 executable 均可启动 | 无来源、target 和版本治理 |
| 签名是占位 | `SHA256SUMS.sig` 不是密码学签名 | 不能支撑自动安装或更新 |
| 包仅开发态 | `private: true`，缺渠道和产品元数据 | 不能发布到目标市场 |
| 测试仅 local Electron | 没有 remote Extension Host 矩阵 | 路径/存储/target 退化不可见 |

## 4. 架构决策

### D1：multi-root 使用“一 root 一 Runtime”，扩展内增加 Registry

新增 `WorkspaceRuntimeRegistry`，以 canonical workspace URI 生成稳定 `root_id`，
持有每个 root 的：

```text
RootRuntime
  root identity
  RuntimeController + ContextBridge
  session/thread/cursor binding
  Chat/Changes/background projector
  lifecycle and resource status
```

- 不让一个 Runtime 的 sandbox 覆盖多个 root；
- binding store 升为 v2 map，以 `root_id` 为 key；v1 single-root binding 原位迁移；
- data directory 使用 `storageUri/runtime/<root_id>`，绝不只按 basename 分目录；
- root 动态 add/remove 时创建或 detach 对应 Runtime；有 active turn、approval 或后台任务
  的 root 不静默删除状态，先停止连接并保留 durable data；
- 同时管理的 root 数必须有显式上限和可诊断降级，防止恶意 workspace 文件启动无界进程；
- Runtime、event、request、turn 和 plan identity 在扩展内都加 root namespace，不能假设
  两个独立 Runtime 铸出的 ID 全局唯一。

Chat 保持一个 View，但增加明确的 root selector。active editor 只更新默认候选，不在有
草稿或 pending modal 时静默切换 root。selection/Code Action 按 document 所属 root 路由；
Changes 和后台 Tree 以 root 为第一层节点。每个 turn 和 approval UI 必须显示 root label。

### D2：远程身份同时绑定 editor URI 与 Runtime 本地路径

仅删除 `scheme === "file"` 检查是不安全的。V3 定义版本化 `WorkspaceIdentity`：

```json
{
  "version": 1,
  "root_id": "sha256...",
  "editor_uri": "vscode-remote://ssh-remote+host/workspace",
  "runtime_path": "/workspace",
  "remote_name": "ssh-remote"
}
```

- Extension Host 从 `WorkspaceFolder.uri`、`fsPath` 和 `vscode.env.remoteName` 构造身份；
- launch 与 ACP initialize 都携带同一 identity，Runtime 绑定后不得由 turn 覆盖；
- editor context 继续携带原始 editor URI、workspace-relative path、version 和 digest；
- Runtime 先验证 URI 属于绑定的 `editor_uri`，再用 `runtime_path + relative path`
  走现有 sandbox/symlink/digest/range 复验；
- local root 是同一模型的特例：`editor_uri=file:...`、`remote_name` 为空；
- authority、scheme、query、fragment、大小写和 percent encoding 必须 canonical；
- root identity 变化视为新 workspace，不复用旧 binding 或 approval。

该变更进入共享 protocol/schema/ACP contract，并同步 RFC-003；HTTP Host 也必须能表达同一
workspace identity，不能产生 VS Code 私有安全语义。

### D3：扩展固定运行在 Workspace Extension Host

manifest 显式声明：

```json
"extensionKind": ["workspace"]
```

binary discovery、download、`globalStorageUri`、`storageUri`、`child_process`、cwd 和
sandbox probe 全部发生在该 Extension Host。V3 不从 UI machine 推断远端 OS/arch。

- remote workspace 只接受该端可执行文件；
- managed artifact target 使用 `process.platform/process.arch`；
- Runtime data 和 managed binary 都必须位于 Extension Host 本地的 `file:` storage；
- storage 不可用、target 未支持或 remote authority 不一致时显式 unavailable；
- Remote SSH 断线沿用 supervisor + durable cursor 恢复，不改走 localhost HTTP。

### D4：binary 有三种来源，信任边界不同

`codehelper.binarySource`：

1. `external`：用户/管理员提供绝对路径或 Extension Host `PATH`；
2. `managed`：扩展从官方签名 release manifest 安装到 `globalStorageUri`；
3. `bundled`：target-specific VSIX 内带与扩展一起签发的 binary。

`auto` 只按“有效 bundled -> 有效 managed -> external”选择，不混合文件。workspace
配置在 untrusted workspace 中仍不能选择 external executable；update URL、公钥和 channel
不允许 workspace scope。

生产扩展拒绝名称错误、target 不符、版本不在兼容表、`dev/unknown` build 或握手 feature
不完整的 binary。Development mode 可显式放宽 `dev`，但门禁必须证明生产包没有该路径。

### D5：官方更新使用真实签名清单和原子回滚

新增 canonical JSON release manifest，每个 artifact 至少包含：

```text
schema/version/channel/sequence
binary version + commit + build time
target os/arch + bytes + sha256 + URL
ACP min/max + required features
extension compatibility range
SBOM/provenance digest
revoked versions/digests
signing key id + Ed25519 signature
```

- trust root 固化在扩展发布物；key rotation 由旧 key 签发新 key statement；
- manifest 与 artifact 只允许 HTTPS、固定 origin、有限 redirect、大小和 timeout；
- 先验证 canonical manifest signature，再下载到临时文件，校验 size/SHA-256，拒绝
  symlink/hardlink/路径穿越，设置最小 executable 权限；
- 安装前执行 `version --json` target/版本预检，启动后完成 ACP handshake + session attach
  才标记 healthy；
- active pointer 通过同目录原子 rename 更新，保留至少一个 last-known-good；
- 更新失败自动回滚且写本地 receipt；不得回滚到 revoked artifact；
- 默认策略为 `notify`，`off|notify|auto` 只能由 user/machine policy 设置；
- 任何跨不兼容 protocol/binary range 的版本只提示，不自动安装。

现有 `SHA256SUMS.sig` 占位文件不能作为 V3 证据；T3 必须替换为 CI/KMS 提供的真实签名，
私钥不进入仓库、日志、SBOM 或 VSIX。

### D6：兼容性由一个机读清单驱动

新增单一 `compatibility.json`，生成或校验：

- extension semver；
- binary semver range；
- ACP protocol min/max；
- operation/schema version；
- required methods/features；
- supported targets 和 channels。

`version --json` 增加 binary target 与 protocol range；ACP `serverInfo.version` 使用真实
build version，不再返回常量 `"1"`。扩展启动、managed installer、VSIX package 和 release
CI 都读取同一清单。未知字段、重叠/空 range、target 缺失和版本倒退 fail-closed。

### D7：分发产物分为 universal 与 target-specific

- universal thin VSIX：不内嵌 Runtime，支持 external 或 managed binary；
- target-specific VSIX：每包只内嵌一个 target binary，不打包其他平台；
- target 基线：`linux-x64`、`linux-arm64`、`darwin-x64`、`darwin-arm64`、
  `win32-x64`；
- Marketplace/Open VSX/企业市场/离线 VSIX 使用同一 extension bundle、兼容清单和
  provenance，渠道差异只在 publisher/上传步骤；
- manifest 补齐 repository、homepage、bugs、icon、keywords、categories、
  changelog、security/privacy/support 文档，并在发布构建中移除 `private`；
- 每个 VSIX 做 allowlist、SBOM、SHA-256、签名/provenance、安装、启动、握手和卸载测试；
- publish credential 仅由 CI secret/OIDC 提供。没有凭据时仍可完成 publish dry-run，
  但不得把“未上传”写成“渠道已发布”。

### D8：资源、状态与可观测性必须 root-aware

- status、Output、通知、错误和 telemetry 都包含无敏感路径的 root label/root ID；
- managed update receipt 记录版本、digest、channel、结果和 rollback，不记录 token；
- activation 仍 `<100 ms`，不在同步 activation 中下载、启动所有 Runtime 或探测网络；
- root registry 的内存和进程数有预算；Tree 合并刷新，禁止每 root 固定 poll timer；
- remote authority、绝对路径、下载 URL 和签名失败不能进入 Webview 可执行消息；
- Workspace Trust 为窗口级只读下限；每个 Runtime 仍由自身 Guard 做最终判定。

## 5. 实施分片

| 分片 | 内容 | 退出条件 | 状态 |
| --- | --- | --- | --- |
| T0 | WorkspaceIdentity、binary/protocol compatibility 清单与共享协议 | local/remote identity 可判定，schema/ACP/HTTP/TS 无漂移 | completed |
| T1 | WorkspaceRuntimeRegistry、binding v2 与 multi-root UI/命令路由 | 两个 root 并行 turn/replay/approval/edit 完全隔离 | completed |
| T2 | Workspace Extension Host 与 external binary remote 支持 | 目标 VS Code 的 Remote SSH/Dev Container 完成 V2 native flow | completed |
| T3 | signed manifest、managed/bundled binary、原子更新与回滚 | 篡改/错 target/不兼容/启动失败均零激活并可恢复 | completed |
| T4 | universal/target VSIX 与 Marketplace/Open VSX/企业/离线发布流水线 | 全 target 产物可审计、安装、启动，渠道 dry-run 可复现 | completed |
| T5 | local/multi-root/remote/update 完整 E2E 与性能/恢复矩阵 | 支持矩阵、断线和资源预算全部通过 | completed |
| T6 | 安全审查、兼容报告、文档与 release candidate 收口 | V3 Gap 全关闭，RC 证据完整 | completed |

执行必须严格按 T0 -> T6，一次只推进一个分片。T0 未冻结身份和兼容契约前，不实现
downloader；T2 未证明 remote external binary 前，不用 updater 掩盖远程路径问题。

## 6. 分片细则

### T0：身份与兼容地基

- 定义 `WorkspaceIdentity`、canonical root ID 和远程 URI 校验；
- `BindingStore` v2 迁移格式先冻结，但 registry 留到 T1 接入；
- 扩展 `version --json`、ACP initialize、protocol schema 与 contract；
- 引入 `compatibility.json` 及生成/漂移检查；
- local `file:` 行为保持兼容，remote URI 负面语料 fail-closed；
- 更新 RFC-003 的 transport/security contract。

### T1：multi-root

- 引入 registry/root runtime abstraction，删除 module-level controller 单例；
- 每 root 独立 data dir、binding、supervisor、projector 和 disposal；
- Chat root selector、Tree root grouping、status/restart root picker；
- selection/diagnostic 按 document URI 路由，Chat submit 按显式 selected root；
- root add/remove、同名 folder、ID collision、跨 root forged request、一个 root crash
  不影响另一个 root；
- 至少两个真实 Runtime 并发完成 approve/deny/replay fixture。

### T2：remote external binary

- manifest `extensionKind=["workspace"]`；
- storage、target、PATH/config 和 launch 全部按 Extension Host；
- context/diagnostic/plan URI 在 SSH/Container 下保持 editor identity 与 runtime path
  双重复验；
- 远程断线、Extension Host restart、Trust 变化和 sandbox unavailable 可诊断；
- Codespaces 类环境通过 Linux remote contract；实际公共 Codespaces 不作为无凭据 CI
  的硬依赖。

### T3：binary 供应链

- release script 生成逐 target raw artifact、SBOM、provenance 和 canonical manifest；
- CI/KMS 签发 Ed25519 signature，仓库只含 public trust root；
- installer 使用临时文件、内容寻址目录、锁、原子 pointer 和 last-known-good；
- bundled binary 与 managed cache 使用同一 verifier；
- update check 去重、频率上限、代理/离线错误、并发窗口竞争和磁盘不足处理；
- corruption、rollback、revocation、key rotation 和 crash-during-install fault injection。

### T4：渠道

- 生产 manifest/README/CHANGELOG/icon/security/privacy/support 元数据；
- universal + 五 target VSIX；
- Marketplace/Open VSX CLI dry-run 与企业/离线产物布局；
- publisher/channel 名称映射独立于 extension runtime 代码；
- release provenance 将 extension、binary、compatibility、SBOM digest 绑定；
- 有凭据的 staging publish 后做 install/list/activate；无凭据只报告 dry-run。

### T5：矩阵

最低矩阵：

| Host | Root | Binary | 必测 |
| --- | --- | --- | --- |
| macOS arm64/x64 | single + multi | external；arm64 bundled | native flow、restart、update |
| Linux x64/arm64 | single + multi | managed；bundled static/release gate | sandbox、update/rollback |
| Windows x64 | target package static gate | bundled | PE target、manifest、checksum |
| Remote SSH Linux | single + multi | remote external/managed | disconnect/replay |
| Dev Container | single + multi | bundled/managed | rebuild/re-attach |

WSL2 与 Windows native runner 不属于当前 V3 分片的退出门禁；Windows x64 只做 target
package 静态审计，不记为动态 E2E。macOS x64 在 arm64 host 上以官方 x64 VS Code
Electron Host + Rosetta 和 darwin/amd64 Runtime 执行 single/multi native flow。

每个环境至少覆盖 Chat selection、Code Action、multi-file Changes、approval、Verify Gate、
Runtime crash/replay 和 untrusted read-only。remote 测试必须证明 binary 进程运行在
remote Extension Host，而不是 UI machine。

### T6：收口

- threat model：workspace 配置投毒、manifest/artifact 篡改、rollback、root confusion、
  remote authority spoof、symlink/hardlink、zip slip、TOCTOU、并发 update；
- compatibility report 和支持/不支持 target 文档；
- activation、root registry、Runtime ready、1 MiB capture、Tree refresh 和 update
  内存/时延预算；
- VSIX allowlist、secret scan、dependency audit、SBOM/provenance/signature verify；
- 更新 ARCHITECTURE、USAGE、ROADMAP、RFC-003 和 release runbook；
- 产出 release candidate，不在没有渠道凭据时虚报 public publish。

Threat model 与控制：

| 威胁 | 输入边界 | 控制与证据 |
| --- | --- | --- |
| workspace 配置投毒 | repository settings | Untrusted 仅接受 global binary path；source/channel/policy 为 application scope |
| manifest/artifact 篡改 | HTTPS release origin | strict canonical JSON、Ed25519、target/bytes/SHA-256、固定 origin |
| rollback/revocation | signed sequence/state | monotonic sequence、active/LKG revocation、pending healthy 后发布 |
| root confusion | Webview/Tree/ACP ID | SHA-256 root ID、root-keyed projector/binding、plan/request 二次查找 |
| remote authority spoof | workspace URI/remoteName | canonical URI、authority match、Host-local file storage、target match |
| symlink/hardlink/path traversal | managed store/VSIX | `lstat`、`nlink=1`、digest path、allowlist、无 archive extraction |
| update TOCTOU/并发 | 双窗口/重复命令 | Extension single-flight、store lock、exclusive staging、atomic rename |
| update 资源耗尽 | manifest/artifact stream | 1 MiB manifest、256 MiB artifact、30 s、3 redirects、disk reserve |
| source/provenance 冒充 | release worktree | source fingerprint；生产模式拒绝 dirty；dry-run 不可发布 |
| secret 泄漏 | VSIX/channel/RC output | allowlist、release artifact secret scan、无 private key/credential |

## 7. 测试与发布门禁

### 7.1 常设门禁

保留 V2 全部命令，并新增：

```text
make vscode-compatibility
make vscode-multiroot-integration
make vscode-remote-ssh-integration
make vscode-wsl-integration
make vscode-devcontainer-integration
make vscode-update-integration
make vscode-distribution
```

remote gate 可由对应 OS runner 执行，但 release candidate 必须聚合所有必需 job 的
provenance，不能在本地缺环境时标绿。

### 7.2 必测安全场景

- 两 root 使用相同 turn/request/plan ID 也不能串流或误审批；
- root remove/re-add、rename、同名目录和 URI authority 变化不复用旧 capability；
- remote URI percent-encoding、authority、query、fragment、大小写和 path traversal；
- untrusted workspace 不能选择 binary、feed、channel、公钥或 update policy；
- manifest/signature/checksum/size/target/version 任一不符均不执行 artifact；
- download redirect、超时、截断、超限、代理错误和离线不破坏 last-known-good；
- update 中断、双窗口并发、磁盘满和启动后 handshake 失败可原子回滚；
- revoked binary 不启动、不作为 rollback target；
- Webview/Tree 伪造 root ID 不能跨 root 读取、提交或审批；
- remote 端 secret/path 不进入 telemetry、渠道日志或 update receipt。

### 7.3 性能与资源预算

- synchronous activation `<100 ms`；
- root registry 初始化不做网络 I/O；
- 单 root Runtime ready p95 `<5 s`，multi-root 启动必须有并发上限；
- idle root 的扩展侧投影和监听有界，Tree 无 per-root poll timer；
- update manifest 有缓存/ETag 与至少 24 h 自动检查间隔；
- artifact 下载有字节、时间、并发和磁盘预算；
- V2 的 Code Action `<20 ms`、1 MiB capture `<100 ms`、10k event 与 context/plan
  上限不退化。

## 8. Gap Ledger

| ID | 缺口 | 风险 | 分片 | 状态 | 计划证据 |
| --- | --- | --- | --- | --- | --- |
| M5-G021 | 单例 controller/binding/data dir | multi-root 状态串扰 | T1 | closed | two-root Electron + true stdio |
| M5-G022 | editor URI 与 runtime path 只支持 local file | remote 身份校验失真 | T0/T2 | closed | shared identity contract + SSH/Container E2E |
| M5-G023 | extension 未固定 Workspace Host | binary 可能跑在错误机器 | T2 | closed | remote process identity gate |
| M5-G024 | binary 无机读兼容矩阵 | 错版本启动或错误自动升级 | T0 | closed | compatibility drift/negative suite |
| M5-G025 | checksum signature 仍为占位 | binary 供应链不可验证 | T3 | closed | signed manifest + fault injection |
| M5-G026 | 仅有 universal 开发 VSIX | 目标渠道与 bundled binary 缺失 | T4 | closed | target VSIX/channel dry-run |
| M5-G027 | 无 remote/multi-root/update release matrix | 生产退化不可见 | T5/T6 | closed | 15/15 machine-readable matrix evidence |

## 9. 状态更新规则

每完成一个分片必须同时：

1. 将 §5 对应状态改为 `completed`；
2. 关闭 §8 中已有真实自动化证据的缺口；
3. 记录命令、平台、target、测试数量、性能数据和环境限制；
4. 更新 ROADMAP M5 当前进度；
5. 协议/身份变化同步 RFC-003，编辑权威变化同步 RFC-005/ARCHITECTURE；
6. 签名、渠道或 remote 只做 mock/dry-run 时必须明确标注，不得写成已发布；
7. 不提前实现下一分片。

## 10. 规划基线

### 2026-08-05：规划冻结

- 审计 ROADMAP §6.3/6.9/6.10/6.11、RFC-003/004/013、当前 extension activation、
  RuntimeController/BindingStore/ContextBridge、VSIX 与 release script；
- 决定 multi-root 使用一 root 一 Runtime + extension registry，不扩张单 Runtime sandbox；
- 决定 remote identity 同时绑定 editor URI 与 Runtime path，不放宽为任意 URI；
- 决定 remote Extension 固定运行于 Workspace Host，stdio ACP 不改成本地端口；
- 决定 external/managed/bundled 三种 binary 来源共用 compatibility 和 verifier；
- 现有 `SHA256SUMS.sig` 明确仅为占位，V3 T3 前不能用于下载/更新信任；
- 规划 T0–T6 与 M5-G021–G027；规划冻结时均未开始实现。

### 2026-08-05：T0 完成

- 新增共享 `WorkspaceIdentity v1`：`root_id=SHA-256(canonical editor URI)`，同时绑定
  `editor_uri`、绝对 `runtime_path` 与可选 `remote_name`；拒绝未知 scheme、query、
  fragment、非 canonical percent escape、非法 authority、相对 Runtime path 和 forged
  root ID；
- CLI 新增 `--workspace-uri`、`--workspace-root-id`、`--remote-name`，先将 Runtime
  workspace 绝对化；ACP launch option、initialize result/request 与每个 `turn.start`
  三处交叉绑定 identity，turn 不能覆盖 Runtime 绑定；
- Context resolver 对 local/`vscode-remote` URI 先验证 scheme/authority/root-relative
  identity，再将 URI path 映射到绑定的 Runtime path，继续走 sandbox symlink、digest、
  UTF-8、UTF-16 range 与大小复验；只删除 `file:` 检查仍然不被允许；
- `StartTurnPayload.workspace_identity` 进入共享 Go/JSON Schema/生成 TypeScript；HTTP
  start body 和 ACP Operation 使用同一字段。双 Host contract 的 editor context 场景改用
  remote-style URI，ACP/HTTP 各 12 个场景均通过；
- 新增唯一源 `internal/compatibility/compatibility.json`，声明 extension/binary range、
  ACP v1–v2、operation schema、required methods/features、五个 target 与四个 channel；
  Go embed 与生成 TypeScript/packaged JSON 均有 unknown-field、重复项和版本漂移门禁；
- `version --json` 增加 OS/arch、ACP min/max 和 operation schema；ACP `serverInfo.version`
  使用真实 build version。生产扩展拒绝 dev、错 target、版本越界和协议不相交 binary，
  Test/Development mode 才允许 `dev`；
- `BindingStore` 冻结 v2 root-keyed map，binding 保存 root ID、workspace URI/path、
  session/thread/cursor；v1 单值可读迁移，cursor 更新只修改目标 root。Registry 和多 root
  UI 仍留给 T1；
- 新增 `make vscode-compatibility`；Node 77 项、真 stdio 77/77、安全 11/11、
  true binary ACP interop、固定 VS Code 1.96.4 Electron empty/workspace/native、
  protocol/schema、Go race 安全矩阵、vet 均通过；
- 性能保持：10k event 12.9 ms，Runtime ready p50 42.9 ms、p95 710.7 ms；
  VSIX 增加机读 compatibility 后为 8 个文件、38.63 KiB，allowlist 与实际安装通过；
- `go test ./...` 除既有 macOS Seatbelt benchmark 18/23 外其余包通过；
- M5-G024 关闭；M5-G022 只完成共享身份契约，真实 remote Extension Host E2E 未执行，
  继续保持 open。T1 尚未开始。

### 2026-08-05：T1 完成

- 新增 `WorkspaceRuntimeRegistry`，以 T0 `root_id` 管理最多 8 个 root；每个 root 独占
  `RuntimeController`、`ContextBridge`、Supervisor、session/thread/cursor binding、
  event namespace 和 `storageUri/runtime/<root_id>` data directory，不再有模块级单
  controller；
- Registry 监听 workspace folder add/remove。remove 先 detach 投影和 listener，再停止
  Runtime 并保留 durable data；同一 root 快速 re-add 时等待旧进程 stop 后才启动新进程，
  防止 orphan process 和同 data directory 双写；
- Chat 改为 root-keyed projector map，Webview header 提供显式 root selector；root 选择写入
  `workspaceState`，不会随 active editor 静默切换。modal/input/submitted approval identity
  使用 `root_id + request_id`；
- selection command 与 Diagnostic Code Action 通过 document URI 的
  `workspace.getWorkspaceFolder` 选择 root，并在 focus Chat 前显式切换 selector；
  status/restart 在多 root 时使用 root picker；
- Changes 与 Threads/Agents/Tasks/Jobs/Approvals/Usage Tree 均以 root 为第一层；
  projector/query/refresh pending set 分 root，所有后台 Tree 共用一个合并 flush timer，
  没有 per-root poll timer；
- Changes 的 file/decision command target 强制携带 root ID；Chat decision 再检查 exact
  Runtime approval/plan identity。diff provider URI 同时 namespace root ID 与 plan ID，
  两个 Runtime 铸出相同 plan/request ID 时也不会串缓存或提交到错误 Runtime；
- Electron 新增固定 VS Code 1.96.4 two-root workspace：两个真实 Runtime 同时 ready，
  每个根依次通过 selection、diagnostic、真实 edit-plan approval/deny 和 terminal；
  context receipt/root ID 均匹配文档所属 root。场景还执行 remove/re-add 并验证恢复 ready；
- Electron 的 empty/workspace/native 与新增 multi 场景通过；真 stdio Node 78/78，
  安全门禁 12/12，typed lint/schema/compatibility drift 均通过；
- 性能保持：10k event 12.8 ms，Runtime ready p50 38.7 ms、p95 687 ms；所有 Electron
  场景仍断言 activation `<100 ms`；
- VSIX allowlist 与实际安装通过：8 个文件、41.94 KiB；
- M5-G021 关闭。M5-G022/M5-G023 继续 open，真实 SSH/WSL/Container Workspace
  Extension Host 尚未执行；T2 尚未开始。

### 2026-08-05：T2 完成

- manifest 固定 `extensionKind=["workspace"]`；Controller 拒绝非 Workspace Extension
  Host，binary discovery、PATH/config、storage、cwd 和 child process 全部位于 Host 端；
- local 与标准 `vscode-remote:` URI 直接使用 canonical authority；remote Host 将远端 URI
  transform 为 Host-local `file:` 时，以 `remoteName + SHA-256(remote hostname)` 合成
  稳定且跨主机隔离的 editor authority。identity、selection、diagnostic 与 context
  receipt 共用该 canonicalizer；
- remote authority 必须匹配 `vscode.env.remoteName`，storage 必须是 Host-local
  `file:`，external binary 的 OS/arch 必须匹配 `process.platform/process.arch`；
- 新增目标 VS Code 1.123.0、官方 Remote SSH 0.124.0、Colima Linux arm64 gate。测试安装
  唯一版本的临时 CodeHelper/test-runner
  VSIX，在远端 Workspace Host 内执行 selection、Diagnostic Code Action、multi-file
  Changes、approve/deny、Runtime restart/recovery，并复验 Linux/arm64 target、remote
  identity 与 Extension Host/Runtime PID；
- 新增目标 VS Code 1.123.0、官方 Dev Containers 0.466.0 gate；在 Ubuntu 24.04 arm64
  容器 Workspace Host 内执行同一完整 native flow，并复验 `remoteName=dev-container`、
  Linux/arm64 target、container storage 与进程隔离；
- local 真 stdio 83/83、安全门禁 13/13、typed lint/schema/compatibility drift 和 Go
  protocol/app/ACP/HTTP 定向测试通过；本地 Electron empty/workspace/native/multi 与生产
  VSIX 安装通过（8 files，43.17 KiB）；
- WSL2 按当前执行范围不做，不属于 T2 退出条件；M5-G022/M5-G023 关闭，T2 完成。

### 2026-08-05：T3 完成

- 新增 strict release manifest：签名覆盖移除 `signature` 后的递归 key-sorted canonical
  JSON；schema、未知字段、sequence、target、ACP/schema/features、extension range、
  SBOM/provenance digest 和 revocation 全部 fail-closed；
- manifest 使用 Ed25519 和 VSIX 内置 public trust root。key rotation statement 必须由
  当前可信 key 签发，按顺序构建临时 keyring；私钥仅由 release script 从仓库外 CI/KMS
  路径读取，不进入仓库、日志或 VSIX；
- `binarySource=auto|external|managed|bundled` 已接入，`auto` 严格按有效 bundled、
  有效 managed、external 选择。bundled 与 managed 共用 signed manifest、target、
  bytes、SHA-256、`version --json` 和 compatibility verifier；
- managed store 位于 Workspace Extension Host 的 `globalStorageUri`，使用临时文件、
  内容寻址 artifact、最小 executable 权限、跨窗口锁和同目录原子 `state.json`
  rename；state 记录 monotonic sequence、pending、last-known-good、revocation 与无敏感
  路径的有界 receipt；
- 固定 HTTPS origin downloader 限制 redirect、30 秒 timeout、manifest/artifact 大小；
  Production activation 不联网，首次后台检查延迟 30 秒且至少间隔 24 小时。
  `off|notify|auto` 与 channel 是 application scope，workspace 不能替换 feed、公钥或策略；
- Runtime 只有完成 ACP initialize 与 session attach 才将 pending 标记 healthy；失败停止
  进程并回滚。revoked active 不启动，revoked last-known-good 不作为回滚目标；
- fault injection 覆盖签名篡改、未知 key/字段、非 HTTPS、错 target、revocation、合法/
  非法 key rotation、sequence replay、checksum corruption、并发安装、pre-pointer crash、
  pending healthy 与失败回滚。typed lint、真实 Runtime stdio 91/91、安全 13/13、性能、
  local Electron、官方 Remote SSH 与 Dev Containers 门禁通过；生产 universal VSIX
  allowlist/安装通过（9 files，51.62 KiB，新增文件仅 public trust root）；
  M5-G025 关闭，T3 完成。

### 2026-08-05：T4 完成

- 补齐 Marketplace/Open VSX 生产元数据、128x128 PNG icon、CHANGELOG、SECURITY、
  PRIVACY 和 SUPPORT；源码保留 `private=true` 防误发布，仅 release staging 删除；
- `make vscode-release-dry-run` 构建五个平台 raw binary，以 T3 canonical manifest
  签名，再产出 universal thin VSIX 和 `linux-x64`、`linux-arm64`、`darwin-x64`、
  `darwin-arm64`、`win32-x64` 五个 target VSIX。每个 target 包带正确
  `TargetPlatform`，且恰好包含一个匹配 target 的 binary；
- 发布矩阵先验证 signed manifest、bytes/SHA-256、revocation 和 trust root，再生成
  extension CycloneDX SBOM、逐 binary SLSA/in-toto provenance 与总
  `release-provenance.json`；总 provenance 绑定 extension、compatibility、binary
  manifest、trust roots、channel mapping、SBOM 和六个 VSIX digest；
- Marketplace/Open VSX 生成无 token 的 CLI plan；企业/离线布局自带六个 VSIX、SBOM、
  provenance 和独立可验证 SHA256SUMS。四渠道 dry-run 均明确
  `dry_run=true, uploaded=false`，不把本地演练写成已发布；
- 本地 dry-run 只使用临时 Ed25519 key；生产模式必须显式提供仓库外 private key、
  matching public roots 和 key ID，key 不匹配时在构建前失败；
- 五个 bundled binary 的 ELF/Mach-O/PE target header 全部通过静态审计；当前
  darwin-arm64 host 上 universal 与 target VSIX 均完成 install/list/uninstall，包内
  extension + signed manifest + binary 完成 ACP handshake/session attach 和 restart
  recovery。其它平台完整 native flow 保留给 T5 runner matrix；
- typed lint/schema/compatibility drift、安全 13/13、性能、普通 universal VSIX
  allowlist/安装（14 files，61.01 KiB）和完整 release dry-run 通过；M5-G026 关闭，
  T4 完成。

### 2026-08-05：T5 完成

- 冻结 schema v1 machine-readable evidence 与 aggregate report；缺任一 required job
  默认非零退出，最终 15/15 required evidence 通过；
- local darwin-arm64 external single/multi、bundled install/handshake/restart、signed
  update、security、performance 和 distribution gate 通过；官方 darwin-x64 VS Code
  1.96.4 Electron Host 在 Rosetta 下用 darwin/amd64 Runtime 完成 native single/multi；
- 官方 Remote SSH Linux arm64 覆盖 external/managed、single/multi、selection、
  Code Action、Changes、approve/deny、root remove/re-add；真实终止 Workspace
  Extension Host 后重新 attach，session/thread identity 保持不变；
- Dev Containers Linux arm64/amd64 覆盖 managed single/multi 和 fresh container
  attach；amd64 在 arm64 Colima 上经 QEMU 执行。容器内 Runtime restart/replay 通过，
  未把 headless client 无法自动恢复的 container disconnect 写成已验证；
- 真实 stdio suite 93/93 通过，包含 Runtime restart、cursor replay、untrusted
  read-only、approval round trip 与 workspace drift rejection；安全 13/13，
  10k event projector 13.0 ms；
- Windows x64/WSL2 没有动态 runner，只保留 T4 PE/VSIX 静态 gate，不虚报为动态 E2E。
  M5-G027 关闭，T5 完成；下一分片为 T6。

### 2026-08-05：T6 完成

- 对 V3 新增面完成三轮 source-to-sink 安全审查；没有发现置信度不低于 0.8、可证明
  利用的新增漏洞。自动安全门禁扩展为 14 项，覆盖 Webview、Workspace Trust、
  root/plan binding、Remote Host、update cache/concurrency/disk budget；
- signed manifest 增加 ETag/`If-None-Match` 重验证缓存；`304` 只复用有界缓存且重新
  验签。更新检查 single-flight，staging 前检查两倍 artifact 加 16 MiB 可用空间；
- release provenance 绑定 commit、source tree fingerprint 与 clean/dirty 状态；
  production release 拒绝 dirty tree。public trust roots 与 signed manifest 进入
  `provenance/`，企业/离线布局可独立复验；
- 新增 `make vscode-rc`，串行聚合 TypeScript/lint、97 项 extension/真实 Runtime、
  14 项安全、性能、Electron、update、distribution 与 15/15 matrix；RC verifier
  再执行 npm audit、secret scan、VSIX allowlist、SBOM/provenance/signature/checksum；
- 兼容/支持报告和 [release runbook](../RELEASE-VSCODE.zh-CN.md) 已冻结。当前工作树
  与临时 key 只产出 `validated-dry-run, publishable=false, uploaded=false`，没有
  渠道凭据，也没有虚报 Marketplace/Open VSX 上传；
- Windows x64/WSL2 仍无动态 runner，Dev Container disconnect re-attach 仍不宣称。
  RFC-014 T0–T6 与 M5 V3 Production 完成。
