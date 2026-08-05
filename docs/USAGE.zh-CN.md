# CodeHelper 使用说明

> 配套：[README](../README.md)、[架构与设计](./ARCHITECTURE.zh-CN.md)

本文面向使用者与接入方：安装、配置、常用命令、权限模式、排障。

---

## 1. 安装与构建

### 1.1 环境

- Go **1.26** 或更高
- macOS / Linux 推荐（强沙箱：Seatbelt / bubblewrap / landlock）
- Windows：部分沙箱能力为 partial + fail-closed（不强行声称 strong）

### 1.2 从源码构建

```bash
cd CodeHelper
make build
./bin/codehelper version
./bin/codehelper help
```

跨平台编译检查：

```bash
make cross-build
```

发布打包：

```bash
VERSION=0.1.0 RELEASE_STAGE=experimental make package
# 产物默认在 dist/release/
```

---

## 2. 配置

### 2.1 优先级

```text
默认值  <  TOML 文件 (--config)  <  CODEHELPER_* 环境变量  <  命令行 flags
```

### 2.2 TOML 示例

```toml
[execution]
provider = "openai"
model = "gpt-4.1"
protocol = "openai_chat"
mode = "act"
workspace = "."
tools = true
max_output_tokens = 4096
max_steps = 8
timeout = "2m"
idle_timeout = "1m"

[execution.verify]
mode = "soft"             # off | soft | hard
scope = "diagnostics"     # diagnostics | repository | affected
on_failure = "fail"       # fail | revert
max_repair_steps = 1
timeout = "2m"
# command = "make verify" # 仅 scope = "repository" / "affected"；留空则自动探测

[context.index]
enabled = true            # 仓库符号索引；关掉则符号工具报不可用，文本搜索照常
max_file_bytes = 1048576  # 超过此大小的文件只记入文件表，不抽符号
max_files = 20000         # 索引文件数上限，超过按截断上报

[context.repo_map]
enabled = true            # 每次采样追加的仓库概览（目录/构建/入口/工作集符号轮廓）
max_bytes = 8192          # 单次请求内该段的字节上限，超出保留前缀并声明被截断
max_directories = 24      # 列出的目录数上限，其余汇总为"另有 N 个目录未列出"

[context.working_set]
enabled = true            # 每次采样追加的"已触达路径"清单（只给路径与来源，不注入内容）
max_entries = 16          # 运行时自行发现的路径条数上限；pin 的路径不占配额
max_bytes = 8192          # 单次请求内该段的字节上限

[context.evidence]
enabled = true            # 每次采样追加的"查到了什么 / 还没证明什么 / 白做了什么"
max_entries = 24          # 列出的事实条数上限，其余汇总为"另有 N 条未列出"
max_bytes = 4096          # 单次请求内该段的字节上限

[context.coding_policy]
enabled = true            # 稳定前缀里的一段工作方法（字节恒定，不影响 prefix cache）

[context.compact]
max_history_bytes = 262144 # history 超过这个字节数就压缩成一段结构化摘要
summary_max_bytes = 8192   # 一次摘要的渲染预算；不够时按优先级从后往前整段丢
max_digest_entries = 120   # 摘要末尾"逐条流水"的行数上限（新在前）

[credential]
kind = "env"
name = "OPENAI_API_KEY"

[state]
data_dir = "~/.codehelper/v1"
busy_timeout = "5s"
event_retention = 10000

[memory]
enabled = false
# path = "~/.codehelper/memory.md"

[telemetry]
log_level = "info"

[route]
lock = false               # true 时缺槽位的用途直接报错，不回落到 act

[route.plan]               # mode=plan 的采样走它；不配则回落到 act
provider = "openai"
model = "o4-mini"

# [route.subquery]         # sub_query 工具的模型
# [route.vision]           # 取代下面的 [vision]；配了它就等于开启 image_analyze

[vision]                   # 旧写法，仍然可用：会折叠成 [route.vision]
enabled = false
# provider = "openai"
# model = "gpt-4.1"

[web]
# search_backend = "..."
```

```bash
codehelper config check --config ./codehelper.toml
codehelper config show --config ./codehelper.toml
```

未知字段会直接报错（fail-closed）。

#### 按用途分模型（`[route.*]`）

`execution.provider/model` 就是主采样（`act`）用的模型。其他用途各有一个具名槽位，**没配就回落到 act**，所以不写 `[route]` 的会话行为与以前一样。语义见 [RFC-010](./rfc/RFC-010-model-routing.zh-CN.md)。

| 槽位 | 谁在用 | 接线情况 |
| --- | --- | --- |
| `act` | 主采样（`mode=act` / `operate`） | 即 `execution.provider/model`，不写成槽位 |
| `route.plan` | `mode=plan` 的采样 | 已接线 |
| `route.subquery` | `sub_query` 工具 | 已接线 |
| `route.vision` | `image_analyze` 工具 | 已接线；取代 `[vision]` |
| `route.summary` / `route.judge` | 模型叙事摘要 / 模型 verify | **还不存在，配了会报错**，不假装支持 |

几点行为：

- 槽位必须同时给 `provider` 与 `model`，只给一个在加载时报错——半个槽位回落到 act 会看起来像生效了；
- **`[route.vision]` 配的模型必须标了 `vision` 能力**（catalog 里目前是 `gpt-4.1`、`claude-sonnet`、`gpt-4o`、`gemini-2.0-flash`）。配了一个纯文本模型会在会话启动时报错，而不是等到第一次 `image_analyze` 才被 provider 打回；
- 整个 `[route]` 段与 `execution.provider` 同级别：只从可信配置（`--config` 指向的文件）读，项目内的未受信配置改不动它；槽位没有 flag，因为六个用途的 provider/model 会把每个命令的 help 挤满，而可复现的运行本来就该把它写在文件里；
- `lock = true`（或 `codehelper exec --lock-route`）关掉回落：缺槽位的用途在采样前报错。想要一次可复现的运行就打开它；
- 给不支持 reasoning 的模型配 `reasoning_effort` 会在采样前报错（catalog 的 `reasoning` 位），而不是变成一次 provider 400；
- 槽位只写 provider 与 model，因此只能落在内置 catalog 上。`--base-url` 会话自带的元数据只描述主模型一个，这类会话拒绝第二个模型；
- 收据的 `routes` 字段会说出这个 turn 每个用途实际用了谁，`codehelper usage` / `observe` 的成本按事件自带的 provider/model 归属；
- **工具自己发起的采样也记在发起它的 turn 名下。** `image_analyze` 与 `sub_query` 会各占一个 sample 号，token 进 turn 总量、钱按各自模型的价目算、`model_call` span 挂在那次工具调用下面。所以一个看了图的 turn，它的 token 数会明显高于它自己那几次请求——那是真的花掉了。对应 usage 事件无法写入 durable event/projection 时，该 turn 会失败，不会留下“成功 receipt 含 token、usage 查询却没有”的分裂结果。注意 usage 行按 provider/model 归属而不带 purpose：如果 `route.vision` 指向的模型与 act 相同，rollup 里这两类调用分不开（金额一样，只是分不清哪笔是看图）。

#### Verify Gate（`[execution.verify]`）

改过文件的 turn 在完成前先跑一次验证；只读 turn 不受影响。语义见 [RFC-002](./rfc/RFC-002-verify-gate.zh-CN.md)。

| 字段 | 取值 | 说明 |
| --- | --- | --- |
| `mode` | `off` / `soft`（默认）/ `hard` | `soft` 记录结论并让模型修，验证失败本身不改变 turn 终态；`hard` 在修复配额耗尽后按 `on_failure` 处置 |
| `scope` | `diagnostics`（默认）/ `repository` / `affected` | `diagnostics` 复用本 turn 的 post-edit 诊断，不额外起进程；`repository` 跑仓库的验证命令；`affected` 只跑改动能影响到的测试 |
| `on_failure` | `fail`（默认）/ `revert` | `fail` → `turn.failed`，`exec` 退出码非零；`revert` → turn 正常完成但改动被撤销。**两者的文件效果相同**，区别只在终态 |
| `max_repair_steps` | 0–8，默认 1 | 验证失败后允许模型自我修复的轮数；这些轮不占用 `max_steps` |
| `timeout` | Go duration，默认 `2m` | 单次验证的超时 |
| `command` | 命令字符串 | `scope = "repository"` 时留空按 go.mod / Cargo.toml / package.json / pyproject.toml 探测，兜底 `make verify`；`scope = "affected"` 时支持 `{paths}`（改动路径）与 `{packages}`（Go 包模式）占位 |

