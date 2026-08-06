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
source_of_truth:
  - scripts/package-release.sh
  - extensions/vscode/scripts/release/vscode-matrix.mjs
status: verified
last_verified: 2026-08-06
---

# VSIX、SBOM、Provenance 与 Release Evidence

简体中文 | [English](../../en/12-engineering-practice/06-release-evidence.md)

## 学习目标

理解 Build Artifact 如何形成可检查的 Release Evidence。

Core Packaging 生成 Target Binary、Checksum、CycloneDX SBOM、Manifest 与 Install/
Upgrade/Rollback Smoke。VS Code Release 生成 Universal 和五个 Target VSIX，验证精确
内容，并记录 Source Identity、Digest、SBOM、Provenance、Performance Matrix、
Channel Dry-run 与 RC Report。

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

## 验证

```bash
make package
make vscode-release-dry-run
```

## 复习问题

1. SBOM 证明和不证明什么？
2. 为什么区分 Universal/Target VSIX？
3. 什么使 Provenance 可发布？
4. Signature、Provenance、SBOM 各证明什么？
5. Channel Promotion 为什么复用 Artifact Digest？

## 事实来源与验证

| 项目 | 值 |
| --- | --- |
| Catalog ID | `practice-release-evidence` |
| 状态 | `verified` |
| 最后验证 | 2026-08-06 |
