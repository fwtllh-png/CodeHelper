# Security Governance 与 Sandbox 架构升级方案

> 状态：SG0 `baseline_frozen`；SG1-SG7 `accepted`。
>
> SG0 基线：
> [`security-governance-sg0-baseline.json`](../security-governance-sg0-baseline.json)。
>
> 参考实现：Codex commit
> `3bbf1fe75701c97fb190e0867002ba2d9dbda5db`。
>
> 范围：Policy、Constitution、Approval、持久授权、Guard、Journal、Egress、
> Process Sandbox、平台能力声明、执行收据与安全验证。

## 1. 摘要

CodeHelper 已经具备统一 Guard、资源规范化、审批指纹、编辑计划重校验、
Workspace Journal、强 Sandbox 能力探测、Typed Outcome、Teardown Receipt 和攻击
语料。这些能力形成了比单纯“命令审批 + 进程隔离”更完整的治理基础。

升级前的核心问题不是缺少控制点，而是授权事实分散在 Mode、Permission、Policy Rule、
Constitution、Tool Descriptor、Sandbox Requirement、进程布尔参数和 Egress Gate
之间。授权决策与操作系统实际执行的权限没有一个共同、不可变、可验证的权威对象。
SG0 基线据此冻结了四类具体风险：

1. `.codehelper` 下的持久授权和治理文件仍可作为普通 Workspace 文件被模型修改；
2. `exec_command` 获批后默认拥有宽泛网络访问，工具层 Egress Gate 无法约束子进程；
3. 真实进程 Sandbox denial 没有 Typed Signal producer，最小增权流程无法闭环；
4. Guard 的 `sandbox:none` 重试与 Process 层 backend-only 约束互相冲突。

本升级保留 CodeHelper 已有 Guard 和证据体系，引入统一权限编译、受限增权、托管网络
出口和平台级纵深防御：

```text
Prepared Invocation
        |
        v
Authority Compiler
  mode + tool + policy + constitution + grant + platform
        |
        v
Effective Permission Profile
        |
        +----> Approval Amendment ----+
        |                             |
        v                             v
Sandbox Planner ------------> Effective Profile vN
        |
        v
Platform Enforcement
        |
        v
Typed Outcome + Enforcement Receipt
```

## 2. 目标

升级必须实现：

1. 每次 consequential execution 只有一个 Effective Permission Profile；
2. Policy、Approval、Sandbox 和 Receipt 使用同一权限语义和同一 digest；
3. 模型不能通过普通工具写入治理控制面；
4. 权限提升默认只增加必要路径、网络目标或进程能力；
5. `suggest` 和 `auto` 不允许直接降级到 unsandboxed；
6. 受限进程默认不能直接访问外部网络；
7. macOS、Linux 的实际 enforcement 可由 Receipt 证明；
8. 所有 denial、增权、重试和 teardown 都有 Typed Outcome；
9. 保持现有本地执行边界，不引入远端执行；
10. 保持 Architecture Ratchet 不低于当前 `67/67`。

## 3. 非目标

本升级不：

- 引入容器平台、远端 Worker 或远端 Sandbox；
- 将安全业务逻辑移动到 CLI、TUI、VS Code 或 ACP Host；
- 绕过 Guard、Constitution、Journal、Claims 或 Typed Execution；
- 以“用户批准”为理由自动移除全部 Sandbox；
- 使用 TLS 中间人作为第一版网络治理前提；
- 为未发布状态增加长期兼容迁移；
- 把 Codex 的所有配置层和远端执行复杂度复制到 CodeHelper。

## 4. 威胁模型

### 4.1 攻击者可控输入

- 用户 Prompt、仓库文本、依赖输出和工具输出；
- Tool 名称、参数、Shell 文本、路径、Glob、URL、Host、Port；
- Workspace 内的符号链接、硬链接、大小写别名和竞态替换；
- Plugin/MCP 返回值和延迟注册工具；
- 子进程 stdout、stderr、退出码和信号行为；
- Sandbox denial 文本、网络错误和恢复请求；
- Workspace 中可能伪装成治理配置的文件。

### 4.2 必须保护的资产