`scope = "affected"` 的映射规则（依赖 `[context.index]`）：

| 改动文件 | 跑什么 |
| --- | --- |
| Go | 改动文件所在包：`go test ./dir/...`（多个包合成一条命令） |
| Python | 索引映射出的测试文件：`python3 -m pytest <files>` |
| 其他语言 | 映射不出来。全都映射不出来 → 判 `unavailable` 并列出路径；部分映射不出来 → 跑能跑的，结论里点名剩下的 |

配了 `command` 就以它为准（占位符照样展开），适合规则表表达不了的自建测试入口。

几点必须知道的行为：

- 验证命令**没有**验证结论时（工作区没有可跑的验证）判 `unavailable`，不算失败，不阻断 turn；
- `affected` 映射不出结论时**不判过**，而是报 `unavailable` 并说明原因；索引不可用时同理；
- `on_failure = "ask"` 尚未实现，配置它会在加载时直接报错，而不是静默降级；
- 修复轮有独立步数配额，但**没有**独立 token 预算，仍受 `[execution]` 的 budget 约束；
- 一次成功写入会作废该文件的读指纹，所以修复轮里模型改同一文件会先重新 `file_read`。

#### 上下文尾块（`[context.repo_map]` / `[context.working_set]` / `[context.evidence]`）

每次采样都会在 history **之后**追加一小段系统上下文，语义见 [RFC-001 §10 / §11](./rfc/RFC-001-repo-context.zh-CN.md)：

- **`repo_map`**：仓库有多少文件与声明、根目录的构建文件、可能的入口、按目录汇总的代码分布，以及当前工作集文件的声明轮廓。目的是让"auth 代码在哪"这类问题不必花一次工具调用。
- **`working_set_ledger`**：本会话已经读过 / 改过 / 报过诊断 / 验证过 / 被 pin 的路径，带来源与最后触达的 turn。**只有路径，没有内容**——内容让模型自己 `file_read`，它读过的内容本来就在 history 里。
- **`evidence`**：三小节，按"最该被读到的排最前"排：**白做的**（同参数重复搜索、同内容重读、发出去没人取的结果 handle）、**还没证明的**（改了没验证 / 改了没读过 / 诊断未清）、**查到的事实**（哪个路径是定义、引用、测试还是配置，各带产出它的工具与 turn）。

几点行为：

- 尾块**不进 history**，因此不影响 `--resume` 与 compact 的重放；放在最后是为了不打穿 provider 的 prefix cache。
- 仓库地图**每 turn 取一次索引快照**，工作集与证据**每次采样重建**。所以同一 turn 内刚读的文件立刻出现在工作集里，但它的符号轮廓要等下一 turn。
- 索引不可用（关闭 / degraded）时 `repo_map` 降级为一行原因说明，并提示"没列出不等于不存在"；工作集与证据不依赖索引，照常输出。任何情况都不会因此让 turn 失败。
- 超预算时保留前缀并追加一行"本段被预算截断"，字节数与截断原因进 `turn.receipt` 的 `context_sections`；TUI 里 `/context` 打印最近一次的摘要行。
- 账本**不持久化**：`--resume` 之后从空开始重建。
- 证据段为空（会话刚开始、什么都没查也没改）时**不产出这一段**。

#### 工作方法（`[context.coding_policy]`）

稳定前缀里的一小段（≤700 字节）祈使句，告诉模型这个 runtime 期望的工作顺序：先读仓库约定与构建文件、把命中分成定义/引用/测试/配置、改前先读、改后先验受影响范围再扩大、不要重复已经做过的搜索与读取。语义见 [RFC-001 §11](./rfc/RFC-001-repo-context.zh-CN.md)。

- 其中两条**由运行时强制**而不是靠模型自觉，正文里也这么写：改前先读由工具直接拒绝（错误消息会说要重读什么），未验证的改动会经 `evidence` 段回灌给模型。
- 其余是**软的**：重复搜索照常执行，只是下一次采样里会挨一句提醒。
- 只在会话启用工具时注册——没有工具的会话读一份工具使用方法是纯浪费。
- 正文是常量，因此这一段**不影响 prefix cache**；关掉它只省几百字节，通常没有必要。

#### 长会话压缩（`[context.compact]`）

history 超过 `max_history_bytes` 时，最早的若干 turn 会被替换成一段结构化摘要，语义见 [RFC-009](./rfc/RFC-009-context-lifecycle.zh-CN.md)。摘要不是转录，而是六段 + 一段流水，按"丢了最贵的排最前"排列：

```
目标 → 待办（只列未完成的 + 已完成 N 项） → 已经失败过的尝试
     → 改动（每条注明有没有被验证、有没有先读过、诊断是否还红）
     → 关键路径 → 查到的事实 → 被移除消息的逐条流水（新在前）
```

几点必须知道的行为：

- 摘要**全部从运行时账本机械派生，不调模型**。所以压缩不产生额外请求、不花 token，也不会出现"我已经验证过了"这种没人观测到的断言。
- 预算不够时**整段丢**而不是切一半（半份改动清单会被读成完整的），流水段例外——它从最旧的行开始丢。无论多挤，摘要都会保留外壳并写明"本段被预算截断"。
- 切割**按 turn 组原子进行**：一个 turn 的消息不会被切开，因此一次压缩至少需要两个 turn，单个 turn 内 history 再大也不会触发。
- 上一次的摘要在下一次压缩时被**整块结转**（渲染成 `Earlier summary:`），不会被当成普通消息压成一行。长会话压很多次也不会把最早的目标丢掉。
- 账本**不持久化**：`--resume` 之后目标/待办/失败/改动/事实从空重建，只有随消息文本活下来的叙事还在。
- `/context` 会打印 `history X/Y 字节` 与已压缩次数；`turn.compaction` 事件报出这次实际带出了哪几段。
- 想在小窗口模型上更早压缩就调低 `max_history_bytes`；想让摘要更完整就调高 `summary_max_bytes`（未显式配置时按阈值的四分之一推导，上限 8 KiB）。

### 2.3 常用环境变量

| 变量 | 含义 |
| --- | --- |
| `CODEHELPER_PROVIDER` | Provider id（如 `openai`） |
| `CODEHELPER_MODEL` | 模型 id |
| `CODEHELPER_PROTOCOL` | `openai_chat` / `openai_responses` / `anthropic` 等 |
| `CODEHELPER_MODE` | `plan` / `act` / `operate` |
| `CODEHELPER_WORKSPACE` | 工作区路径 |
| `CODEHELPER_TOOLS` | `true`/`false` 是否启用工具 |
| `CODEHELPER_TIMEOUT` | 请求总超时（Go duration，如 `2m`） |
| `CODEHELPER_IDLE_TIMEOUT` | 流空闲超时 |
| `CODEHELPER_MAX_STEPS` | 单 turn 最大模型步数 |
| `CODEHELPER_MAX_OUTPUT_TOKENS` | 最大输出 token |
| `CODEHELPER_BUDGET_TOKENS` / `CODEHELPER_BUDGET_USD` | 预算上限 |
| `CODEHELPER_VERIFY_MODE` / `CODEHELPER_VERIFY_SCOPE` / `CODEHELPER_VERIFY_ON_FAILURE` | Verify Gate 模式、范围、失败处置 |
| `CODEHELPER_VERIFY_MAX_REPAIR_STEPS` / `CODEHELPER_VERIFY_TIMEOUT` / `CODEHELPER_VERIFY_COMMAND` | 修复轮数、验证超时、显式验证命令 |
| `CODEHELPER_STATE_DATA_DIR` | 持久化目录 |
| `CODEHELPER_CREDENTIAL_KIND` / `CODEHELPER_CREDENTIAL_NAME` | 凭证引用 |
| `CODEHELPER_LOG_LEVEL` | 日志级别 |
| `CODEHELPER_MEMORY_ENABLED` / `CODEHELPER_MEMORY_PATH` | 记忆开关与路径 |
| `CODEHELPER_INDEX_ENABLED` / `CODEHELPER_INDEX_MAX_FILE_BYTES` / `CODEHELPER_INDEX_MAX_FILES` | 仓库索引开关与上限 |
| `CODEHELPER_REPO_MAP_ENABLED` / `CODEHELPER_REPO_MAP_MAX_BYTES` / `CODEHELPER_REPO_MAP_MAX_DIRECTORIES` | 仓库概览段开关、字节上限、目录数上限 |
| `CODEHELPER_WORKING_SET_ENABLED` / `CODEHELPER_WORKING_SET_MAX_ENTRIES` / `CODEHELPER_WORKING_SET_MAX_BYTES` | 工作集段开关、条数上限、字节上限 |
| `CODEHELPER_EVIDENCE_ENABLED` / `CODEHELPER_EVIDENCE_MAX_ENTRIES` / `CODEHELPER_EVIDENCE_MAX_BYTES` | 证据段开关、事实条数上限、字节上限 |
| `CODEHELPER_CODING_POLICY_ENABLED` | 稳定前缀里的工作方法段开关 |
| `CODEHELPER_COMPACT_MAX_HISTORY_BYTES` / `CODEHELPER_COMPACT_SUMMARY_MAX_BYTES` / `CODEHELPER_COMPACT_MAX_DIGEST_ENTRIES` | compact 触发阈值、摘要预算、流水行数上限 |
| `CODEHELPER_VISION_*` | 视觉相关 |
| `CODEHELPER_WEB_SEARCH_BACKEND` | Web 搜索后端 |

