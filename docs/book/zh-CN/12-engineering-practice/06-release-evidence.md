---
id: practice-release-evidence
title: Binary、Web Asset、SBOM 与 Release Evidence
audience:
  - contributor
  - operator
prerequisites:
  - practice-cross-platform
  - host-web
code_paths:
  - scripts
  - web
test_paths:
  - internal/host/runtimeapi/web/server_test.go
  - web/src/runtime/client.test.ts
source_of_truth:
  - scripts/package-release.sh
  - Makefile
  - testdata/contracts/web-feature-parity.json
status: draft
last_verified: null
---

# Binary、Web Asset、SBOM 与 Release Evidence

## 学习目标

理解独立 CodeHelper Binary 如何携带 Web 资源，并通过可重放命令形成发布证据。

## 产物链

`make web-build` 先生成无 Source Map 的 Hash Asset；Go Build 随后通过 `web/embed.go`
把 `web/dist` 嵌入 Binary。`scripts/package-release.sh` 为目标平台构建 Binary、执行
Smoke、生成 Checksum、CycloneDX SBOM 和 Release Manifest。

Web 不是独立部署物。发布时必须证明页面资源来自同一 Binary，且：

- `make web-parity-check` 证明旧 Host 能力均已实现、保留或明确删除；
- `make web-check web-test web-build web-e2e` 证明 TypeScript、Unit、生产 Bundle
  与真实 Chromium 主流程；
- `make web-parity-report` 执行 Ledger 声明的资格测试，并记录 Commit、输入摘要、
  测试命令和嵌入资源摘要；
- `make web-streaming-soak` 连续运行一小时真实 WebSocket Streaming，并约束 Event
  完整性、Heap、Goroutine 和文件描述符增长；
- `make cross-build smoke` 证明目标平台 Binary 与命令面；
- `make web-release-drill PREVIOUS_RELEASE_REF=<上一发布提交>` 证明 Web RC Data Dir
  经逐文件摘要备份和恢复后，上一 Binary 仍可 List、Load 并读取 Session History；
- `make secret-leak-test` 证明产物和日志不泄漏凭证；
- `make docs-check book-check` 证明文档事实与发布命令一致。

## 回滚

运行中的 Web 进程不自替换。升级与回滚由外部包管理器或 Release Artifact 完成，重启
后由 Runtime 执行持久化恢复。这样不会让浏览器获得替换可执行文件的权限，也不会把
下载器变成第二个安装控制面。

## 验证

```bash
make web-build
make web-e2e
make build
make test-release
```

`make test-release` 只接受 clean Commit。Release Gate 在全部资格测试、Web Asset
重建和降级演练结束后再次检查工作树；源码或生成资源发生漂移时，即使测试本身通过，
也不能产出 `verified` 发布结论。

## 事实来源与验证

| 项目 | 值 |
| --- | --- |
| Catalog ID | `practice-release-evidence` |
| 状态 | `draft` |
| 最后验证 | 尚未验证 |
