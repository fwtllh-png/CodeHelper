---
id: practice-release-evidence
title: VSIX、SBOM、Provenance 与 Release Evidence
audience:
  - contributor
  - operator
prerequisites:
  - practice-cross-platform
  - host-vscode
code_paths:
  - scripts
  - extensions/vscode/scripts/release
test_paths:
  - extensions/vscode/src/binary/manifest.test.ts
  - extensions/vscode/src/binary/store.test.ts
  - extensions/vscode/src/test/suite/index.ts
source_of_truth:
  - scripts/package-release.sh
  - extensions/vscode/scripts/release/vscode-matrix.mjs
  - extensions/vscode/scripts/matrix/journeys.mjs
  - extensions/vscode/RELEASE-EVIDENCE.md
status: draft
last_verified: null
---

# VSIX、SBOM、Provenance 与 Release Evidence

## 学习目标

理解 Build Artifact 如何形成可检查的 Release Evidence。

Core Packaging 生成 Target Binary、Checksum、CycloneDX SBOM、Manifest 与 Install/
Upgrade/Rollback Smoke。VS Code Release 生成 Universal 和五个 Target VSIX，验证精确
内容，并记录 Source Identity、Digest、SBOM、Provenance、Performance Matrix、
Channel Dry-run 与 RC Report。
`RELEASE-EVIDENCE.md` 与 Machine-readable Journey Manifest 把产品验收路径绑定到
Automated 或可复现 Manual Evidence。

Universal VSIX 不含 Binary；Target VSIX 只含对应 Binary。Signed Manifest 绑定 Target、
Version、Digest，支持 Authorized Key Rotation/Revocation。Dirty Source 或 Dry-run
Signing 被记录，不能产生 Publishable Claim。

## Evidence Chain

```text
source revision + dirty state
 -> deterministic build/toolchain
 -> artifact bytes + target
 -> sha-256 + sbom
 -> signed manifest
 -> install/handshake/upgrade/rollback smoke
 -> provenance + rc report
 -> publication record
```

每一环引用上一环 Digest/Identity。Provenance 描述 How Built；Signature 授权 Exact
Byte/Metadata；SBOM 列出 Component。任何一项都不单独证明 Source Correctness、无漏洞或
Install Success。

## Journey 与 Matrix Evidence

VS Code Matrix 要求 7 条 Job Record 和完整 Journey Set。Electron Evidence 提交
Automated Journey ID；Report 拒绝任何缺失 ID。Panel-move Journey 因 View Container
Movement 由 VS Code 拥有而保留为 Manual，但复现步骤会打包进 VSIX，RC 要求其存在。

当前收口为 Journey 20/20：19 个 Automated、1 个 Documented Manual。覆盖 Local
Single-root/Multi-root、ARM64/Rosetta x64、Native Context/Navigation、Accessibility、
Incremental Transcript、Hidden Resume、Retry/Continue、Session Lifecycle/Search
与 Plan Destination。发布不声明 Remote SSH、Dev Container、Codespaces 或 Full
Editor Chat。

RC 验证 Matrix/Journey 完整性、Compatibility、Performance Field Presence/Budget、
Dependency Audit、Secret Scan、VSIX Allowlist、SBOM、Provenance、Signature 与
Checksum。Validated Dry Run 仍明确记录 `publishable=false`、`uploaded=false`。

## Reproducibility/Promotion

`-trimpath`、Fixed Version、Controlled Dependency、Stable Archive、Isolated Output 降低
Variation。Rebuild 应匹配 Byte，或解释 Signed/Reviewed Nondeterminism。

Promotion 复用 Verified Artifact Digest，不为新 Channel 重建“相同”版本。Dry-run Key、
Dirty Source、Fallback SBOM、Missing Native Matrix 均明确 Non-publishable。

Rollback 在 Activation 前验证 Previous Artifact/Compatibility。Revocation 阻止本地
Cached Known-bad Manifest。

## 失败边界

- Forbidden/Missing VSIX File 失败。
- Artifact Digest 必须匹配 Provenance。
- Unsafe URL、Replay、Revocation、Unknown Key Fail Closed。
- Smoke 使用 Fake Credential/Bounded Temp State。
- Dry Run 不等于已签名发布。
- Automated Journey 缺失或 Manual Journey 未记录时 RC 失败。
- Provenance 生成后 Source 变化会使 RC 失效。

## 验证

```bash
make package
make vscode-release-dry-run
make vscode-rc
```

## 复习问题

1. SBOM 证明和不证明什么？
2. 为什么区分 Universal/Target VSIX？
3. 什么使 Provenance 可发布？
4. Signature、Provenance、SBOM 各证明什么？
5. Channel Promotion 为什么复用 Artifact Digest？
6. Source Edit 后为什么必须重新生成 Provenance？
7. Panel-move 为什么记录为 Manual，而不是伪装 Automated？

## 事实来源与验证

| 项目 | 值 |
| --- | --- |
| Catalog ID | `practice-release-evidence` |
| 状态 | `draft` |
| 最后验证 | 尚未验证 |