Provider 自身密钥通常放在该 Provider 声明的环境变量中（例如 OpenAI 的 `OPENAI_API_KEY`），或通过 `--api-key-env` 指定。

### 2.4 凭证原则

- 配置里只写 **引用**（env 名 / 文件路径 / keyring key）
- 不要把 API Key 写进 TOML、日志或 git
- `codehelper auth` 用于管理非密钥引用；`kind=keyring` 时用 `--from-env` 把环境变量里的值写入系统钥匙串（macOS Keychain / Linux Secret Service / Windows Credential Manager），service 名固定为 `codehelper`

```bash
codehelper auth login --config ./codehelper.toml --kind env --name OPENAI_API_KEY
codehelper auth login --config ./codehelper.toml --kind keyring --name openai/default --from-env OPENAI_API_KEY
codehelper auth logout --config ./codehelper.toml
```

会话出网经 egress Gate（见 [RFC-011](./rfc/RFC-011-egress-broker.zh-CN.md)）：已配置的 provider endpoint 与 web search backend 自动放行；`web_fetch` 仍需审批，批准后才能 Dial 该 host。执行中若遇到未授权 host（如重定向），会再弹一次出网审批；允许后 Grant 并重试该次工具调用。超时/拒连等真实连通性失败不会走这条审批。

---

## 3. 模型

### 3.1 列出与解析

```bash
codehelper model list
codehelper model resolve --provider openai --model gpt-4.1
codehelper model resolve --provider openai-responses --model gpt-4.1
```

内置目录见 `internal/adapter/model/catalog.v1.json`（打包进二进制）。`resolve` 会说出 `protocol=`，用来区分 Chat Completions（`openai`）与 Responses（`openai-responses`）。

### 3.1.1 能力探测（probe）

```bash
# 手动探测（经 egress Gate；结果写入 --data-dir 的 provider_capabilities）
codehelper model probe --provider openai --model gpt-4.1 \
  --capability vision,reasoning --data-dir ~/.codehelper --json

# 会话叠加观测：默认只收紧；要信任 probe 的"支持"结论才放宽
codehelper exec --data-dir ~/.codehelper --trust-probe --provider openai --model gpt-4.1 "ping"
```

probe **不会**在打开会话时自动跑。`supported=0` 会清掉 catalog 里对应能力位（例如 vision 槽位因此起不来）；`supported=1` 只有 `--trust-probe` 才把 catalog 里的 `false` 改成可用。

### 3.2 使用内置 Provider

```bash
export OPENAI_API_KEY=sk-...
codehelper exec --provider openai --model gpt-4.1 "ping"

# OpenAI Responses（与上面共用同一把 OPENAI_API_KEY；默认协议仍是 openai_chat，须显式选 provider）
codehelper exec --provider openai-responses --model gpt-4.1 "ping"
```

`openai` 与 `openai-responses` 是两个 catalog 条目，不是同一 provider 的两种模式。同一 model id（如 `gpt-4.1`）在 Auto 选路下会因歧义报错——必须显式给 `--provider`。

### 3.3 自定义 OpenAI 兼容端点

```bash
export MY_LLM_KEY=...
codehelper exec \
  --provider my-custom \
  --model some-wire-id \
  --base-url https://example.com/v1 \
  --protocol openai_chat \
  --api-key-env MY_LLM_KEY \
  --context-tokens 128000 \
  --model-max-output-tokens 8192 \
  --model-capabilities streaming,tool_calls \
  "hello"
```

### 3.4 Hermetic Fixture（无密钥）

```bash
codehelper exec \
  --provider-fixture ./testdata/providers/openai-chat \
  --output-format stream-json \
  "hello"
```

### 3.5 真实模型冒烟

```bash
export LIVE_MODEL_BASE_URL=https://...
export LIVE_MODEL_WIRE_MODEL=...
export LIVE_MODEL_API_KEY=...
make live-model-smoke
```

---

## 4. 核心命令

以下均假设已 `make build`，二进制为 `./bin/codehelper`（可简写为 `codehelper`）。

### 4.1 `exec` — 非交互一轮

```bash
codehelper exec [flags] "<prompt>"
```

常用 flags：

| Flag | 说明 |
| --- | --- |
| `--config` | TOML 配置 |
| `--provider` / `--model` | 主采样（`act`）的模型；其他用途见 `[route.*]`（§2.2） |
| `--lock-route` | 缺 `[route.*]` 槽位时报错，不回落到 act |
| `--base-url` / `--protocol` / `--api-key-env` | 自定义端点 |
| `--provider-fixture` | 本地 fixture 目录 |
| `--enable-tools` | 启用内置工作区工具 |
| `--workspace` | 工具工作区（默认 `.`） |
| `--mode` | `plan` / `act` / `operate` |
| `--posture` / `--permission` | `suggest` / `auto` / `bypass` / `never` |
| `--max-steps` | 最大模型步数 |
| `--timeout` / `--stream-idle-timeout` | 超时 |
| `--output-format stream-json` | 目前仅支持 NDJSON 事件流 |
| `--data-dir` | 持久化目录 |
| `--resume` / `--continue` | 续跑 data-dir 中活动线程 |
| `--thread-id` / `--session-id` | 显式线程/会话 |
| `--approval-stdin` | 从 stdin 读审批 NDJSON |
| `--mcp-config` | MCP 服务器配置 JSON |
| `--plugin-bundle` / `--plugin-receipt` | 插件 |
| `--attach-image` | 附图（工作区相对路径，逗号分隔，最多 3）。它把路径交给模型并提示用 `image_analyze` 看图，因此需要配好 `[route.vision]`；图片不会直接进主采样的消息 |
| `--file` | 点名一个文件进入上下文（可重复）。内容注入 prompt，路径同时被 pin 进工作集：pin 的路径不衰减、不占 `max_entries` 配额 |
| `--metrics-file` / `--log-file` | 进程计数器快照与脱敏日志。计数器不含 token 与金额，那些查库（见 §4.11） |

示例：启用工具并自动审批低风险操作：

```bash
codehelper exec \
  --provider openai --model gpt-4.1 \
  --enable-tools --workspace . \
  --mode act --posture auto \
  --max-steps 12 \
  "Run tests and summarize failures"
```

输出为 **NDJSON 事件流**（`turn.started`、`output.delta`、`tool.result`、`turn.completed` 等）。可用 `jq` 过滤：

```bash
codehelper exec ... "hi" | jq -r 'select(.kind=="output.delta") | .data.text'
```

### 4.2 `tui` — 交互界面

```bash
codehelper tui --workspace .
```

