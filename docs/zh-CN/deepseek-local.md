# 本机 DeepSeek 一键编译、配置与运行

本文为人和 Coding Agent 提供 macOS 上确定性的一键编译、配置、Web 与 TUI 入口。

## 前置条件

- Go 1.26 或更高版本；
- Git 与 Make；
- 重新构建 Web 前端时需要 Node.js 与 npm；
- 有效的 DeepSeek API Key；
- 可访问 macOS Keychain。

## 一键命令

在仓库根目录执行：

```bash
make deepseek-init
make deepseek-tui
make deepseek-web
```

| Target | 结果 |
| --- | --- |
| `deepseek-init` | 编译 `bin/codehelper`，安装配置，写入 Keychain 并校验模型路由 |
| `deepseek-tui` | 完成初始化并启动启用 Tool 的 TUI |
| `deepseek-web` | 完成初始化并在 `127.0.0.1` 启动 Web 工作区 |

## 密钥来源

`scripts/deepseek-local.sh` 按以下顺序查找凭证：

1. `DEEPSEEK_API_KEY`；
2. Git 忽略的本机 Runbook `docs/DEEPSEEK-LIVE.zh-CN.md`；
3. macOS Keychain Service `codehelper`、Account `deepseek/default`；
4. CodeHelper 品牌切换前的历史 Keychain Service、Account `deepseek/default`；
5. 不回显的终端输入。

初始化会把取到的值迁移到当前 `codehelper` Keychain Service。安装后的 TOML 只包含引用：

```toml
[credential]
kind = "keyring"
name = "deepseek/default"
```

## 本机 Runbook

创建或刷新本机 Runbook：

```bash
./scripts/deepseek-local.sh doc
```

`docs/DEEPSEEK-LIVE.zh-CN.md` 按仓库所有者的明确要求保存本机真实 API Key。该文件：

- 被 `.gitignore` 精确忽略；
- 权限为 `0600`；
- 不作为 Runtime 的长期凭证存储；
- 禁止通过 `git add -f` 提交，禁止在输出中复制其内容。

受 Git 跟踪、面向其他用户的维护文档仍然禁止保存真实凭证。

## TUI 控制

默认 Workspace 是 CodeHelper 仓库，默认本机 Posture 为 `bypass`：

```bash
make deepseek-tui
```

需要审批提示或操作其他仓库时：

```bash
CODEHELPER_LOCAL_POSTURE=suggest make deepseek-tui
CODEHELPER_LOCAL_WORKSPACE=/path/to/project make deepseek-tui
```

`bypass` 只适用于受信任的本机 Workspace。它不会绕过 Tool Guard、Constitution、
Journal 或 OS Sandbox。

## Agent 执行说明

Agent 应调用 Make Target，不得读取、打印、总结或 Patch 本机 Runbook：

```bash
make deepseek-init
CODEHELPER_LOCAL_POSTURE=suggest make deepseek-tui
make deepseek-web
```

`deepseek-web` 默认打开系统浏览器；使用普通浏览器标签页即可。

如果 IDE Sandbox 拒绝写 macOS Keychain，应停止并请用户在普通 macOS Terminal
执行相同 Target。不得把凭证降级为受 Git 跟踪的配置值，也不得在命令输出中暴露它。

## 验证

非交互环境检查：

```bash
./scripts/deepseek-local.sh check
./bin/codehelper config show --config ~/.config/codehelper/config.toml
```

检查会验证 Binary、TOML、Keychain 引用，以及指向 `https://api.deepseek.com` 的内置
`openai_responses` Route；不会发送产生费用的模型请求。

真实 Provider 请求可通过 TUI 发起，也可执行：

```bash
./bin/codehelper exec \
  --config ~/.config/codehelper/config.toml \
  --workspace . \
  --mode plan \
  --posture never \
  "总结当前仓库"
```

## 手动恢复

仅重建本机 Runbook：

```bash
DEEPSEEK_API_KEY='your-key' ./scripts/deepseek-local.sh doc
```

重建完整 Runtime 配置：

```bash
DEEPSEEK_API_KEY='your-key' make deepseek-init
```

使用字面量时不要把命令留在持久 Shell History 中；优先使用安全输入或已有 Keychain
条目。