- 用户主目录、Workspace 外文件、凭据和系统配置；
- `.codehelper` Runtime 状态、审批、Constitution 和 Journal；
- Git 元数据、Agent 元数据及恢复证据；
- 内网服务、云元数据端点和本地敏感服务；
- Session、PID、PTY、文件描述符和网络连接；
- Approval 意图与实际执行参数的一致性；
- 安全事件、Receipt 和审计链的完整性。

### 4.3 信任边界

可信：

- Host 发起的显式审批响应；
- Runtime 构造层注入的受控依赖；
- Guard 内部生成并校验的 Prepared Invocation；
- 平台 Probe 证明可用的强 Sandbox backend。

不可信：

- 模型和 Prompt；
- Workspace 全部内容，包括仓库自带 `.codehelper`；
- Tool 参数、Shell 输出、Plugin/MCP；
- 仅由 stderr 文本推断出的安全事实；
- 未绑定 profile digest 的历史审批和执行收据。

## 5. CodeHelper 当前机制

当前主链为：

```text
Registry Catalog Binding
 -> Guard Prepare
 -> 参数校验与 Resource Resolution
 -> Policy / Constitution / Permission
 -> Approval / Permission Hook
 -> Execution Admission / Claims
 -> Journal
 -> Sandbox Attempt
 -> Egress Retry
 -> Typed Outcome / Receipt
```

值得保留的能力：

| 能力 | 当前性质 |
| --- | --- |
| Catalog authority | Tool source、revision、authority 在执行前重校验 |
| Canonical Resource | Policy 和 Claims 使用规范化资源 |
| Approval fingerprint | 绑定参数、资源、Scope 和过期时间 |
| Exact Edit Plan | 审批后重新生成并检查 stale state |
| Journal | Workspace 修改可恢复，且参与写入治理 |
| Strong Sandbox | backend 不可用时 fail closed |
| Platform Probe | macOS/Linux 能力和攻击语料可真实验证 |
| Typed Execution | Outcome、Security Signal、Attempt、Teardown 已类型化 |
| Claims | 冲突资源串行，独立资源可并发 |

### 5.1 macOS

macOS 使用 Seatbelt profile，限制文件读取和写入，并通过准备后的 Policy ID 与
Strength 防止未验证策略执行。当前网络是布尔开关，允许时没有 Host/Port 粒度。

### 5.2 Linux

Linux 使用 Bubblewrap 构造文件系统和 Namespace 视图，并使用 Landlock helper
收紧文件访问。当前未形成 `no_new_privs + seccomp` 的系统调用防线；网络允许时
使用共享网络 Namespace。

### 5.3 审批与增权

Approval 能精确绑定调用，持久 Grant 具备 TTL 和 Workspace/Tool/Resource 约束。
SG0 基线中，Sandbox 失败后的设计意图是申请批准并重试 `sandbox:none`，但真实 Process 路径
没有生产 Typed Sandbox Denial，且进程仍携带必须由 backend 执行的约束。

## 6. Codex 参考实现

Codex 的可借鉴优势：

1. `PermissionProfile` 同时表达文件系统、网络和 enforcement mode；
2. `AdditionalPermissionProfile` 允许一次命令只增加特定路径或网络能力；
3. denied read 在权限提升中继续保留，避免增权覆盖硬拒绝；
4. macOS 可将进程网络限制到本地 managed proxy；
5. Linux 组合 Bubblewrap、`no_new_privs`、seccomp 和网络 Namespace；
6. Exec Policy 基于命令段和可执行文件分类，拒绝生成危险宽前缀；
7. `.git`、`.agents`、`.codex` 等元数据路径默认受保护；
8. managed requirements 能限制用户和项目配置的最大权限。

不直接复制的部分：

- Codex 某些显式 Exec Allow 可以绕过 Sandbox，不符合 CodeHelper 的纵深防御原则；
- stderr 启发式 denial 分类只能作为兼容补充，不能成为 Typed 权威；
- 通用序列化审批 Key 不替代 CodeHelper 的 typed grant、TTL 和资源指纹；
- CodeHelper 保留 Journal、Edit Plan、Claims 和 Teardown Receipt；
- 不引入 Codex 的远端执行、外部 Sandbox 和多运行环境复杂度。

## 7. 差距与优先级