在 TUI 中输入普通文本即发起 turn；以 `/` 开头为 slash 命令（如 `/help`、`/model`、`/permissions`、`/jobs`、`/relay`、`/clear`、`/compact`…）。`/compact` 在真实会话中提交 Runtime `thread.compact`（非仅截断 UI）；离线 fake host 仍可做本地 transcript 修剪。完整列表见 `codehelper tui` 内 `/help`，实现于 `internal/host/tui/commands`。

长时间跑的东西有自己的面板：`5` 或 `/agent` 看子 Agent（角色、姿态、状态，已结束的也留在列表里），`6` 或 `/task` 看后台任务并告诉你现在有没有 worker 在跑，`7` 或 `/jobs` 看后台 job 与它最新一行输出；面板上按 Enter 刷新。子 Agent 由模型的 agent 工具派生，`/agent` 只是观察面而不会替你派生一个。前台 shell 的输出边跑边出现在工具行下方（协议里是 live-only 的 `tool.output`，不落盘）。

### 4.3 `serve` / `host` — Runtime API

```bash
# HTTP + SSE
codehelper serve --listen 127.0.0.1:8080

# ACP 适配（stdio 上的 newline-delimited JSON-RPC 2.0）
codehelper host --adapter acp --data-dir ./.codehelper/state
```

适合编辑器插件或其他进程作为 Host，复用同一 Runtime。持久化 ACP 要求 `--data-dir`，stdin 被 JSON-RPC 独占（因此不支持 `--approval-stdin`）。

ACP 方法（`initialize` 的 `methods` 字段公示同一份列表，客户端不必靠探测 `-32601` 判断）：

| 方法 | 参数 → 结果 |
| --- | --- |
| `initialize` | `{protocolVersion?}` → `{protocolVersion:2, minSupportedVersion:1, methods, operations, events, provider, model}`；版本越界返回 `-32602` 且 `data` 带 `minSupportedVersion` |
| `provider/list`、`provider/select`、`model/list`、`model/select` | 单 provider / 单 model，select 仅校验一致性 |
| `thread/list`、`thread/get` | 有界 thread 目录与 turn 明细；ACP 强制限定到 Host workspace |
| `task/list`、`agent/list`、`usage/query` | 后台任务、Agent、usage/rollup 只读投影；列表 `limit` 最大 1000 |
| `session/new` | `{cwd?, title?, sessionId?}` → `{sessionId, threadId, provider, model}`；`cwd` 必须与 Host workspace 相同 |
| `session/load` | `{sessionId, threadId}` → `{sessionId, threadId, provider, model, latestSeq, runtimeSeq}` |
| `session/prompt` | `{sessionId, prompt}` → turn 终止后回 `{turnId, stopReason, output}` |
| `session/submit` | `{sessionId, operation:{kind,payload}, idempotencyKey?}` → `{operationId, accepted, kind, threadId, turnId, itemId}` |
| `session/replay` | `{sessionId, sinceSeq?, limit?}` → `{events, nextSeq, truncated}` |
| `session/cancel` | `{sessionId, reason?}` → 取消当前 turn |
| `shutdown` | 取消活跃 turn 后退出 |

通知：`session/update {sessionId, event}` 携带完整 `protocol.Event`；`session/desync {sessionId, threadId, lastSeq, oldestAvailable?, reason}` 表示历史已不完整，客户端应重新拉状态而不是继续用旧游标。

`session/submit` 承载任意 Operation（`turn.start`、`turn.cancel`、`turn.steer`、`approval.decision`、`input.reply`、`thread.compact`、`thread.fork`、`turn.revert`）。`thread_id`/`turn_id`/`item_id` 可以不填：thread 取自会话绑定（指向别的 thread 会被拒），turn 缺省取当前活跃 turn，item 每次新铸。**两个 turn 之间提交时没有活跃 turn，必须显式给 `turn_id`。** 带 `idempotencyKey` 时补全的 ID 由 key 派生，重试同一 key 返回同一份收据而不会重复执行。

```bash
# 批准一次待审批的工具调用
{"jsonrpc":"2.0","id":9,"method":"session/submit","params":{
  "sessionId":"session_...","operation":{"kind":"approval.decision",
  "payload":{"request_id":"approval_...","decision":"approve","scope":"once"}}}}
```

游标恢复：客户端持久化 `(sessionId, threadId, lastSeq)`。重连（stdio 下等于重启进程）后先 `session/load` 重绑，再用 `session/replay` 从 `lastSeq` 续读，`truncated` 为真时以 `nextSeq` 继续翻页。`--acp-replay-limit`（默认 256，对应 `serve` 的 `--sse-replay-limit`）限制单页上限，超限的 `limit` 返回 `-32602`。游标落在保留窗口之前返回 `-32001` 且 `data` 带 `oldestAvailable`。

两个恢复边界值得注意：

- **回放是实时流的子序列。** `output.delta`、`reasoning.delta`、`tool.state`、`turn.compaction` 不落盘（`eventlog.ShouldPersist`），只有实时订阅者收得到。恢复后的 UI 要按已落盘事件重建，不要指望逐 token 重放。
- **原 thread 可以继续执行。** `session/load` 重绑后，首次新 turn 会由 `ThreadManager` 从 durable eventlog 重建模型历史；重建失败则拒绝 turn，不会在空历史上静默继续。无 `--data-dir` 的 ephemeral 模式（如 `exec`）完全不承诺跨进程恢复。

#### VS Code Companion

V1 已交付 Chat、editor context、原生 diff preview、受控 apply 和后台观察面。
开发与发布门禁：

```bash
make build
make vscode-check vscode-test vscode-build
make vscode-security vscode-performance
make vscode-runtime-integration
make vscode-integration
make vscode-package
make vscode-release-dry-run
make vscode-matrix-report
make vscode-rc
```

`vscode-integration` 首次运行会下载固定的 VS Code 1.96.4 Electron Host；
`vscode-package` 生成并审计 `extensions/vscode/dist/codehelper-vscode-0.0.1.vsix`，
再用同一版本的 VS Code CLI 实际安装验证。VSIX 不内嵌 Runtime binary，安装后仍需
配置本机 `codehelper`：

```bash
code --install-extension extensions/vscode/dist/codehelper-vscode-0.0.1.vsix
```

`vscode-release-dry-run` 生成 universal 和五个 target VSIX、CycloneDX SBOM、
provenance、SHA256SUMS，以及 Marketplace/Open VSX/企业/离线四套渠道计划。演练使用
临时签名 key，产物明确标记 `dry_run=true, uploaded=false`，不得上传。生产构建必须
提供 `CODEHELPER_RELEASE_PRIVATE_KEY`、`CODEHELPER_RELEASE_TRUST_ROOTS` 和
`CODEHELPER_RELEASE_KEY_ID`；私钥必须位于仓库外并匹配 public trust root。

`vscode-matrix-report` 汇总 `extensions/vscode/dist/matrix/evidence/*.json`，输出
`report.json` 与 `report.md`；缺任一 required evidence 会失败。当前动态矩阵覆盖
macOS arm64/x64（x64 通过 Rosetta）、Remote SSH Linux arm64 和 Dev Container
Linux arm64/amd64。Windows x64/WSL2 没有动态 runner，不会被报告为已通过。

`vscode-rc` 串行执行完整 V3 门禁，并输出 `dist/rc/report.json`。dirty worktree 或
临时 dry-run key 只会得到 `validated-dry-run, publishable=false, uploaded=false`；
正式签名与渠道步骤见 [VS Code Release Runbook](./RELEASE-VSCODE.zh-CN.md)。

扩展当前支持最多 8 个 root；每个 root 使用独立 Runtime。`codehelper.binarySource`
支持 `auto|external|managed|bundled`，默认 `auto` 按已验签 bundled、已验证 managed、
external 选择。external 内部仍按 `codehelper.binaryPath`、开发仓库 `bin/codehelper`、
Extension Host `PATH` 查找，显式路径必须是绝对路径。

`CodeHelper: Check for Binary Updates` 从固定 HTTPS feed 读取 Ed25519 签名 manifest，
安装到 Workspace Extension Host 的 `globalStorageUri`。`codehelper.update.policy` 为
`off|notify|auto`（默认 `notify`），`codehelper.update.channel` 为 `stable|preview`；
两者都是 application scope，workspace 不能覆盖。更新完成 ACP handshake 与 session
attach 后才标记 healthy；失败自动回滚到未撤销的 last-known-good。离线时 external
仍可用，activation 不访问网络。

