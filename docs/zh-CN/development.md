# 本地开发、测试与脚本

简体中文 | [English](../en/development.md)

## 开发环境

必需：

- Go 1.26+
- Git
- Make

开发 VS Code 插件还需要：

- 与 Lockfile 兼容的 Node.js
- npm
- Electron 集成需要 VS Code 1.96+

可选工具：

- `syft`：生成完整 CycloneDX SBOM；
- `tmux`：可 Attach 的 Lane；
- Linux Bubblewrap/Landlock 或 macOS Seatbelt；
- Extension Matrix 所需 Docker/Remote SSH 依赖。

## 初始化

```bash
git clone https://github.com/fwtllh-png/CodeHelper.git
cd CodeHelper
go mod download
make vscode-install
make build
```

`make vscode-install` 使用 `npm ci`，不要通过随意安装依赖改变 Lockfile。

## 快速开发循环

仅 Go 变更：

```bash
gofmt -w path/to/file.go
go test ./path/to/package
go test ./path/to/package -run TestSpecificBehavior -count=1
```

VS Code 变更：

```bash
cd extensions/vscode
npm run check
npm test -- state runtime
```

文档变更：

```bash
make docs-check
make book-check # 修改 docs/book 或 Catalog 时
```

交付前：

```bash
git diff --check
make brand-check
```

## Make Target

### 核心

| Target | 作用 |
| --- | --- |
| `make fmt` | Go 格式化 |
| `make build` | 带 Build Metadata 构建 `bin/codehelper` |
| `make test` | 全部 Go 测试 |
| `make race` | 串行 Race Go 测试 |
| `make smoke` | 构建并验证 Help/Version |
| `make docs-check` | 检查 Markdown 本地链接与双语结构 |
| `make book-check` | 检查知识书籍 Catalog、元数据、镜像和路径 |
| `make book-navigation` | 根据 Catalog 重新生成双语书籍导航 |
| `make verify` | Docs、Brand、VS Code、Vet、Test、Race 综合门禁 |
| `make clean` | 删除生成的 Build/Release 目录 |

### 安全与契约

| Target | 作用 |
| --- | --- |
| `make security-test` | Security、Guard、Plugin、CLI、Engine、App 测试 |
| `make sandbox-attack-test` | Sandbox/File/Shell Attack Corpus |
| `make secret-leak-test` | 发布二进制 Secret 脱敏测试 |
| `make acp-interop` | 真实二进制 ACP Stdio 生命周期 |
| `make api-contract` | 真实二进制 HTTP Contract |
| `make protocol-contract` | ACP 与 HTTP 共享场景 |
| `make protocol-schema` | 重新生成 Runtime Protocol Schema |

### VS Code

| Target | 作用 |
| --- | --- |
| `make vscode-install` | `npm ci` |
| `make vscode-check` | 生成文件漂移、TypeScript、ESLint |
| `make vscode-test` | Extension Unit Test |
| `make vscode-runtime-integration` | 不启动 Electron 的真实 Go Runtime Stdio 测试 |
| `make vscode-integration` | 官方 VS Code Electron Flow |
| `make vscode-security` | Extension Security Test |
| `make vscode-performance` | Projection 与 Runtime Ready 性能预算 |
| `make vscode-package` | 构建并安装本地 VSIX |
| `make vscode-distribution` | 多目标 Dry-run 发布产物 |
| `make vscode-rc` | 完整 Release Candidate Gate |

### Benchmark 与发布

| Target | 作用 |
| --- | --- |
| `make bench` | Hermetic Coding Benchmark |
| `make catalog-bench` | Dynamic Tool Catalog Scale Benchmark |
| `make live-model-smoke` | 显式、非 Hermetic 的真实模型冒烟 |
| `make package VERSION=x.y.z` | 多平台 CLI Package 与 SBOM |

## 仓库脚本

| 脚本 | 行为 |
| --- | --- |
| `scripts/check-docs.sh` | 检查本地 Markdown 链接与双语镜像 |
| `scripts/check-book.sh` | 检查 Agent 工程知识书籍契约 |
| `scripts/render-book-navigation.py` | 根据书籍 Catalog 生成导航 |
| `scripts/check-brand.sh` | 拒绝历史产品品牌残留 |
| `scripts/test-brand-check.sh` | Brand Scanner 自测 |
| `scripts/test-secret-leak.sh` | 对已构建二进制执行脱敏测试 |
| `scripts/live-model-smoke.sh` | 调用一个显式配置的真实模型 |
| `scripts/content-fixture-smoke.sh` | 验证可选内容依赖探测 |
| `scripts/package-release.sh` | 构建五个平台、Checksum、SBOM、Manifest 与 Smoke |
| `scripts/deepseek-local.sh` | 编译配置本机 DeepSeek，并启动 TUI 或 VS Code |
| `scripts/setup-vscode-local.sh` | macOS 官方 VS Code 本地安装 |

`extensions/vscode/scripts` 管理 TypeScript Build、Protocol/Compatibility 生成、
Electron/Remote Integration、VSIX、Release Manifest、Provenance、Matrix Evidence 和
RC Report。

所有脚本必须：

- 能从任意调用目录解析仓库 Root；
- 使用严格错误处理；
- 正确传递失败退出码；
- 除非提供 Override，否则不写个人绝对路径；
- 不打印 Secret，也不从受 Git 跟踪的仓库文档读取 Secret；
- Git 忽略的本机 DeepSeek Runbook 只能作为凭证输入；
- 说明生成目录与清理行为。

## 生成文件

不要手工编辑：

- `docs/protocol/runtime-protocol.schema.json`
- `extensions/vscode/src/protocol/generated.ts`
- `extensions/vscode/src/compatibility/generated.ts`

使用：

```bash
make protocol-schema
cd extensions/vscode
npm run generate:protocol
npm run generate:compatibility
```

生成结果应与源变更一起提交。

## 测试策略

按风险选择测试：

| 变更 | 最低验证 |
| --- | --- |
| 局部纯函数 | Package Test |
| 共享 Runtime 行为 | Package + 依赖 Runtime Test |
| Protocol | Schema Drift + ACP + HTTP Contract |
| Persistence | Repository + State Package Test |
| Guard/Security | 聚焦 Race Test + Attack Corpus |
| VS Code State/UI | Typecheck、ESLint、相关 Node Test |
| Release Script | Dry-run Package/Distribution Gate |
| 产品文档 | `make docs-check` |
| 知识书籍或 Catalog | `make docs-check && make book-check` |

完整 `go test ./...` 包含平台敏感 Integration 和 Benchmark。出现环境失败时，应准确报告
原因，并单独重跑对应 Package，再判断是否回归。

## 平台说明

- macOS 使用平台专用沙箱行为和 Keychain。
- Linux 强度依赖 Kernel 和可用 Sandbox Helper。
- Windows 的进程与文件系统隔离保证不同。
- 高并发 Package Test 下，Race 和真实 Fixture 生命周期测试可能对资源敏感。

不能为了让不支持的平台“看起来支持”而削弱测试。

## 发布开发

CLI Package：

```bash
VERSION=0.1.0 RELEASE_STAGE=experimental make package
```

VS Code Dry-run：

```bash
make vscode-distribution
make vscode-matrix-report
```

正式发布凭证必须从仓库外注入。兼容契约和发布边界见
[VS Code 插件](./vscode.md)。