| 级别 | 差距 | 后果 | 目标阶段 |
| --- | --- | --- | --- |
| P0 | Workspace 控制面可由普通文件工具修改 | 下一 Session 权限持久化污染 | SG1 |
| P1 | 没有统一 Effective Permission Profile | 决策与 enforcement 漂移 | SG2 |
| P1 | Sandbox denial 与增权流程不闭环 | 无法安全完成最小权限重试 | SG3 |
| P1 | 子进程网络是宽泛布尔授权 | SSRF、内网访问和数据外传 | SG4 |
| P1 | Linux 缺少 seccomp 层 | ptrace、process_vm、io_uring 攻击面 | SG5 |
| P2 | Shell Prefix 规则解析偏字符串化 | 规则过宽或解释差异 | SG6 |
| P2 | Receipt 不绑定实际权限摘要 | 无法证明批准与执行一致 | SG7 |

## 8. 目标权限模型

### 8.1 Effective Permission Profile

```go
type EffectivePermissionProfile struct {
    Revision    uint64
    Filesystem  FilesystemAuthority
    Network     NetworkAuthority
    Process     ProcessAuthority
    Enforcement EnforcementMode
    Provenance  []AuthoritySource
    Digest      string
}
```

`Digest` 必须由 canonical encoding 生成，覆盖全部权限和来源版本。审批、Sandbox
Plan、Attempt Receipt 必须携带同一 digest。

### 8.2 权限组合规则

基础权限使用交集：

```text
mode ceiling
  ∩ tool descriptor
  ∩ managed requirements
  ∩ constitution
  ∩ repository policy
  ∩ platform capability
```

显式 Grant 只能在 ceiling 内增加权限：

```text
effective(vN+1) =
  effective(vN)
  + approved additional permission
  - all immutable denies
```

规则：

- `deny` 永远不能被普通 Approval 覆盖；
- Workspace 配置不能扩大 User/Managed ceiling；
- 增权不能隐式扩大到父目录、任意 Host 或全部系统调用；
- `unsandboxed` 是独立的高危能力，不是 Sandbox denial 的默认恢复方式；
- Profile 变化必须增加 revision 并生成新 digest。

### 8.3 控制面分类

```text
Runtime-owned:
  permission store, approval ledger, journal, state database, receipts

Admin-authored:
  user constitution, managed requirements

Repository-advisory:
  repository policy, repository constitution additions

Workload:
  ordinary workspace files
```

Runtime-owned 和 Admin-authored 路径不得通过普通文件工具写入。Repository-advisory
只能增加 deny/ask，不能扩大上层权限。

## 9. 分阶段实施

### SG0：基线与攻击语料

状态：`baseline_frozen`。

交付：

- `security-governance-sg0-baseline.json`；
- `scripts/securitygovernancebaseline`；
- 5 个版本化攻击场景；
- 15 项单调安全能力 Ratchet；
- `make security-governance-sg0`。

退出条件：

- 当前强项和缺口可重复测量；
- 已有 `true` 控制不能回退；
- 攻击 Case ID 和数量不能静默漂移；
- focused security、Process、Architecture tests 通过。

### SG1：控制面完整性

状态：`accepted`。

工作：

- 引入 `ControlPlanePathClassifier`；
- 默认保护 `.codehelper`、`.git` 和 Agent 元数据；
- 持久授权移出 Workspace，绑定 canonical Workspace Identity；
- Runtime-owned 写入改为专用 Operation、CAS 和原子持久化；
- Repository policy 只允许收紧，不允许扩大权限；
- 为旧权限文件提供一次性显式导入，不自动信任。

退出条件：

- `SG-ATTACK-001` 变为 `blocked`；
- 普通 Tool 无法创建、覆盖、链接或重命名控制面文件；
- symlink、hardlink、case-fold、TOCTOU 测试通过；
- `control_plane_protected` 和 `authority_store_outside_workspace` 为 true。

已交付：

- 新增 Runtime-owned `ControlPlanePathClassifier`，默认保护 `.codehelper`、
  `.codehelper-worktree`、`.git`、`.agents` 与 `.codex`；
- Guard 在 Policy、Permission Hook 和 Approval 前检查所有路径写资源，`bypass`
  也不能覆盖 `control_plane_protected`；
- Sandbox 对 exact write path 再次执行控制面分类，并继续拒绝 symlink、hardlink、
  case-fold 别名和跨设备路径；
