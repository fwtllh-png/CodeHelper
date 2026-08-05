# CodeHelper

CodeHelper 是用 Go 实现的**终端优先 AI 编程助手**：在本地工作区里完成对话、工具调用、审批与沙箱执行，并可通过 CLI、TUI、HTTP/SSE、ACP 与轻量 Web 控制页接入。

| | |
| --- | --- |
| 语言 / 运行时 | Go 1.26+ |
| 主入口 | [`cmd/codehelper`](./cmd/codehelper) → [`internal/host/cli`](./internal/host/cli) |
| 架构说明 | [docs/ARCHITECTURE.zh-CN.md](./docs/ARCHITECTURE.zh-CN.md) |
| 使用说明 | [docs/USAGE.zh-CN.md](./docs/USAGE.zh-CN.md) |

## 它能做什么

- **一次性执行**：`codehelper exec "..."` 输出 NDJSON 事件流
- **交互式 TUI**：`codehelper tui`（Bubble Tea）
- **Runtime API**：`codehelper serve` / `codehelper host`（HTTP·SSE 或 ACP）
- **控制页**：`codehelper web`
- **工具与安全**：内置文件/Shell/搜索等工具，经 Guard + policy + permissions + constitution + OS 沙箱
- **扩展**：MCP、plugin、skill、hooks、memory
- **编排**：task / automation / fleet / workflow / lane / subagent

凭证只保存**非密钥引用**（环境变量名、文件路径、keyring key），不会把 API Key 写进配置或报告。

## 快速开始

```bash
# 依赖：Go 1.26+
git clone <repo> && cd CodeHelper

make build
./bin/codehelper version
./bin/codehelper help

# 无真实密钥的 hermetic 冒烟（fixture provider）
./bin/codehelper exec \
  --provider-fixture ./testdata/providers/openai-chat \
  --output-format stream-json \
  "hello"

# 交互界面
./bin/codehelper tui --workspace .
```

真实模型调用示例：

```bash
export OPENAI_API_KEY=sk-...
./bin/codehelper exec \
  --provider openai \
  --model gpt-4.1 \
  --enable-tools \
  --workspace . \
  --mode act \
  --posture auto \
  "Summarize this repository in 5 bullets"
```

更多用法见 [使用说明](./docs/USAGE.zh-CN.md)。

## 开发与验证

```bash
make verify          # gofmt + brand + vet + test + 串行 race
make build           # → bin/codehelper
make smoke           # help / version
make brand-check
make security-test
make sandbox-attack-test
make secret-leak-test
make cli-smoke
make tui-smoke
make package         # 多平台发布产物（scripts/package-release.sh）
make vscode-matrix-report # VS Code V3 E2E evidence 汇总（缺项失败）
make clean
```

`make live-model-smoke` 会打真实模型，需同时提供：

- `LIVE_MODEL_BASE_URL`
- `LIVE_MODEL_WIRE_MODEL`
- `LIVE_MODEL_API_KEY`

或 `LIVE_MODEL_CONFIG` + `LIVE_MODEL_NAME`。密钥不会写入仓库。

## 仓库布局

```text
cmd/codehelper/                 进程入口
internal/
  host/                      CLI / TUI / WebUI / Runtime API / pairing
  runtime/                   Application、wire 装配、protocol、agent engine
  security/                  policy、permissions、constitution、sandbox
  orchestration/             task、automation、fleet、workflow、lane、subagent
  adapter/                   provider、model、tool、mcp、plugin、skill、hooks…
  persist/                   state、snapshot、session、contentstore、workspacejournal
  platform/                  OS 进程、内容依赖探测
  observability/             telemetry、usage、diagnostics
  config/                    TOML / env / CLI 配置加载
  buildinfo/                 版本嵌入
docs/                        架构与使用文档
scripts/                     品牌检查、冒烟、打包
testdata/                    Provider SSE fixture 等
```

分层与依赖禁令见 [架构与设计](./docs/ARCHITECTURE.zh-CN.md)。

## 设计要点（摘要）

1. **Host 与 Runtime 分离**：UI/CLI 只提交 `Operation`、订阅 `Event`；业务循环在 `runtime/agent`。
2. **Wire 只装配**：`runtime/app/wire` 连接 config、model、tool、sandbox、persist，不跑主循环。
3. **工具必经 Guard**：宿主不得绕过 `adapter/tool/guard` 直接执行危险能力。
4. **安全分层**：session policy ≠ 工作区 permissions ≠ constitution hold ≠ OS sandbox。
5. **可持久化 Runtime**：带 `--data-dir` 时状态进入 SQLite / eventlog，可 resume。

## 文档索引

| 文档 | 内容 |
| --- | --- |
| [ARCHITECTURE.zh-CN.md](./docs/ARCHITECTURE.zh-CN.md) | 分层、数据流、包职责、安全模型、扩展点 |
| [USAGE.zh-CN.md](./docs/USAGE.zh-CN.md) | 安装、配置、常用命令、模式/审批、排障 |
| [VSCODE-LOCAL-INSTALL.zh-CN.md](./docs/VSCODE-LOCAL-INSTALL.zh-CN.md) | VS Code 插件本地构建、安装、体验、升级与卸载 |
| [RELEASE-VSCODE.zh-CN.md](./docs/RELEASE-VSCODE.zh-CN.md) | VS Code 支持矩阵、RC 门禁、签名与渠道 runbook |
| [scripts/README.md](./scripts/README.md) | 脚本约定 |

## 许可与品牌

二进制与源码中的产品名称为 **codehelper**。仓库通过 `scripts/check-brand.sh` 禁止在 `.go` / Makefile / shell 中出现历史品牌字面量。
