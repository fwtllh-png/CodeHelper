# Scripts

该目录保存可重复执行的构建、基线提取、验证和发布脚本。

脚本必须支持从仓库根目录运行，正确传递失败退出码，并避免写死个人绝对路径。

## VS Code 本地配置

`setup-vscode-local.sh` 在 macOS 上完成 CodeHelper target VSIX 的本地构建、DeepSeek
Keychain 凭证、可信 TOML、官方 VS Code User Settings 与扩展安装：

```bash
make vscode-local-setup
```

该脚本固定使用官方 VS Code CLI，不使用可能指向 Cursor 的 `PATH` 中 `code`。完整说明见
[`docs/VSCODE-LOCAL-INSTALL.zh-CN.md`](../docs/VSCODE-LOCAL-INSTALL.zh-CN.md)。
