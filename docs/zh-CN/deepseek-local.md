# 本机 DeepSeek 一键编译、配置与运行

本文为人和 Coding Agent 提供确定性的一键编译、配置与 Web 启动入口。

## 前置条件

- Go 1.26 或更高版本；
- Git 与 Make；
- 重新构建 Web 前端时需要 Node.js 与 npm；
- 有效的 DeepSeek API Key；

## 一键命令

在仓库根目录执行：

```bash
make deepseek-init
make deepseek-web
```

| Target | 结果 |
| --- | --- |
| `deepseek-init` | 编译 `bin/codehelper` 并安装 Web 配置 |
| `deepseek-web` | 完成初始化并在 `127.0.0.1` 启动 Web 工作区 |

## 密钥来源

`scripts/deepseek-local.sh` 按以下顺序查找凭证：

1. `DEEPSEEK_API_KEY`；
2. Git 忽略的本机 Runbook `docs/DEEPSEEK-LIVE.zh-CN.md`；
3. macOS Keychain Service `codehelper`、Account `deepseek/default`；
4. CodeHelper 品牌切换前的历史 Keychain Service、Account `deepseek/default`；
5. 不回显的终端输入。

Keychain 仅作为已有凭证的兼容读取来源。安装后的 TOML 只包含环境变量引用：

```toml
[credential]
kind = "env"
name = "DEEPSEEK_API_KEY"
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

## Web 控制

默认 Workspace 是 CodeHelper 仓库。操作其他仓库时：

```bash
CODEHELPER_LOCAL_WORKSPACE=/path/to/project make deepseek-web
```

## Agent 执行说明

Agent 应调用 Make Target，不得读取、打印、总结或 Patch 本机 Runbook：

```bash
make deepseek-init
make deepseek-web
```

`deepseek-web` 默认打开系统浏览器；使用普通浏览器标签页即可。

如果 IDE 无法安全读取凭证，应停止并请用户在普通 Terminal 执行相同 Target。不得把
凭证写入受 Git 跟踪的配置值，也不得在命令输出中暴露它。

## 验证

非交互环境检查：

```bash
./scripts/deepseek-local.sh check
./bin/codehelper --version
```

检查会验证 Binary 与 TOML 已安装，不发送产生费用的模型请求。

真实 Provider 请求通过 Web 发起：

```bash
DEEPSEEK_API_KEY='...' ./bin/codehelper \
  --config ~/.config/codehelper/config.toml \
  --workspace . \
  --posture suggest \
  --open
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
