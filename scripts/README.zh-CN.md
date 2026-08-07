# 仓库脚本

简体中文 | [English](./README.md)

本目录保存稳定的验证、冒烟、本地配置和发布打包入口。除非脚本明确说明，否则从仓库
Root 运行。

| 脚本 | 网络 | 输出或副作用 |
| --- | --- | --- |
| `check-docs.sh` | 无 | 检查 Markdown Link 与双语镜像 |
| `check-book.sh` | 无 | 检查书籍 Catalog、元数据、镜像、路径和导航 |
| `check-doc-governance.py` | 仅外链模式联网 | 检查 Ownership、PR Impact、Freshness、Release Fact、图片和外链 |
| `render-book-navigation.py` | 无 | 根据书籍 Catalog 重新生成双语导航 |
| `check-brand.sh` | 无 | 扫描已跟踪源码中的历史品牌 |
| `test-brand-check.sh` | 无 | Brand Scanner 自测 |
| `test-secret-leak.sh` | 无 | 验证 Binary Redaction |
| `run-test-lane.py` | 取决于被测命令 | 写入 Passed、Failed 或 Unavailable JSON Lane 证据 |
| `check-hotspot-baseline.go` | 无 | 校验 Stage 0 热点冻结与拆分后的职责归属 |
| `commanddocs` | 无 | 从 Cobra Command Tree 生成或校验双语命令清单 |
| `upgradebaseline` | 无 | 写入 Stage 0 Benchmark 指标与能力可用性 |
| `stage3experience` | 无 | 校验 Host Journey 契约、可用性指标和已移除产品面 |
| `content-fixture-smoke.sh` | 无 | 临时 Content Dependency Fixture |
| `live-model-smoke.sh` | 有 | 一次真实 Provider 请求，不持久化 Secret |
| `package-release.sh` | 无 | `dist/release` Binary、Checksum、SBOM、Manifest |
| `deepseek-local.sh` | 配置或打包可能联网 | 本机 DeepSeek 编译、Keychain 配置、TUI 与 VS Code |
| `setup-vscode-local.sh` | Package Build 可能安装依赖 | 把 Target VSIX 安装到 macOS 官方 VS Code |

## 约定

脚本必须：

- 解析 Repository Root，不假设调用目录；
- 使用严格错误处理并保留失败退出码；
- 通过环境变量暴露机器相关路径；
- 不打印 Secret，也不从受 Git 跟踪的仓库文档读取 Secret；
- Git 忽略的本机 DeepSeek Runbook 只能作为 Secret Input；
- 只向约定的 Build Directory 写生成产物；
- 使用 Trap 清理临时文件；
- 接受参数时提供 `--help`。

## 常用命令

```bash
make docs-check
make book-check
make book-navigation
make command-docs
make upgrade-baseline
make test
make architecture-freeze
make host-journey-contract
make experience-baseline
make test-platform-capability
make test-integration
BASE_REF=origin/main make doc-impact
make release-fact-check
make doc-external-links
make brand-check
make secret-leak-test
make live-model-smoke
VERSION=0.1.0 make package
make deepseek-init
make deepseek-tui
make deepseek-vscode
make vscode-local-setup
```

完整开发与发布背景见
[docs/zh-CN/development.md](../docs/zh-CN/development.md)。