`codehelper.runtime.autoStart` 默认为 `true`，命令
`CodeHelper: Show Status` 和 `CodeHelper: Restart Runtime` 可用于检查和重启。
Runtime 继承 Extension Host 环境，并使用 CodeHelper 默认配置/凭据解析。
`codehelper.runtime.maxSteps` 默认为 8。

在 CodeHelper Activity Bar 的 Chat 中输入普通文本发起 turn，Stop 提交
`turn.cancel`。写/进程/网络副作用出现 `approval.required` 时，同时显示 Chat 卡片
和原生 modal；可用 scope 由 Runtime 事件决定。模型发送的 textual
`reasoning.delta` 实时显示在独立、可折叠的“推理过程”区域，最终 output 显示在
“最终结论”区域；两者支持标题、列表、引用、表格、链接、行内代码与 fenced code
block。Markdown 只生成有限 DOM 白名单，不执行模型 HTML。reasoning signature 和
加密 provider data 不展示，且 live reasoning 不在重启后重放。`@file` 引用已保存的 active file，
`@selection` 引用已保存的非空 active selection；`@symbol` 通过 VS Code language
service 捕获包含 cursor/selection 的最内层 symbol；`@diagnostics` 捕获 active file
当前 diagnostics，稳定排序后最多发送 32 项并记录 omitted count。例如：

```text
Review @symbol and explain the current problems in @diagnostics.
```

每个 root 最多管理 32 个 Chat。可在 Chat header 点击 `New`，或执行
`CodeHelper: New Chat` 创建；用 header selector、`CodeHelper: Select Chat` 或 Threads
Tree 切换。自动引导的 `Chat 1` 沿用 shared workspace；显式新建 Chat 使用独立 Git
worktree、Engine、工具和 journal，因此多个新 Chat 可并行执行。非 Git workspace
仍可使用 `Chat 1`，但创建隔离 Chat 会明确失败。

`Chat 1`、`Chat 2` 这类占位名会在首次提交成功后根据用户消息自动改为最多 48 个
字符的内容标题。标题在 Extension Host 本地生成，不额外调用模型，不增加 token
费用或网络延迟；Runtime 的 primary thread 和 BindingStore 同步持久化该标题。升级前
已有的占位标题会在 history hydration 时根据最近 200 个 turn 中最早的用户消息迁移。

扩展持久化全部 Chat binding、当前选择和各自 cursor。Runtime 或 Extension Host
重启后恢复每个 Chat 最近 200 个 turn，再从 cursor 接续 live event。隔离 Chat 的改动
不会直接写入主 workspace；点击 `Merge` 先生成 plan-bound preview，确认后 `Apply`
才经 fingerprint、journal 和原子事务合入。主 workspace 在 preview 后漂移时零写入
拒绝，untrusted workspace 只允许 preview。

Webview 不能指定路径。Extension Host 生成结构化引用，Runtime 再校验 workspace、
file URI、SHA-256 与 UTF-16 range；文件在提交前发生变化会 fail-closed。单文件超过
1 MiB 被拒，模型可见单项正文最多 64 KiB、全部引用编码最多 128 KiB。dirty document
不会静默保存，需由用户保存后重新提交。Chat 只根据 Runtime 在 `turn.started` 和
`turn.receipt` 中确认的 receipt 展示 context card，包括 path/digest/range、symbol 或
diagnostic 数量及 retained/truncated/omitted；本地捕获成功本身不会冒充模型已收到。

选中已保存的本地文件内容后，可从 Command Palette 或 editor context menu 执行：

```text
CodeHelper: Explain Selection
CodeHelper: Edit Selection
CodeHelper: Refactor Selection
CodeHelper: Generate Tests for Selection
```

四个入口都提交带 `source=selection_command` 的普通 turn，并聚焦同一个 Chat View。
Edit/Refactor 会先要求输入最多 4096 字符的具体指令；取消输入不会提交。Explain 在
untrusted workspace 可用，但 Runtime 以 `never` posture 限制工具；其余三项在扩展侧
先拒绝 untrusted，Runtime approval/posture 仍是最终边界。命令不会使用
`WorkspaceEdit` 或直接写文件。

Problems 或编辑器 Quick Fix 中会为当前 workspace 的本地文件 diagnostic 提供：

```text
Fix with CodeHelper
Explain with CodeHelper
```

untrusted workspace 只显示 Explain。Code Action 本身不读文件、不启动 Runtime，也不携带
`WorkspaceEdit`；执行 command 时才重新核对原 URI、document version、range、
severity、message、code、source 和当前文件 digest。diagnostic 已变化、文件已关闭/dirty、
URI 不属于当前 workspace 或磁盘内容漂移时会拒绝且不提交 turn。fresh action 只附带触发
的单条 diagnostic，并以 `source=code_action` 进入现有 Chat/thread；Fix 后续仍必须经过
Runtime edit plan approval 和 Verify Gate。

`CodeHelper Changes` Tree View 展示当前未解决的 edit plan；没有 pending plan 时保留最近
一次计划供只读回顾。展开 plan 可看到每个文件的 created/modified/deleted 状态、
before/after digest，以及按 workspace-relative path 关联的 diagnostics/verification。
选择文件只打开该文件的原生 `vscode.diff`，hunk 导航使用 VS Code 内建能力。

pending plan 下可从键盘选择 `Approve once` 或 `Deny`。Tree 节点本身不保存可执行权限，
decision 会回到 Chat 当前 pending approval，重新核对 request、turn、item、plan ID、
expiry、allowed scope 与 Trust；历史 replay、已解决或未知计划只有只读 diff，不能再次
提交 decision。Changes View 不轮询，也不会因 replay 自动打开 diff 或 modal。

VS Code Runtime 固定启用 edit-plan approval。`file_write`、`file_edit`、
`file_apply` 在任何写入前把 before/after 作为原生 VS Code diff 打开；审批 scope
固定为 once。批准会携带 plan ID 回 Runtime，Runtime 重新生成相同计划后才经过
read-before-edit、journal 和原子提交。预览后文件被外部修改会拒绝且不覆盖外部内容。
扩展不使用 `WorkspaceEdit` 写盘。当前 `file_patch` 无法生成可靠的完整 before/after，
在该模式下会被拒绝，模型应改用 `file_apply`。

CodeHelper Activity Bar 还提供 Threads、Agents、Tasks、Jobs、Approvals、Usage 六个
Tree View。Jobs 是具有 executor 的 durable Task 子集；Approvals 展示当前未解决
请求。view 打开、Runtime ready 或相关事件到达时刷新，不按固定周期轮询。Agent 或
后台 Task 进入 completed/failed/canceled 等终态时显示一次通知；重启 replay 只恢复
状态，不重复通知历史完成项。所有查询由 Runtime 按当前 workspace 隔离。

本地 multi-root workspace 为每个 root 启动独立 Runtime，最多同时管理 8 个 root。
Chat header 的 selector 或 `CodeHelper: Select Workspace Root` 明确选择 composer 目标；
selection/Code Action 始终按当前文档所属 root 路由。Changes 和六个后台 Tree 以 root
为第一层，status/restart 在多 root 时先要求选择目标。每个 root 的 durable data 位于
`storageUri/runtime/<root_id>`，binding/cursor、request/turn/plan 和 diff preview
缓存均带 root namespace。folder remove 会停止对应 Runtime；快速 re-add 会等旧进程
退出后再恢复原 durable state。

Remote SSH 已支持 external binary：扩展与 `codehelper` 都运行在远端 Workspace
Extension Host，`codehelper.binaryPath` 必须是远端绝对路径，target 必须匹配远端
OS/arch。扩展不会在本地 UI 端执行 SSH。Dev Container 同样在容器 Workspace
Extension Host 内运行扩展和 Runtime。目标 VS Code 的官方 Remote SSH 与 Dev
Containers gate 均已通过；当前范围不包含 WSL2。

workspace 未 Trust 时，扩展忽略 workspace 级 `binaryPath` 并以
`--posture never` 启动，只允许 Runtime 判定的读取；Trust 后重启为
`--posture suggest`，写操作仍须 approval，不会因 Trust 自动放行。
binding 与 cursor 存在 VS Code workspace state，Runtime durable state 存在
扩展 workspace storage。无法完整 replay 时状态显示 desync，不会静默新建会话。