- 持久 Grant 迁移到
  `state.data_dir/security/workspaces/<workspace-identity>/permissions.toml`；
- Workspace Identity 绑定 canonical path、device 和 inode，权限文件使用
  `0700` 目录、`0600` 临时文件、`fsync + atomic rename`；
- `state.data_dir` 位于 Workspace 内时 fail closed，不读取旧
  `.codehelper/permissions.toml`；
- Repository rules 只能 `ask`、`deny` 或 `hold`，不能通过 `allow` 扩大权限；
- Plugin 与 Task verification 的 Workspace 权限声明收紧为 read-only；
- SG0 Controls 从 `6/15` 提升到 `8/15`，`SG-ATTACK-001` 已变为 `blocked`。

阶段 Gate：

```bash
make security-governance-sg1
```

### SG2：统一权限编译

状态：`accepted`。

工作：

- 实现 `AuthorityCompiler` 和 `EffectivePermissionProfile`；
- 将现有 Mode、Rule、Grant、Sandbox flags 投影为统一模型；
- Sandbox backend 只接收已验证 Profile；
- 删除独立、重复的权限布尔权威；
- Profile 使用 canonical digest 和 revision。

退出条件：

- 每个 Attempt 有且只有一个 Profile；
- 任意来源 deny 在组合后仍保留；
- 替换参数重新编译 Profile；
- `unified_permission_profile` 为 true。

已交付：

- 新增 `AuthorityCompiler` 与不可变 `EffectivePermissionProfile`，统一表达 Tool、
  Filesystem、Network、Process、Enforcement 和 Provenance；
- Profile 使用 canonical JSON、单调 revision 和 SHA-256 digest，任何字段变更都会
  使校验失败；
- Mode、Permission、Repository Rules、Grant、Tool Descriptor 和 Sandbox Policy
  均进入 Provenance；
- Guard 每个 Attempt 重新编译 Profile，参数替换后只执行新资源对应的 Profile；
- Profile 通过受控 Context 投影为 Process ceiling，执行器只能进一步收紧权限；
- Process 拒绝超出 Profile 的 Workspace、write path、network 或 enforcement 请求；
- macOS Seatbelt、Linux Bubblewrap 及 policy binding 必须回签相同 authority digest，
  否则进程不启动；
- 控制面 deny roots 始终进入 Profile，不能被授权或 Profile 合并覆盖；
- SG0 Controls 从 `8/15` 提升到 `9/15`，已知缺口从 7 降至 6。

阶段 Gate：

```bash
make security-governance-sg2
```

### SG3：Typed Denial 与受限增权

状态：`accepted`。

工作：

- Process/Sandbox 生产 Typed Sandbox Denial；
- denial 携带 backend、operation、resource 和 reason code；
- 引入 `AdditionalPermissionRequest`；
- 支持单路径 read/write、单 Host/Port、必要进程能力增权；
- 删除 `suggest/auto` 的直接 unsandbox fallback；
- `operate` 下的 bypass 使用独立 critical approval。

退出条件：

- `SG-ATTACK-003/004` 变为 `blocked`；
- denial 不依赖自由文本才能触发；
- 增权后 immutable deny 不变；
- `typed_sandbox_denial_producer` 和 `coherent_sandbox_escalation` 为 true。

已交付：

- 新增结构化 `sandbox.Denial` 与 `DenialError`，统一表达 backend、operation、
  resource、reason code、protocol 和 port；
- Process ceiling、Sandbox backend 验证及 Tool Outcome 直接生产或透传 Typed
  Denial，不再通过 stderr 或自由文本猜测拒绝原因；
- 新增绑定 base profile digest 的 `AdditionalPermissionRequest`，支持单路径
  read/write、单 Host/Port 和 Process capability；
- `Authority.Amend` 生成递增 revision 和新 digest，保留全部 immutable deny 与
  provenance，并拒绝跨 Profile、Workspace 外写入和控制面写入；
- Additional Permission 仅允许 `ApprovalOnce`，事件固定为
  `external.mutation / critical`，不进入 Session/Always Approval Cache；
- 每次调用最多执行一次 amendment；第二次 denial、不可结构化 denial 和
  non-amendable denial 均 fail closed；
- 重试继续使用原 Strong Sandbox，Seatbelt、Bubblewrap 与 Landlock 仅增加批准的
  read/write path，不存在 strong 到 none 的通用降级；
