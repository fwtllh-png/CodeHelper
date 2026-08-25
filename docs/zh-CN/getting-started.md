# 快速开始

## 1. 前置条件

| 依赖 | 说明 |
| --- | --- |
| Go 1.26+ | Runtime 必需 |
| Git | 仓库工作流和 Worktree 隔离必需 |
| Make | 推荐的统一构建入口 |
| Node.js + npm | 仅重新构建 Web 前端时需要 |
| macOS/Linux | 推荐；Windows 的沙箱能力边界不同 |

## 2. 构建

```bash
git clone https://github.com/fwtllh-png/CodeHelper.git
cd CodeHelper
make web-install
make web-build
make build
./bin/codehelper --version
```

二进制包含 Web 静态资源，启动后不需要独立前端服务。

## 3. 配置

从安全示例开始：

```bash
cp docs/examples/codehelper.toml ./codehelper.toml
```

配置 Credential Reference，不要写入 Secret：

```toml
[credential]
kind = "env"
name = "OPENAI_API_KEY"

[execution]
provider = "openai"
model = "gpt-4.1"
workspace = "."
tools = true
```

随后在启动进程的环境中提供凭证：

```bash
export OPENAI_API_KEY='...'
```

也可以先启动 Web，再在 Settings 中把 Credential 写入系统 Keyring。浏览器不会持久化
原始凭证值。

## 4. 启动 Web

```bash
./bin/codehelper \
  --config ./codehelper.toml \
  --workspace . \
  --enable-tools \
  --posture suggest \
  --open
```

Web 只监听 `127.0.0.1`，默认选择空闲端口。终端先输出 Listening URL，完成持久化
恢复后再输出 Runtime Ready URL。

支持的启动参数见[Web 使用指南](./usage.md)。运行 `./bin/codehelper --help` 可查看
当前 Binary 的参数事实。

## 5. 使用 Fixture

无需凭证和网络即可启动真实 Runtime 与 Web Transport：

```bash
./bin/codehelper \
  --provider-fixture ./testdata/providers/openai \
  --provider openai \
  --model gpt-fixture \
  --workspace . \
  --no-open
```

Fixture 使用确定性的已记录响应，但仍经过真实 Session、Operation、Event、Guard 和
Persistence 路径。

## 6. 本机 DeepSeek

仓库所有者可执行：

```bash
make deepseek-web
```

该 Target 会构建 Binary、安装本机配置并启动 Web。具体凭证边界见
[本机 DeepSeek 一键配置与运行](./deepseek-local.md)。

## 7. 首次检查

进入 Web 后确认：

1. Settings 中 Runtime 状态为 Ready；
2. Provider、Model 与 Credential 状态符合配置；
3. 创建 Session 后 Composer 可用；
4. `suggest` Posture 下修改型 Tool 会进入 Approval；
5. 完成 Turn 后可查看 Receipt、Usage 与 Trajectory。

Runtime 无法启动时，页面保留 Boot Failure Surface，并展示结构化修复信息。

## 8. 开发验证

```bash
make web-check
make web-test
make web-e2e
make test
```

完整发布前门禁使用 `make verify`。