### 4.4 `web` — 控制页

```bash
codehelper web --listen 127.0.0.1:8787
```

内嵌静态 UI；移动端可用 pairing / QR 相关能力（见 `internal/host/pairing`）。

### 4.5 `config`

```bash
codehelper config check --config ./codehelper.toml
codehelper config show
codehelper config reload --config ./codehelper.toml
```

### 4.6 `auth` / `model` / `thread`

```bash
codehelper auth --help
codehelper model list
codehelper model resolve --provider anthropic --model claude-sonnet-4-20250514
codehelper thread --help
```

### 4.7 扩展：`mcp` / `plugin` / `skill`

```bash
codehelper mcp --help
codehelper mcp list
codehelper mcp add ...
codehelper mcp test ...
codehelper mcp status --config ~/.codehelper/mcp.json
codehelper mcp status --config ~/.codehelper/mcp.json --json
codehelper plugin --help
codehelper plugin install --plugin-registry file:///srv/codehelper/index.json \
  --plugin-publishers ~/.codehelper/data/plugins/publishers.json review@1.2.0
codehelper plugin update --plugin-registry https://registry.example/index.json \
  --plugin-publishers ~/.codehelper/data/plugins/publishers.json review@1.3.0
codehelper plugin rollback \
  --plugin-publishers ~/.codehelper/data/plugins/publishers.json review
codehelper plugin security-revoke review
codehelper skill --help
codehelper skill lint --workspace . ./skills/review
codehelper skill lock --workspace . --data-dir ~/.codehelper
codehelper skill verify --workspace . --data-dir ~/.codehelper
```

`mcp test` 只校验配置；`mcp status` 会真实连接并 discovery，逐 server
输出 `healthy/degraded/open`、连续失败数与错误，任一 server 不健康时退出码为 1。
TUI 的 `/mcp` 面板消费 `mcp.health.changed`；`serve --mcp-config PATH`
同时通过事件流和 `GET /v1/mcp/health` 暴露同源状态。

`exec` 也可通过 `--mcp-config` 注入版本化 MCP 配置。新建配置应写
`permission_profile`；旧 v1 配置兼容地以显式 tool/resource/prompt bindings
作为授权 ceiling，远端 catalog 不能扩展这些授权。
远端 MCP tool 默认以 deferred descriptor 进入目录，不会把大型 catalog 的完整
schema 全部发送给模型；模型先用 `tool_search` 搜索，命中项才 materialize 并在
下一次采样进入 `tools[]`。materialize 本身不发起远端业务调用。
每次采样前会同步 MCP Pool 与共享 Tool Registry；若同步失败，该 turn 以可重试
`unavailable` 失败且不会请求模型。失败期间 MCP 工具会被 quarantine，恢复同步
后自动重新进入目录，避免继续展示或调用旧 executor。

Registry Plugin 必须在 `plugin.toml` 同时声明 strict SemVer `version`、`publisher`
和 `codehelper` compatibility。publisher allowlist 为 strict JSON：

```json
{
  "schema_version": 1,
  "publishers": {
    "platform": "<base64-ed25519-public-key>"
  }
}
```

`plugin install/update` 只接受显式 `NAME@VERSION`。`https://` 与 `file://`
Registry 使用相同的逐 release Ed25519 signature 和 artifact/manifest/capability
digest 校验；artifact 放入内容寻址 cache，安全解包后进入 immutable staging。
install 会原子激活并 enable；普通 update/rollback 让旧调用自然 drain，
`security-revoke` 则立即取消本进程调用，并通过 durable state watcher 在有界时间内
传播到其他运行进程。rollback 只能选择 durable receipt journal
中仍可验签且 staging 完整的版本。运行已安装 Plugin 时也必须提供同一 publisher
allowlist；旧本地 Plugin 继续使用 `trust/enable`，状态为 `unsigned-local`。
运行中的 `exec`/`serve` 会订阅该 durable state：enable/install 增加 namespaced
tool，update/rollback 在下一次 watcher tick 或 sampling 边界切换 Catalog revision
与 executor，disable/revoke 撤销 tool。普通切换只允许已开始的旧调用 drain；
尚未开始的旧 executor 会 fail-closed，`security-revoke` 还会取消在飞进程。

Configured/Plugin Skill 必须在 `SKILL.md` 同目录提供严格 `skill.toml`：

```toml
schema_version = 1
name = "review"
version = "1.2.0"
codehelper = ">=0.4.0 <0.5.0"

[dependencies]
repository-context = "^2.1.0"
```

`skill lock` 解析当前 workspace 的 precedence winner、检查 compatibility/DAG，
并原子写入 workspace-scoped lock；`skill verify` 校验 runtime version、来源、
版本、依赖边和内容 digest。存在 governed Skill 时，`exec`/`serve` 启动前必须
通过 lock 校验。旧 workspace/user 单文件 Skill 继续可用，但显示为
`version=local, locked=false`，且不能满足受治理 Skill 的版本依赖。

#### M4 扩展迁移与回滚

升级前备份 `--data-dir`，尤其是 `plugins/state.json`、`plugins/publishers.json`
和 workspace-scoped Skill lock。迁移边界如下：

- 旧 workspace/user Plugin 无需改 manifest，继续用 `plugin trust/enable`，
  并明确显示 `unsigned-local`；要改为 Registry 安装，先移走同名本地 bundle，
  再配置 publisher allowlist 并执行显式版本 `plugin install NAME@VERSION`；
- Plugin state 仍是 schema v1，新 activation/receipt 字段为原子写入的兼容扩展；
  不会把人工 review receipt 自动提升成 publisher signature；
- 普通 Plugin 回退使用 `plugin rollback NAME`，只接受 receipt journal 中仍可验签、
  digest 完整的 staging；安全事件使用 `plugin security-revoke NAME`，不能用
  rollback 绕过 revoke。downgrade/replay 会被 generation/version high-watermark 拒绝；
- 旧 workspace/user 单文件 Skill 保持 `local/unlocked`。Configured/Plugin Skill
  必须补 `skill.toml`；受治理 Skill 变化后需重新审计并执行 `skill lock`，
  CodeHelper 不会自动接受未知或漂移的旧 lock；
- 旧 MCP v1 配置可从显式 bindings 推导 permission ceiling；建议用 `mcp add`
  重写显式 `permission_profile`，再以 `mcp test/status` 验证。

`tool.catalog.changed`、`mcp.health.changed` 和 `extension.lifecycle` 都经 ACP/HTTP
共享事件流发布；lifecycle 在 sampling 边界投影，所以外部安装/升级会在下一次
模型采样时可见，安全 revoke 的 authority 取消不等待该事件。

受信 Host 动态工具默认关闭，且必须同时启用普通工具运行时：

```bash
codehelper host --adapter acp --data-dir ./.codehelper/state \
  --enable-tools --trusted-dynamic-tools
codehelper serve --data-dir ./.codehelper/state \
  --enable-tools --trusted-dynamic-tools
```

ACP 管理方法为 `tool/catalog`、`tool/register`、`tool/replace`、
`tool/revoke`；Runtime 以 `tool/call` 通知发起调用，客户端通过
`tool/call/result` 回填。HTTP 对应 `GET|POST /v1/tools/dynamic`、
`PUT|DELETE /v1/tools/dynamic/{name}`、`GET /v1/tools/dynamic/calls`
和 `POST /v1/tools/dynamic/calls/{call_id}/result`。HTTP pending call
在结果提交或原始 turn 取消前会保留，因此轮询重试不会丢调用。
catalog 响应中的 `entries` 是共享 Registry 的完整 immutable snapshot
（含 source/revision/state/descriptor），`tools` 是可供 Host 管理的动态定义；
`catalog_id`、`generation` 与 `digest` 对应 `entries`。

replace/revoke 必须携带最近一次 snapshot 的 `expected_generation`（ACP 为
`expectedGeneration`），stale 请求以 conflict 拒绝。客户端 spec 只能提供
name/namespace/description/input schema/deferred 标记；capability、resource、
parallel 与 sandbox policy 固定由服务端配置，不能从请求提升。默认 policy
把调用归类为 `plugin` 并继续经过 ToolGuard；执行发生在受信 Host，故不虚假
声明本地 `SandboxStrong`。