- SG0 Controls 从 `9/15` 提升到 `11/15`，已知缺口从 6 降至 4，
  `SG-ATTACK-003/004` 已变为 `blocked`。

阶段 Gate：

```bash
make security-governance-sg3
```

### SG4：Managed Process Egress

状态：`accepted`。

工作：

- 进程默认禁网；
- 引入 Runtime-owned 本地 CONNECT/SOCKS proxy；
- Sandbox 仅允许连接代理和必要本地 IPC；
- Policy 支持 Host、Port、Protocol、HTTP Method；
- 拒绝 loopback、link-local、私网和云元数据地址，除非显式授权；
- 处理 DNS rebinding、重定向和 CONNECT 目标复核；
- Process、Web、Provider、MCP 统一输出 Egress Receipt。

退出条件：

- `SG-ATTACK-002` 变为 `blocked`；
- 受限进程直连外网和内网成功率为 0；
- 允许 Host 可访问，未允许 Host fail closed；
- `restricted_process_egress` 和 `managed_network_proxy` 为 true。

已交付：

- 新增 Runtime-owned loopback HTTP/CONNECT proxy，生命周期与 Sandbox backend
  组合关闭，Process 退出后才停止代理；
- `exec_command` 新增结构化 `network_targets`，授权精确到 Host、Port、
  Protocol、HTTP Method 和 `allow_private`；
- Guard 将批准目标直接投影到 Session Egress Gate，未声明目标无法通过代理；
- Proxy 在每次连接前解析 DNS，拒绝 loopback、private、link-local、
  multicast 和 metadata 地址；拨号固定使用已检查 IP，消除二次解析；
- HTTP redirect 和 CONNECT 都重新执行目标检查，Host header 与 absolute URL
  不一致时 fail closed；
- macOS Seatbelt 仅开放 `localhost:<proxy-port>`，不开放通用
  `network-outbound`、DNS 或本地任意端口；
- Linux 在 namespace-to-proxy bridge 与 SG5 seccomp 尚未交付前保持进程全禁网，
  不宣称无法执行的 proxy-only capability；
- Process、Web、Provider 和 MCP 使用同一 `egress.Receipt` 事实结构，Gate 保留
  有界 256 条 allow/deny 审计；
- 真实 Seatbelt 攻击测试证明允许目标经代理可达、同一 upstream 直连失败；
- SG0 Controls 从 `11/15` 提升到 `13/15`，`SG-ATTACK-002` 已变为
  `blocked`，已知缺口只剩 SG5 与 SG7 两项。

阶段 Gate：

```bash
make security-governance-sg4
```

### SG5：平台纵深防御

状态：`accepted`。

工作：

- Linux 增加 `PR_SET_NO_NEW_PRIVS`；
- seccomp 拒绝 ptrace、process_vm、危险 clone 和 io_uring；
- 网络模式决定 socket/connect syscall 集；
- Landlock 作为文件系统纵深防御继续保留；
- macOS 网络 profile 仅开放代理端口；
- Windows strong sandbox 未实现前保持明确 unavailable。

退出条件：

- `SG-ATTACK-005` 变为 `blocked`；
- 文件、网络、进程、清理攻击语料在真实平台通过；
- capability claim 与真实 Probe 一致；
- `linux_syscall_filter` 为 true。

已交付：

- Linux Landlock helper request 新增强制 `syscall_policy`，只接受
  `restricted`、`proxy_routed` 或 `direct`；
- Helper 使用 `runtime.LockOSThread` 将 Landlock、`PR_SET_NO_NEW_PRIVS`、
  seccomp 与 `execve` 固定在同一 OS thread；
- seccomp 在 amd64/arm64 校验 Audit Architecture，架构不匹配直接
  `SECCOMP_RET_KILL_PROCESS`；
- 所有网络模式均拒绝 `ptrace`、`process_vm_readv/writev`、`clone3`、
  `unshare`、`setns` 与 `io_uring`；
- legacy `clone` 仅在包含 namespace 或 untraced flags 时拒绝，保留普通
  Process/Thread 创建；
- `restricted` 模式只允许 AF_UNIX socket/socketpair，并拒绝 connect、bind、
  listen、accept、send 与 socket option syscalls；
