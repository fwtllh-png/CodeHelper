# VS Code Extension Release Runbook

本文档描述 RFC-014 V3 的 release candidate、签名和渠道发布流程。

## 1. 支持矩阵

| Workspace Extension Host | VSIX | Runtime source | 动态证据 |
| --- | --- | --- | --- |
| macOS arm64 | universal / darwin-arm64 | external / bundled | single、multi、bundled handshake |
| macOS x64 | universal / darwin-x64 | external；bundled 静态审计 | Rosetta 下 single、multi |
| Linux arm64 Remote SSH | universal / linux-arm64 | external / managed | single、multi、Host restart/reattach |
| Linux arm64 Dev Container | universal / linux-arm64 | managed | fresh attach、single、multi |
| Linux amd64 Dev Container | universal / linux-x64 | managed | QEMU 下 fresh attach、single、multi |
| Windows x64 | universal / win32-x64 | external / bundled | 仅 PE、manifest、VSIX 静态审计 |

不支持 `win32-arm64`、`linux-armhf`。WSL2、公共 Codespaces 和 Windows x64
动态 E2E 当前不可用，不得写成已验证。Dev Container gate 不宣称 disconnect
自动 re-attach；容器内 Runtime restart/replay 已验证。

兼容契约来自 `extensions/vscode/compatibility.json`：

- VS Code `^1.96.0`；
- extension `0.0.1`；
- binary `>=0.0.1 <0.1.0`；
- ACP protocol `1..2`；
- operation schema `1`；
- required features：`editor_context_v2`、`workspace_identity_v1`。

## 2. RC 门禁

```bash
make vscode-rc
```

该命令串行执行：

1. TypeScript、ESLint、protocol/compatibility drift；
2. 真实 Runtime stdio、security 和 performance；
3. 官方 VS Code Electron native flow；
4. signed update、rollback、revocation、ETag/cache；
5. universal + 五 target VSIX、install/handshake；
6. 15/15 required E2E evidence；
7. npm dependency audit、release secret scan；
8. SBOM、provenance、manifest signature 和 SHA256SUMS 独立复验。

结果位于：

```text
extensions/vscode/dist/rc/report.json
extensions/vscode/dist/rc/report.md
extensions/vscode/dist/rc/compatibility-report.json
```

当前工作树不干净或使用临时 dry-run key 时，报告只能是
`candidate_kind=validated-dry-run`、`publishable=false`、`uploaded=false`。

## 3. 正式签名

正式构建必须从 clean worktree 执行，并从仓库外注入：

```bash
export CODEHELPER_RELEASE_PRIVATE_KEY=/secure/path/release-private.pem
export CODEHELPER_RELEASE_TRUST_ROOTS=/secure/path/release-trust-roots.json
export CODEHELPER_RELEASE_KEY_ID=release-2026
export CODEHELPER_RELEASE_VERSION=0.0.1
export CODEHELPER_RELEASE_SEQUENCE=1

cd extensions/vscode
npm run build
node ./scripts/release/prepare-vscode-release.mjs
npm run matrix:report
npm run release:vscode:rc
```

私钥与 public trust root 不匹配、source tree dirty、manifest sequence 非法或 required
证据缺失时必须失败。provenance 同时绑定 commit、source fingerprint、compatibility、
manifest、public trust roots、SBOM、channel mapping 和全部 VSIX digest。

## 4. 渠道发布

RC 通过后检查 `dist/vscode-release/channels/*/publication.json`：

- `dry_run=false`；
- `uploaded=false`；
- artifact digest 与 RC report 一致；
- Marketplace/Open VSX credential 仅在发布执行器中注入。

随后按 plan 执行渠道 CLI。CLI 成功后才允许单独记录 `uploaded=true` 的外部发布收据；
构建脚本本身永远不宣称上传成功。企业/离线目录必须连同 `SHA256SUMS`、SBOM 和完整
`provenance/` 一起交付。

## 5. 回滚与撤销

- 撤销 binary 时发布更高 sequence 的 signed manifest，并加入 version 或 digest；
- active 与 last-known-good 命中 revocation 时均不得启动；
- extension 回滚使用前一份已验签 VSIX 与对应 provenance，不修改旧 manifest；
- key rotation 必须由当前 trust root 签发 statement，不能直接替换仓库 trust root；
- 渠道事故不得复用 sequence 或重新签发相同版本的不同 bytes。