### 4.8 编排：`worker` / `fleet` / `automation` / `workflow` / `lane`

```bash
codehelper worker run --data-dir ~/.codehelper --workspace .          # 常驻调度器（前台守护）
codehelper worker enqueue --data-dir ~/.codehelper --prompt "..." --role implementer --max-attempts 3
codehelper worker enqueue --data-dir ~/.codehelper --executor workflow_run --workflow-spec workflow.json
codehelper worker enqueue --data-dir ~/.codehelper --executor shell_command --command "make test" --timeout 10m
codehelper worker list --data-dir ~/.codehelper --state queued
codehelper fleet list|status|inspect|logs|profile   # 只读审计
codehelper automation --help
codehelper workflow validate --script ./my.workflow.js
codehelper workflow run --script ./my.workflow.js --workspace . --driver fake
codehelper workflow run --script ./my.workflow.js --data-dir ~/.codehelper --id run_123  # 断点续跑
codehelper workflow status --data-dir ~/.codehelper --id run_123
codehelper lane --help
```

后台任务由 `codehelper worker` 执行：它 claim 任务、续租、回收超租的任务、按 backoff 重试，退出时把在飞的任务放回队列并把这次尝试还回去。claim 可接管同一 workspace 的其他 session，但会按规范化 workspace root 过滤；同一个 `--data-dir` 可存多个 workspace，worker 不会在错误根目录执行它们。新 `exec` / `serve` 进程打开数据库也不会回收仍有效的 lease，接管必须等 lease 到期。生产 scheduler 同时执行 `agent_turn`、`workflow_run` 与 `shell_command`；隔离写 `agent_turn` 只有在 child diff 经 baseline conflict 检查、ToolGuard 与 parent journal 合回宿主工作区后才 completed，结果里的 `merged` 表示是否真的进入宿主根；merge 冲突或失败时 task failed，worktree 不会被当成已交付成果。workflow 复用 durable node checkpoint，节点 timeout 会取消对应真实 turn，等旧 attempt 退出后才 retry；shell 仍经 `shell_run` 的 ToolGuard、policy 与强沙箱。后两类任务默认只尝试一次；只有调用方确认副作用可重放时，才同时传 `--idempotent --max-attempts N`。后台没有交互审批，policy/hook 要求 Ask 时任务立即失败而不会无限等待。`execution.worker.enabled` 打开后 `exec` / `serve` 进程内也会起同一个调度器。

子 Agent 的 `execution.subagent.workspace` 支持 `auto`、`read_only`、`worktree` 和 `same_workspace_serialized`。最后一种让写 child 直接使用宿主工作区，但父/子 turn 会按整个 turn 串行；spawn 后 child 要等当前父 turn 结束才能启动。因此当前 turn 内的 `agent_wait` 会返回 `deferred=true`，应结束当前 turn，下一 turn 再读取结果。该策略下改动已经在宿主工作区，不要再调用 `agent_merge`。

`codehelper fleet` 只剩读的动词（`list` / `status` / `inspect` / `logs` / `profile`）：它的 JSONL 是审计轨，不再调度任何东西（原 `create` / `enqueue` / `interrupt` / `resume` 请改用 `codehelper worker`）。

`workflow run` 带 `--data-dir` 时按节点 checkpoint 落库，`--id` 可以续跑一个被中断的 run；spec 变了会拒绝续跑而不是拿旧记录硬凑。
同一波次的节点会并发提交；production driver 当前让它们共用一个可写 workspace，
因此 turn 会经 whole-turn gate 串行，避免争用单 active journal 或破坏回滚原子性。
需要真正并行写入时应使用隔离子 Agent/worktree；Workflow 尚无每节点 worktree merge。
task 节点可声明 `response_schema`，JS 形式为
`task({prompt: "...", response_schema: {...}})`。production driver 要求最终文本是
唯一完整 JSON value 并在本地校验 schema；schema 最大 64 KiB，外部 `$ref`
被拒绝。这是结果校验，不是 provider constrained decoding。当前没有 Workflow
profile registry，因此 `profile` 非空会在启动 turn 前明确失败，不会被静默忽略。

Fleet profile 可用 `--file` 指定 TOML，或省略后使用内置默认 profile。

### 4.9 安全与诊断

```bash
codehelper sandbox status
codehelper sandbox probe
codehelper doctor
codehelper diagnostics
codehelper features
codehelper execpolicy
```

### 4.10 Review / Apply / Init

```bash
codehelper init
codehelper setup
codehelper review --help
codehelper apply --help
```

工作区变更由 `workspacejournal` 记录，支撑审查与应用补丁计划。

### 4.11 用量、成本与延迟

`metrics` 与 `scorecard` 读同一个库、报同一批数字，只是密度不同：

```bash
codehelper metrics    --data-dir ~/.codehelper          # 明细：按 provider/model 一行，按 span name 一行
codehelper scorecard  --data-dir ~/.codehelper          # 汇总：一行一个指标
codehelper scorecard  --data-dir ~/.codehelper --session session-1
codehelper metrics    --data-dir ~/.codehelper --turn turn_abc --json
codehelper scorecard  --data-dir ~/.codehelper --since 24h
```

共用 flag：`--data-dir`（必需，除非用 `--file`）、`--session` / `--thread` / `--turn` 缩小范围、`--since` / `--until` 限定时间窗（RFC3339 时刻或 `24h` 这样的时长，后者表示"多久以前"）、`--json`。

金额的三种答案是刻意不同的字符串：

| 输出 | 含义 |
| --- | --- |
| `$0.0123` | 全部调用都有价格，这就是金额 |
| `$0.00` | 价格已知且为零（免费模型真的没花钱） |
| `unknown` | 没有一个调用有价格，谁也不知道花了多少 |
| `$0.0123+ (2 calls unpriced)` | 混合，这是下限而不是总额 |

**价格未知永远不会显示成 `$0.00`。** 同理，范围内没有 span 时报 `latency: not recorded`，不报零——从旧版本升上来的库有用量行却没有 trace。

`--file` 是显式降级：它读 `--metrics-file` 写出的**进程计数器**（events published、subscribers dropped、compaction 之类），与 token 和金额无关，输出也这么说。两个 flag 都不给则退出码 2。

```bash
codehelper exec ... --metrics-file /tmp/counters.json "hi"
codehelper metrics --file /tmp/counters.json          # counters file=counters.json keys=8 (process counters, not billing)
```

TUI 里 `/cost` 或 `/usage` 打开 cost 面板，报本 turn（token、金额、首 token、各阶段耗时、预算剩余）、本 thread、本 session 三级，Enter 刷新。turn 级数字来自 turn 收据，因此不带 `--data-dir` 也有；thread 与 session 合计要查库，没有持久化会话时面板会直说。`/status` 仍是一行，其中的金额片段守同一条 unknown 规则。

HTTP 侧：

```bash
curl "$BASE/v1/usage?thread_id=$THREAD"                          # 逐行明细 + rollup（含 cost_known、cached_share）
curl "$BASE/v1/threads/$THREAD/turns/$TURN/trace"                # 该 turn 的 span 树
```

服务端返回 microunits 不格式化金额，但会用 `cost_known` 明说这个数是全额还是下限。预算剩余不另开端点——它在 `turn.receipt` 事件上，从 `/v1/events` 就能拿到。turn 不属于所给 thread 时 trace 端点返回 404；turn 存在但没有 span 时返回空列表（那是"没记"，不是"不存在"）。

### 4.12 其它运维命令

```bash
codehelper sessions
codehelper runtime-observe
codehelper completion bash|zsh|fish
```

---

## 5. Mode 与 Posture

### 5.1 Mode（工具能力边界）

| Mode | 含义（概括） |
| --- | --- |
| `plan` | 硬只读：仅允许 Read，其余 capability 在 mode 层拒绝 |
| `act` | 常规编程改动（默认） |
| `operate` | 相对 `act` 在 `auto` posture 下放大 Process / Network / Plugin 判定（见下表） |

`act` 与 `operate` 均不在 mode 层拦截 capability；差集只在 **posture=`auto`** 下生效。`suggest` / `never` / `bypass` 下两者行为一致（仍受 LifecycleGrants Ask 与 constitution 约束）。