- `proxy_routed` 只允许 AF_INET/AF_INET6 socket 与 AF_UNIX socketpair；
  `direct` 仍保留 Process/IO hardening；
- Capability 新增 `syscall_isolation`，启动 Probe 要求 Landlock、
  no-new-privs 与 seccomp marker 同时存在；
- 新增 Linux capability 测试，真实验证 no-new-privs、ptrace、process_vm、
  io_uring、clone3、INET socket 均返回 EPERM，AF_UNIX socketpair 可用；
- Linux amd64/arm64 capability test binary 均可交叉编译；非支持架构 fail
  closed，不声明 Strong capability；
- 本地 Docker Linux/arm64 真实执行通过 Landlock strict policy 与 syscall
  policy 测试；
- SG0 Controls 从 `13/15` 提升到 `14/15`，`SG-ATTACK-005` 已变为
  `blocked`，仅剩 SG7 Attempt Receipt digest 缺口。

阶段 Gate：

```bash
make security-governance-sg5
```

### SG6：Policy 与治理来源

状态：`accepted`。

工作：

- 使用 Shell AST/argv-native 命令段；
- 解析 host executable 和解释器边界；
- 禁止 shell、解释器、`git`、`rm` 等危险宽前缀持久授权；
- 引入 Managed/User/Repository authority source；
- 定义固定优先级、最大权限和原子 reload；
- Policy revision 进入 Profile provenance。

退出条件：

- Prefix 不能跨 shell segment 或解释器 payload；
- 低权来源不能覆盖高权 deny；
- reload 中的执行绑定旧 revision 或完整切换到新 revision；
- malformed policy fail closed。

已交付：

- 引入 `mvdan.cc/sh/v3/syntax` Bash AST parser，替换字符级 Shell Segment
  扫描；
- 命令规则使用静态 argv Prefix，不再对规范化字符串执行
  `strings.HasPrefix`；
- AST 显式识别 pipeline、逻辑操作符、重定向、subshell、function、
  command substitution 与 dynamic word；
- `env KEY=value command` 会解析到真实 Host Executable；
- `sh/bash/dash/zsh/ksh/fish -c/-lc` payload 递归解析；Python、Node、
  Ruby、Perl、PHP payload 标记为 Opaque Interpreter Boundary；
- Allow Prefix 必须是单一静态 Command Segment，不能跨 Pipeline、
  Redirect、Subshell 或 Interpreter Payload；Deny/Hold 检查所有可解析段；
- 持久 Allow Prefix 禁止 Shell、Interpreter，以及单 Token `git`、`rm`
  宽前缀；
- Typed Shell Grant 使用 AST canonical identity，空白变化不改变 Grant，
  增加 Segment 或 Payload 会产生新 Grant；
- Policy Runtime 新增 Managed、User、Repository 三类 Authority Source：
  Managed 定义最大权限，Repository 只能 Ask/Deny/Hold，User Allow 不能覆盖
  任一高权 Deny/Ask；
- `ReloadSources` 在完整校验后原子发布 User/Repository snapshot 并递增
  Revision；失败 reload 不改变当前 snapshot；
- Guard 在授权前冻结 Policy snapshot，执行期间继续绑定旧 Revision；后续调用
  只会看到完整的新 Revision；
- Profile provenance 新增 Policy Revision 和 Managed/User/Repository 独立
  digest；
- SG0 Controls 维持 `14/15`，最后一项仍为 SG7 Attempt Receipt digest。

阶段 Gate：

```bash
make security-governance-sg6
```

### SG7：证据收敛与发布

已交付：

- 每个已编译权限的 Attempt Receipt 记录 Profile schema/revision/digest、
  capability/access、backend/strength/enforcement、Workspace/read/write/deny roots、
  network mode/targets/proxy 和 Process ceiling；
- Receipt provenance 记录 Policy revision、Managed/User/Repository source digest、
  实际命中的 Managed Grant，以及存在时的 Typed Grant Key；
- Typed Sandbox denial 与 Additional Permission amendment 形成同一条证据链：
  base digest、请求的最小资源、审批结果和 amended digest 相互绑定；
- ResultStore、Engine retained result 与持久化 JSON 使用深拷贝后的同一 Receipt，
  后续内存修改不能篡改已路由证据；