| Capability | `act` + `auto` | `operate` + `auto` |
| --- | --- | --- |
| Read / Write | allow | allow |
| Process（如 shell） | deny | allow |
| Network / Plugin | deny | ask |

TUI：`/mode [plan|act|operate]` 切换工具 mode（与 UI chat/plan 视图分离）；`/status` 展示当前 `toolMode`。CLI：`--mode operate`。

### 5.2 Posture（审批姿势）

| Posture | 含义（概括） |
| --- | --- |
| `suggest` | 尽量询问 |
| `auto` | 低风险自动、高风险询问（常用） |
| `bypass` | 跳过常规审批（**不能**跳过 constitution） |
| `never` | 拒绝需要审批的工具调用 |

工作区可持久化权限记忆：`.codehelper/permissions.toml`。
用户/仓库 constitution 规则始终机械生效。

交互审批：TUI 内确认；`exec` 可用 `--approval-stdin` 喂入决策 NDJSON。

### 5.3 文件编辑工具

改文件的工具有四个，都受 read-before-edit 约束（编辑既存文件前必须先 `file_read`，写入成功后指纹作废、需重读）。语义与设计取舍见 [RFC-005](./rfc/RFC-005-edit-transaction.zh-CN.md)。

| 工具 | 用途 |
| --- | --- |
| `file_write` | 整文件原子写入；文件已存在则保留其权限位 |
| `file_edit` | 替换文件中**唯一**匹配的一段文本 |
| `file_apply` | 一次调用内跨多个文件做 `write` / `edit` / `move` / `delete`，**要么全部生效要么全部不生效** |
| `file_patch` | 应用标准 unified diff；依赖 `git` 与强沙箱 |

`file_apply` 的行为要点：

- **先校验后写入**。任一改动的前置条件不满足（`edit` 的 `old` 匹配 0 次或多次、`move` 的目标已存在、`delete` 的文件不存在……）时磁盘零改动，错误作为工具结果回灌模型，turn 不会因此失败。
- **同一文件可在一次调用里改多次**：后面的改动看到前面的结果，不需要中间重读。
- `"dry_run": true` 只返回 unified diff，不写盘；但它仍按写操作声明资源，**照样要过审批**——预览不是绕开审批的通道。
- `move` 的源与目标都算写入，两者都会出现在审批面板与收据里（源 `deleted`、目标 `created`）。
- 单次调用最多 64 个改动。
- 进程在写入过程中被杀（`kill -9`、断电）时事务性不成立：回退代码没有机会运行，工作区可能留下部分改动。turn 级回滚（`turn.revert`）同样只在活进程内有效。

### 5.4 检索与符号工具

| 工具 | 用途 |
| --- | --- |
| `search_text` / `search_files` / `search_project` | 内容与文件名检索 |
| `project_map` | 目录结构概览 |
| `search_symbol` | 按名字子串查声明（函数 / 类型 / 类 / 常量…） |
| `search_definition` | 按名字精确查声明 |
| `search_references` | 查一个名字在仓库里的使用处，默认排除定义点 |
| `search_related_tests` | 查覆盖给定源文件的测试文件 |

要点：

- **文件集合由 git 决定**。在 git 仓库里走一次 `git ls-files`，所以 `.gitignore`（含嵌套）、`.git/info/exclude`、全局 excludes 全部生效；非 git 仓库退回目录遍历。`vendor` / `node_modules` / `.git` / `.codehelper` 始终跳过。**submodule 里的文件不可见**。
- **符号结果是词法的，不是编译器的**（结果里带 `resolution: "lexical"`）。注释与字符串里长得像声明的文本会被抽出来；没有类型解析与调用图。因此"索引里没有"**不等于**"仓库里没有"，否定结论要用 `search_text` 复核。
- 首批支持抽符号的语言：Go / Python / JavaScript+TypeScript / Rust / Java。其他语言的文件仍进索引（可被 `search_references` 扫描），但没有声明条目。
- 索引在第一次符号调用时构建，之后按大小/修改时间 + 内容摘要增量刷新。索引被关掉或损坏时，四个符号工具会明确报告不可用及原因，`search_text` 等不受影响。
- 索引状态可在 `codehelper diagnostics --json` 的 `maturity.repo_index` 看到（值为 `lexical`，即"能用但是词法精度"）。

边界、降级契约与 `affected` 映射规则见 [RFC-001](./rfc/RFC-001-repo-context.zh-CN.md)。

---

## 6. 持久化会话

```bash
DATA=~/.codehelper/work/demo
mkdir -p "$DATA"

codehelper exec --data-dir "$DATA" --provider openai --model gpt-4.1 \
  "Create a short plan"

codehelper exec --data-dir "$DATA" --resume --provider openai --model gpt-4.1 \
  "Continue"
```

`--continue` 是 `--resume` 的别名。也可用 `--thread-id` 指定线程。

---

## 7. 输出与问题协议

- 成功路径：stdout 上的 **Event NDJSON**
- 失败路径：稳定的 **Problem**（含 `code`、是否可重试等），避免把不稳定上游文案当作机器协议

机器侧请解析 `kind` / `code`，不要依赖 stderr 自然语言。

---

## 8. 开发者常用 Make 目标

| 目标 | 说明 |
| --- | --- |
| `make verify` | gofmt + brand + vet + unit + 串行 race |
| `make test` / `make race` | 仅测试 / 仅 race |
| `make smoke` | 二进制 help/version |
| `make cli-smoke` / `tui-smoke` | Host 冒烟子集 |
| `make security-test` | 安全相关包测试 |
| `make vscode-security` / `vscode-performance` | VS Code Webview/Trust/Changes 可访问性安全门禁与 10k event、Runtime ready 预算 |
| `make vscode-compatibility` | 校验 extension/binary/ACP/schema/target/channel 机读兼容清单及生成物漂移 |
| `make vscode-runtime-integration` | 真实 CodeHelper binary + stdio ACP 集成 |
| `make vscode-integration` | 固定 VS Code 1.96.4 的 Electron Extension Host、1 MiB context capture 与 native flow 集成 |
| `make vscode-package` | 构建、8 文件 allowlist 审计并实际安装验证 VSIX |
| `make vscode-matrix-report` | 汇总 schema v1 E2E evidence；required job 缺失时失败 |
| `make vscode-rc` | 聚合安全、性能、兼容、签名、SBOM/provenance 与渠道 RC 证据 |
| `make sandbox-attack-test` | 沙箱攻击语料 |
| `make secret-leak-test` | 密钥泄漏检查 |
| `make live-model-smoke` | 真实模型（需密钥） |
| `make package` | 发布打包 |
| `make clean` | 清理 `bin/` `dist/` |

---

## 9. 排障

| 现象 | 排查 |
| --- | --- |
| `credential ... is not set` | 检查 `--api-key-env` / Provider 默认 env 是否导出 |
| 工具不执行 | 是否加了 `--enable-tools`；mode/posture 是否过严 |
| 沙箱失败 | `codehelper sandbox probe`；Linux 需可用 bwrap/landlock，macOS Seatbelt |
| 配置被拒 | `config check`；是否写了未知 TOML 字段或把密钥写进了配置 |
| resume 无效 | 是否同一 `--data-dir`；线程是否仍存在 |
| 流中途断开 | 调大 `--timeout` / `--stream-idle-timeout`；检查上游限流 |
| race 偶发超时 | `make verify` 已串行 `-p 1`；可单独 `go test -race -p 1 ./pkg` |

开启脱敏日志与进程计数器：

```bash
codehelper exec ... --log-file /tmp/codehelper.jsonl --metrics-file /tmp/counters.json "hi"
```

排查"这一轮花了多少"用 §4.11 的 `metrics` / `scorecard` 查库，计数器文件里没有这个。

---

## 10. 最小工作流清单

1. `make build`
2. 配置密钥引用（env）
3. `codehelper model list` 确认目录
4. Fixture 跑通：`--provider-fixture`
5. 真实模型：`exec` 无工具 → `exec --enable-tools` → `tui`
6. 需要 API Host：`serve` / `host --adapter acp`
7. 提交改动前：`make verify`

更深层的包职责与扩展方式见 [ARCHITECTURE.zh-CN.md](./ARCHITECTURE.zh-CN.md)。