- 短 Tool Result 不再因未进入 CAS 而丢失 Receipt：
  `tool.result.execution` 将同一条类型化证据链投影到 Durable Runtime Event，
  History Reconstruction 明确忽略该字段，避免回灌 Model Context；
- authority compile/apply 等稀有失败路径也会先封口 terminal status/owner 并附加
  已形成的 Receipt；
- `network_targets` 的详细约束由 Typed Runtime Validator 执行，Model-visible
  Execution Schema 收敛到 `1033/1112` bytes，未放宽 EX5 Token Ratchet；
- SG0 已提升为 `15/15`，known gap 为 `0`，5 个 Attack Case 全部为
  `blocked`；迁移期通用 Sandbox fallback 不再存在。

阶段 Gate：

```bash
make security-governance-sg7
```

## 10. 迁移与兼容

- SG1 迁移旧 `.codehelper/permissions.toml` 时必须展示来源和目标 Scope；
- 未显式导入的 Workspace 权限文件按不可信数据处理；
- SG2 允许短期从旧字段单向生成 Profile，不允许 Profile 反向写回旧权威；
- SG3 完成时删除旧 sandbox escalation 分支，不保留双生产路径；
- SG4 初期可只支持 CONNECT 和 TCP，UDP 保持拒绝；
- 每阶段可回滚到前一阶段，但不能回滚已冻结的安全 Ratchet；
- 持久格式变化在公开发布前不增加无需求兼容迁移。

## 11. 验证矩阵

| 层次 | 必测内容 |
| --- | --- |
| Policy | allow、ask、deny、优先级、malformed、revision |
| Control Plane | direct、symlink、hardlink、rename、case-fold、TOCTOU |
| Approval | 参数替换、TTL、Scope、Profile digest、immutable deny |
| Sandbox | read/write、network、ptrace、process_vm、io_uring、cleanup |
| Egress | DNS、redirect、CONNECT、private IP、metadata、Host mismatch |
| Process | Pipe、PTY、foreground、detached、cancel、teardown |
| Receipt | Profile、backend、grant、denial、amendment、terminal owner |
| Concurrency | reload、approval wait、Claims、公平性、race |

标准命令：

```bash
make security-governance-sg0
make security-test
make sandbox-attack-test
go test -race ./internal/security/... ./internal/adapter/tool/guard/...
make docs-check
make book-check
make architecture-ratchet
git diff --check
```

## 12. 量化验收

| 指标 | 目标 |
| --- | --- |
| 控制面普通 Tool 写入成功率 | 0 |
| 受限进程非代理直连成功率 | 0 |
| Attempt 携带 Profile digest | 100% |
| Approval 与执行 Profile 一致率 | 100% |
| Immutable deny 保留率 | 100% |
| Typed Sandbox denial 覆盖率 | 100% |
| Session/PID/Span/Lease 残留 | 0 |
| SG0 Controls | 15/15 |
| SG0 Attack Cases | 5/5 blocked |
| Architecture Ratchet | 不低于 67/67 |

## 13. Ownership

| 关注点 | Owner |
| --- | --- |
| Tool Resource 与 Typed Outcome | `internal/adapter/tool` |
| Guard、Approval、Attempt | `internal/adapter/tool/guard` |
| Policy、Constitution、Permission Profile | `internal/security` |
| Sandbox backend | `internal/security/sandbox` |
| Managed Egress | `internal/security/egress` |
| Process 与 Session | `internal/platform/process` |
| 持久权限与 Receipt | `internal/persist` |
| 依赖构造 | `internal/runtime/app/wire` |
| Host 展示 | `internal/runtime/eventview`、`extensions/vscode` |

Host 只能提交 Operation 和展示结果；Provider、Tool、Sandbox、Approval、Policy 的业务
逻辑不得进入 Host。所有 consequential execution 仍必须通过 Guard。

## 14. 决策原则

- 安全审批表达用户意图，但不替代最小权限 enforcement；
- 能请求一个路径时，不请求整个 Workspace；
- 能请求一个 Host 时，不开放整个网络；
- 能用 Typed reason 时，不依赖 stderr 文本；
- 能证明实际 enforcement 时，不只记录配置意图；
- 平台能力不确定时 fail closed；
- 后续阶段不得通过放宽 SG0 baseline 获得通过。
